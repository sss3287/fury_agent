//go:build linux

package ebpfwatch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"
)

const (
	ringBufferBytes = 8 << 20
	trackedPIDLimit = 1 << 16
	pathBytes       = 128
	eventHeaderSize = 88
	eventSize       = eventHeaderSize + pathBytes
)

// Observer owns the BPF programs, maps, links, and event reader for one agent
// run. The root process is tracked at startup; sched_process_fork propagates
// membership to child processes so process tools and their descendants remain
// correlated with the agent session.
type Observer struct {
	reader  *ringbuf.Reader
	links   []link.Link
	progs   []*ebpf.Program
	maps    []*ebpf.Map
	stop    context.CancelFunc
	done    chan struct{}
	onEvent func(Event)
	once    sync.Once
}

// Start loads and attaches native eBPF tracepoint programs. It requires a
// Linux kernel with tracepoints and ring buffers, plus the privileges required
// by the kernel to load and attach BPF programs.
func Start(ctx context.Context, rootPID uint32, onEvent func(Event)) (*Observer, error) {
	if rootPID == 0 {
		return nil, errors.New("root PID must be positive")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf("eBPF syscall decoding is not implemented for %s", runtime.GOARCH)
	}
	if onEvent == nil {
		return nil, errors.New("event handler is required")
	}
	if err := rlimit.RemoveMemlock(); err != nil && !errors.Is(err, unix.EPERM) {
		return nil, fmt.Errorf("raise BPF memlock limit: %w", err)
	}
	tracked, err := ebpf.NewMap(&ebpf.MapSpec{
		Name: "fury_pids", Type: ebpf.Hash, KeySize: 4, ValueSize: 4, MaxEntries: trackedPIDLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("create tracked PID map: %w", err)
	}
	events, err := ebpf.NewMap(&ebpf.MapSpec{
		Name: "fury_events", Type: ebpf.RingBuf, MaxEntries: ringBufferBytes,
	})
	if err != nil {
		tracked.Close()
		return nil, fmt.Errorf("create event ring buffer: %w", err)
	}
	dropped, err := ebpf.NewMap(&ebpf.MapSpec{
		Name: "fury_dropped", Type: ebpf.PerCPUArray, KeySize: 4, ValueSize: 8, MaxEntries: 1,
	})
	if err != nil {
		tracked.Close()
		events.Close()
		return nil, fmt.Errorf("create dropped-event counter: %w", err)
	}
	rootValue := uint32(1)
	if err := tracked.Put(rootPID, rootValue); err != nil {
		tracked.Close()
		events.Close()
		dropped.Close()
		return nil, fmt.Errorf("track root PID %d: %w", rootPID, err)
	}
	programs, err := makePrograms(tracked, events, dropped)
	if err != nil {
		tracked.Close()
		events.Close()
		dropped.Close()
		return nil, fmt.Errorf("load BPF programs: %w", err)
	}
	reader, err := ringbuf.NewReader(events)
	if err != nil {
		closePrograms(programs)
		tracked.Close()
		events.Close()
		dropped.Close()
		return nil, fmt.Errorf("open BPF event reader: %w", err)
	}
	var links []link.Link
	for _, spec := range []struct {
		group string
		name  string
		prog  *ebpf.Program
	}{
		{group: "raw_syscalls", name: "sys_enter", prog: programs[0]},
		{group: "raw_syscalls", name: "sys_exit", prog: programs[1]},
		{group: "sched", name: "sched_process_fork", prog: programs[2]},
	} {
		attached, attachErr := link.Tracepoint(spec.group, spec.name, spec.prog, nil)
		if attachErr != nil {
			for _, l := range links {
				l.Close()
			}
			reader.Close()
			closePrograms(programs)
			tracked.Close()
			events.Close()
			dropped.Close()
			return nil, fmt.Errorf("attach %s:%s tracepoint: %w", spec.group, spec.name, attachErr)
		}
		links = append(links, attached)
	}
	runCtx, cancel := context.WithCancel(ctx)
	o := &Observer{
		reader: reader, links: links, progs: programs, maps: []*ebpf.Map{tracked, events, dropped},
		stop: cancel, done: make(chan struct{}), onEvent: onEvent,
	}
	go o.readEvents(runCtx, onEvent)
	return o, nil
}

// Close detaches the tracepoints, stops event delivery, and releases all BPF
// resources. It is safe to call more than once.
func (o *Observer) Close() error {
	var first error
	o.once.Do(func() {
		for _, l := range o.links {
			if err := l.Close(); err != nil && first == nil {
				first = err
			}
		}
		o.stop()
		<-o.done
		if dropped, err := o.droppedEvents(); err != nil {
			if first == nil {
				first = fmt.Errorf("read dropped-event counter: %w", err)
			}
		} else if dropped > 0 {
			o.onEvent(Event{Dropped: dropped})
		}
		if err := o.reader.Close(); err != nil && first == nil {
			first = err
		}
		for _, p := range o.progs {
			if err := p.Close(); err != nil && first == nil {
				first = err
			}
		}
		for _, m := range o.maps {
			if err := m.Close(); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}

func (o *Observer) readEvents(ctx context.Context, onEvent func(Event)) {
	defer close(o.done)
	o.reader.SetDeadline(time.Now().Add(100 * time.Millisecond))
	for {
		record, err := o.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if ctx.Err() != nil && o.reader.AvailableBytes() == 0 {
					return
				}
				o.reader.SetDeadline(time.Now().Add(100 * time.Millisecond))
				continue
			}
			onEvent(Event{Error: "read BPF ring buffer: " + err.Error()})
			return
		}
		o.reader.SetDeadline(time.Now().Add(100 * time.Millisecond))
		event, err := decodeEvent(record.RawSample)
		if err != nil {
			onEvent(Event{Error: "decode BPF event: " + err.Error()})
			continue
		}
		event.Syscall = syscallName(event.SyscallID)
		event.MemoryOp = memoryOperation(event.SyscallID)
		onEvent(event)
	}
}

func (o *Observer) droppedEvents() (uint64, error) {
	key := uint32(0)
	var perCPU []uint64
	if err := o.maps[2].Lookup(&key, &perCPU); err != nil {
		return 0, err
	}
	var total uint64
	for _, count := range perCPU {
		total += count
	}
	return total, nil
}

func makePrograms(tracked, events, dropped *ebpf.Map) ([]*ebpf.Program, error) {
	sysEnter := &ebpf.ProgramSpec{
		Name: "fury_sys_enter", Type: ebpf.TracePoint, License: "GPL",
		Instructions: syscallProgram(tracked.FD(), events.FD(), dropped.FD(), pathArgumentIndexes(), 1),
	}
	sysExit := &ebpf.ProgramSpec{
		Name: "fury_sys_exit", Type: ebpf.TracePoint, License: "GPL",
		Instructions: syscallProgram(tracked.FD(), events.FD(), dropped.FD(), nil, 2),
	}
	fork := &ebpf.ProgramSpec{
		Name: "fury_fork", Type: ebpf.TracePoint, License: "GPL",
		Instructions: forkProgram(tracked.FD()),
	}
	first, err := ebpf.NewProgram(sysEnter)
	if err != nil {
		return nil, fmt.Errorf("load syscall observer: %w", err)
	}
	second, err := ebpf.NewProgram(sysExit)
	if err != nil {
		first.Close()
		return nil, fmt.Errorf("load syscall completion observer: %w", err)
	}
	third, err := ebpf.NewProgram(fork)
	if err != nil {
		first.Close()
		second.Close()
		return nil, fmt.Errorf("load process tree observer: %w", err)
	}
	return []*ebpf.Program{first, second, third}, nil
}

func syscallProgram(trackedFD, eventsFD, droppedFD int, pathArgs map[int32]int16, phase int16) asm.Instructions {
	ins := asm.Instructions{
		asm.Mov.Reg(asm.R6, asm.R1),
		asm.LoadMem(asm.R8, asm.R6, 8, asm.DWord), // raw_syscalls:sys_enter.id
		asm.FnGetCurrentPidTgid.Call(),
		asm.Mov.Reg(asm.R7, asm.R0),
		asm.RSh.Imm(asm.R0, 32),
		asm.StoreMem(asm.RFP, -4, asm.R0, asm.Word),
		asm.LoadMapPtr(asm.R1, trackedFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "exit"),
		asm.StoreImm(asm.RFP, -eventSize, 0, asm.DWord),
	}
	for offset := int16(-eventSize + 8); offset < 0; offset += 8 {
		ins = append(ins, asm.StoreImm(asm.RFP, offset, 0, asm.DWord))
	}
	if phase == 1 {
		for _, id := range syscallIDs() {
			if _, ok := pathArgs[id]; ok {
				ins = append(ins, asm.JEq.Imm(asm.R8, id, fmt.Sprintf("path_%d", id)))
			}
		}
		ins = append(ins, asm.Ja.Label("write"))
		for _, id := range syscallIDs() {
			index, ok := pathArgs[id]
			if !ok {
				continue
			}
			ins = append(ins,
				asm.LoadMem(asm.R9, asm.R6, 16+index*8, asm.DWord).WithSymbol(fmt.Sprintf("path_%d", id)),
				asm.Mov.Reg(asm.R1, asm.RFP),
				asm.Add.Imm(asm.R1, -eventSize+eventHeaderSize),
				asm.Mov.Imm(asm.R2, pathBytes),
				asm.Mov.Reg(asm.R3, asm.R9),
				asm.FnProbeReadUserStr.Call(),
				asm.Ja.Label("write"),
			)
		}
	}
	ins = append(ins,
		asm.FnKtimeGetNs.Call().WithSymbol("write"),
		asm.StoreMem(asm.RFP, -eventSize, asm.R0, asm.DWord),
		asm.Mov.Reg(asm.R0, asm.R7),
		asm.RSh.Imm(asm.R0, 32),
		asm.StoreMem(asm.RFP, -eventSize+8, asm.R0, asm.Word),
		asm.Mov.Reg(asm.R0, asm.R7),
		asm.LSh.Imm(asm.R0, 32),
		asm.RSh.Imm(asm.R0, 32),
		asm.StoreMem(asm.RFP, -eventSize+12, asm.R0, asm.Word),
		asm.StoreMem(asm.RFP, -eventSize+16, asm.R8, asm.DWord),
	)
	if phase == 2 {
		ins = append(ins,
			asm.LoadMem(asm.R0, asm.R6, 16, asm.DWord), // raw_syscalls:sys_exit.ret
			asm.StoreMem(asm.RFP, -eventSize+24, asm.R0, asm.DWord),
		)
	} else {
		ins = append(ins, asm.StoreImm(asm.RFP, -eventSize+24, 0, asm.DWord))
	}
	ins = append(ins, asm.StoreImm(asm.RFP, -eventSize+32, int64(phase), asm.Byte))
	for offset := int16(-eventSize + 33); offset < -eventSize+eventHeaderSize; offset++ {
		ins = append(ins, asm.StoreImm(asm.RFP, offset, 0, asm.Byte))
	}
	if phase == 1 {
		for arg := int16(0); arg < 6; arg++ {
			ins = append(ins,
				asm.LoadMem(asm.R0, asm.R6, 16+arg*8, asm.DWord),
				asm.StoreMem(asm.RFP, -eventSize+40+arg*8, asm.R0, asm.DWord),
			)
		}
	}
	ins = append(ins,
		asm.LoadMapPtr(asm.R1, eventsFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -eventSize),
		asm.Mov.Imm(asm.R3, eventSize),
		asm.Mov.Imm(asm.R4, 0),
		asm.FnRingbufOutput.Call(),
		asm.JEq.Imm(asm.R0, 0, "return"),
		asm.StoreImm(asm.RFP, -4, 0, asm.Word),
		asm.LoadMapPtr(asm.R1, droppedFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "return"),
		asm.Mov.Imm(asm.R7, 1),
		asm.StoreXAdd(asm.R0, asm.R7, asm.DWord),
		asm.Mov.Imm(asm.R0, 0),
		asm.Return().WithSymbol("return"),
		asm.Mov.Imm(asm.R0, 0).WithSymbol("exit"),
		asm.Return(),
	)
	return ins
}

func forkProgram(trackedFD int) asm.Instructions {
	return asm.Instructions{
		asm.Mov.Reg(asm.R6, asm.R1),
		asm.FnGetCurrentPidTgid.Call(),
		asm.Mov.Reg(asm.R7, asm.R0),
		asm.RSh.Imm(asm.R0, 32),
		asm.StoreMem(asm.RFP, -4, asm.R0, asm.Word),
		asm.LoadMapPtr(asm.R1, trackedFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "exit"),
		asm.LoadMem(asm.R0, asm.R6, 44, asm.Word), // sched_process_fork.child_pid
		asm.StoreMem(asm.RFP, -4, asm.R0, asm.Word),
		asm.StoreImm(asm.RFP, -8, 1, asm.Word),
		asm.LoadMapPtr(asm.R1, trackedFD),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),
		asm.Mov.Reg(asm.R3, asm.RFP),
		asm.Add.Imm(asm.R3, -8),
		asm.Mov.Imm(asm.R4, 0), // BPF_ANY
		asm.FnMapUpdateElem.Call(),
		asm.Mov.Imm(asm.R0, 0),
		asm.Return(),
		asm.Mov.Imm(asm.R0, 0).WithSymbol("exit"),
		asm.Return(),
	}
}

type eventWire struct {
	TimestampNS uint64
	PID         uint32
	TID         uint32
	SyscallID   int64
	ReturnValue int64
	Phase       uint8
	Padding     [7]byte
	Args        [6]uint64
	Path        [pathBytes]byte
}

func decodeEvent(raw []byte) (Event, error) {
	if len(raw) != eventSize {
		return Event{}, fmt.Errorf("unexpected event size %d", len(raw))
	}
	order := binary.NativeEndian
	path := raw[eventHeaderSize:]
	if end := bytes.IndexByte(path, 0); end >= 0 {
		path = path[:end]
	}
	phase := raw[32]
	event := Event{
		TimestampNS: order.Uint64(raw[0:8]),
		PID:         order.Uint32(raw[8:12]),
		TID:         order.Uint32(raw[12:16]),
		SyscallID:   int64(order.Uint64(raw[16:24])),
		Phase:       map[byte]string{1: "enter", 2: "exit"}[phase],
		Path:        string(path),
	}
	if phase == 1 {
		event.Args = make([]uint64, 6)
		for i := range event.Args {
			event.Args[i] = order.Uint64(raw[40+i*8 : 48+i*8])
		}
	}
	if phase == 2 {
		ret := int64(order.Uint64(raw[24:32]))
		event.ReturnValue = &ret
	}
	if event.Phase == "" {
		return Event{}, fmt.Errorf("unknown event phase %d", phase)
	}
	return event, nil
}

func closePrograms(programs []*ebpf.Program) {
	for _, program := range programs {
		program.Close()
	}
}

// HostPID returns the process ID used by the kernel's initial PID namespace.
// BPF helpers report kernel task IDs, while os.Getpid may be namespace-local.
func HostPID() (uint32, error) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "NSpid:") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "NSpid:"))
		if len(fields) == 0 {
			break
		}
		pid, parseErr := strconv.ParseUint(fields[0], 10, 32)
		if parseErr != nil || pid == 0 {
			return 0, fmt.Errorf("invalid NSpid in /proc/self/status")
		}
		return uint32(pid), nil
	}
	pid := os.Getpid()
	if pid <= 0 {
		return 0, fmt.Errorf("invalid process ID %d", pid)
	}
	return uint32(pid), nil
}

func syscallIDs() []int32 {
	// Syscall numbers differ between architectures. Keep the monitored set
	// explicit so unrelated high-volume syscalls do not flood the ring buffer.
	if runtime.GOARCH == "arm64" {
		return []int32{34, 35, 38, 39, 40, 51, 56, 93, 94, 97, 105, 106, 117, 196, 197, 198, 199, 200, 201, 202, 203, 214, 215, 216, 220, 221, 222, 226, 228, 229, 230, 231, 233, 242, 268, 271, 273, 276, 280, 281, 284, 288, 435, 437, 440, 447, 448, 462}
	}
	return []int32{2, 9, 10, 11, 12, 25, 28, 30, 41, 42, 43, 49, 50, 53, 56, 57, 58, 59, 60, 67, 82, 83, 84, 87, 101, 149, 150, 151, 152, 161, 165, 166, 175, 176, 231, 257, 258, 263, 264, 272, 288, 308, 310, 311, 313, 316, 321, 322, 323, 325, 329, 435, 437, 440, 447, 448, 462}
}

func pathArgumentIndexes() map[int32]int16 {
	if runtime.GOARCH == "arm64" {
		return map[int32]int16{34: 1, 35: 1, 38: 1, 56: 1, 221: 0, 276: 1, 281: 1, 437: 1}
	}
	return map[int32]int16{2: 0, 59: 0, 82: 0, 83: 0, 84: 0, 87: 0, 257: 1, 258: 1, 263: 1, 264: 1, 316: 1, 322: 1, 437: 1}
}

func syscallName(id int64) string {
	for i, number := range syscallIDs() {
		if int64(number) == id {
			if runtime.GOARCH == "arm64" {
				return arm64SyscallNames[i]
			}
			return amd64SyscallNames[i]
		}
	}
	return fmt.Sprintf("syscall_%d", id)
}

// memoryOperation classifies syscalls that create, remove, resize, or change
// permissions on virtual-memory regions, and syscalls that can modify a
// different process's address space. Entry args and the matching exit result
// provide the range and outcome; this does not observe ordinary CPU stores.
func memoryOperation(id int64) string {
	return memoryOperationForArch(runtime.GOARCH, id)
}

func memoryOperationForArch(arch string, id int64) string {
	if arch == "arm64" {
		switch id {
		case 117:
			return "ptrace"
		case 196:
			return "shmat"
		case 197:
			return "shmdt"
		case 214:
			return "brk"
		case 215:
			return "munmap"
		case 216:
			return "mremap"
		case 221:
			return "execve"
		case 281:
			return "execveat"
		case 222:
			return "mmap"
		case 226:
			return "mprotect"
		case 228, 229, 230, 231, 284:
			return syscallName(id)
		case 233:
			return "madvise"
		case 271:
			return "process_vm_writev"
		case 288:
			return "pkey_mprotect"
		case 440:
			return "process_madvise"
		case 447:
			return "memfd_secret"
		case 448:
			return "process_mrelease"
		case 462:
			return "mseal"
		}
		return ""
	}
	switch id {
	case 59:
		return "execve"
	case 322:
		return "execveat"
	case 101:
		return "ptrace"
	case 149, 150, 151, 152, 325:
		return syscallName(id)
	case 9:
		return "mmap"
	case 10:
		return "mprotect"
	case 11:
		return "munmap"
	case 12:
		return "brk"
	case 25:
		return "mremap"
	case 28:
		return "madvise"
	case 30:
		return "shmat"
	case 67:
		return "shmdt"
	case 311:
		return "process_vm_writev"
	case 329:
		return "pkey_mprotect"
	case 440:
		return "process_madvise"
	case 447:
		return "memfd_secret"
	case 448:
		return "process_mrelease"
	case 462:
		return "mseal"
	}
	return ""
}

var amd64SyscallNames = []string{"open", "mmap", "mprotect", "munmap", "brk", "mremap", "madvise", "shmat", "socket", "connect", "accept", "bind", "listen", "socketpair", "clone", "fork", "vfork", "execve", "exit", "shmdt", "rename", "mkdir", "rmdir", "unlink", "ptrace", "mlock", "munlock", "mlockall", "munlockall", "chroot", "mount", "umount2", "init_module", "delete_module", "exit_group", "openat", "mkdirat", "unlinkat", "renameat", "unshare", "accept4", "setns", "process_vm_readv", "process_vm_writev", "finit_module", "renameat2", "bpf", "execveat", "userfaultfd", "mlock2", "pkey_mprotect", "clone3", "openat2", "process_madvise", "memfd_secret", "process_mrelease", "mseal"}
var arm64SyscallNames = []string{"mkdirat", "unlinkat", "renameat", "umount2", "mount", "chroot", "openat", "exit", "exit_group", "unshare", "init_module", "delete_module", "ptrace", "shmat", "shmdt", "socket", "socketpair", "bind", "listen", "accept", "connect", "brk", "munmap", "mremap", "clone", "execve", "mmap", "mprotect", "mlock", "munlock", "mlockall", "munlockall", "madvise", "accept4", "setns", "process_vm_writev", "finit_module", "renameat2", "bpf", "execveat", "mlock2", "pkey_mprotect", "clone3", "openat2", "process_madvise", "memfd_secret", "process_mrelease", "mseal"}
