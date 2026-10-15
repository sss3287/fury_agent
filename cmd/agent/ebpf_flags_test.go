package main

import (
	"testing"

	"github.com/ahhh/fury_agent/internal/config"
)

func TestEBPFFlags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		enabled  bool
		required bool
	}{
		{name: "disabled by default"},
		{name: "best effort", args: []string{"--enable-ebpf"}, enabled: true},
		{name: "required", args: []string{"--require-ebpf"}, enabled: true, required: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags, _, err := parseFlags(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			rt := config.Defaults()
			flags.apply(&rt)
			if rt.EnableEBPF != tc.enabled || rt.RequireEBPF != tc.required {
				t.Fatalf("enable=%v required=%v, want %v/%v", rt.EnableEBPF, rt.RequireEBPF, tc.enabled, tc.required)
			}
		})
	}
}
