package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{name: "injected release", injected: "v0.4.1", info: &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, ok: true, want: "v0.4.1"},
		{name: "module release", injected: "dev", info: &debug.BuildInfo{Main: debug.Module{Version: "v0.4.1"}}, ok: true, want: "v0.4.1"},
		{name: "local build", injected: "dev", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, ok: true, want: "dev"},
		{name: "missing build info", injected: "dev", ok: false, want: "dev"},
		{name: "empty fallback", info: &debug.BuildInfo{}, ok: true, want: "dev"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveVersion(test.injected, test.info, test.ok); got != test.want {
				t.Fatalf("resolveVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
