package ebpfwatch

// Event is the stable, privacy-conscious syscall record written by the BPF
// program. It omits raw arguments and user buffers, except for bounded pathname
// strings on selected file and exec syscalls.
type Event struct {
	TimestampNS uint64 `json:"ktime_ns,omitempty"`
	PID         uint32 `json:"pid,omitempty"`
	TID         uint32 `json:"tid,omitempty"`
	SyscallID   int64  `json:"syscall_id,omitempty"`
	Syscall     string `json:"syscall,omitempty"`
	Phase       string `json:"phase,omitempty"`
	ReturnValue *int64 `json:"return_value,omitempty"`
	Path        string `json:"path,omitempty"`
	Error       string `json:"error,omitempty"`
}
