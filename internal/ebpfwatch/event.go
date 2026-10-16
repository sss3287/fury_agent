package ebpfwatch

// Event is a syscall record written by the BPF program. Arguments are raw
// syscall register values; pointer arguments are addresses, not pointed-to data.
type Event struct {
	TimestampNS uint64   `json:"ktime_ns,omitempty"`
	PID         uint32   `json:"pid,omitempty"`
	TID         uint32   `json:"tid,omitempty"`
	SyscallID   int64    `json:"syscall_id,omitempty"`
	Syscall     string   `json:"syscall,omitempty"`
	Phase       string   `json:"phase,omitempty"`
	Args        []uint64 `json:"args,omitempty"`
	ReturnValue *int64   `json:"return_value,omitempty"`
	Path        string   `json:"path,omitempty"`
	MemoryOp    string   `json:"memory_op,omitempty"`
	Dropped     uint64   `json:"dropped_events,omitempty"`
	Error       string   `json:"error,omitempty"`
}
