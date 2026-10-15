//go:build !linux

package ebpfwatch

import (
	"context"
	"errors"
)

type Observer struct{}

func Start(context.Context, uint32, func(Event)) (*Observer, error) {
	return nil, errors.New("eBPF observation is supported only on Linux")
}

func (*Observer) Close() error { return nil }

func HostPID() (uint32, error) { return 0, errors.New("host PID lookup is supported only on Linux") }
