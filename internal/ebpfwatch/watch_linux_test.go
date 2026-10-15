//go:build linux

package ebpfwatch

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
	"unsafe"

	"github.com/cilium/ebpf/asm"
)

func TestProgramInstructionsResolveAndEncode(t *testing.T) {
	for name, instructions := range map[string][]byte{
		"sys_enter": marshalProgram(t, syscallProgram(1, 2, syscallIDs(), pathArgumentIndexes(), 1)),
		"sys_exit":  marshalProgram(t, syscallProgram(1, 2, syscallIDs(), nil, 2)),
		"fork":      marshalProgram(t, forkProgram(1)),
	} {
		if len(instructions) == 0 || len(instructions)%8 != 0 {
			t.Fatalf("%s program encoded to invalid length %d", name, len(instructions))
		}
	}
}

func marshalProgram(t *testing.T, instructions asm.Instructions) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := instructions.Marshal(&b, binary.LittleEndian); err != nil {
		t.Fatalf("encode BPF program: %v", err)
	}
	return b.Bytes()
}

func TestSyscallNumbersAndNamesStayAligned(t *testing.T) {
	ids, names := syscallIDs(), syscallNames()
	if len(ids) == 0 || len(ids) != len(names) {
		t.Fatalf("syscall table mismatch: %d IDs, %d names", len(ids), len(names))
	}
	for i, id := range ids {
		if got := syscallName(int64(id)); got != names[i] {
			t.Errorf("syscall %d: got %q, want %q", id, got, names[i])
		}
	}
}

func TestDecodeEventUsesKernelWireLayout(t *testing.T) {
	var raw [eventSize]byte
	order := binary.NativeEndian
	order.PutUint64(raw[0:8], 987654321)
	order.PutUint32(raw[8:12], 321)
	order.PutUint32(raw[12:16], 654)
	order.PutUint64(raw[16:24], 257)
	raw[32] = 1
	copy(raw[eventHeaderSize:], "/tmp/workspace/file.txt\x00ignored")
	got, err := decodeEvent(raw[:])
	if err != nil {
		t.Fatal(err)
	}
	want := Event{TimestampNS: 987654321, PID: 321, TID: 654, SyscallID: 257, Phase: "enter", Path: "/tmp/workspace/file.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if unsafe.Sizeof(eventWire{}) != eventSize {
		t.Fatalf("wire layout is %d bytes, want %d", unsafe.Sizeof(eventWire{}), eventSize)
	}
}

func TestDecodeExitEventHasReturnValue(t *testing.T) {
	var raw [eventSize]byte
	order := binary.NativeEndian
	order.PutUint64(raw[16:24], 42)
	order.PutUint64(raw[24:32], ^uint64(0)) // -1, a kernel errno result
	raw[32] = 2
	got, err := decodeEvent(raw[:])
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "exit" || got.ReturnValue == nil || *got.ReturnValue != -1 {
		t.Fatalf("unexpected exit event: %+v", got)
	}
}

func TestDecodeEventRejectsWrongSize(t *testing.T) {
	if _, err := decodeEvent(make([]byte, eventSize-1)); err == nil {
		t.Fatal("expected malformed event to fail")
	}
}

func syscallNames() []string {
	if len(arm64SyscallNames) == len(syscallIDs()) && len(arm64SyscallNames) != len(amd64SyscallNames) {
		return arm64SyscallNames
	}
	return amd64SyscallNames
}
