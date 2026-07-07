// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"slices"
	"testing"
)

// TestProvidersFor pins the EnvFaceEP → provider-order routing without touching the ONNX runtime.
// The load-bearing case: CoreML is opt-in, so it is returned only when explicitly requested and is
// NEVER in the auto (unset) order — which keeps an unset Apple-silicon host on the deterministic
// CPU path.
func TestProvidersFor(t *testing.T) {
	cases := map[string][]string{
		"cpu":      nil,
		"CPU":      nil,
		" cpu ":    nil,
		"cuda":     {"cuda"},
		"directml": {"directml"},
		"coreml":   {"coreml"},
		"CoreML":   {"coreml"},
		"":         {"cuda", "directml"},
		"auto":     {"cuda", "directml"},
		"nonsense": {"cuda", "directml"},
	}
	for env, want := range cases {
		if got := providersFor(env); !slices.Equal(got, want) {
			t.Errorf("providersFor(%q) = %v, want %v", env, got, want)
		}
	}
	if slices.Contains(providersFor(""), "coreml") {
		t.Error("coreml must not be in the auto (unset) order — it is opt-in")
	}
}

// TestFaceSessionOptionsForceCPU covers the EnvFaceEP opt-out: "cpu" (any case, trimmed) must
// short-circuit to nil options and the "cpu" provider WITHOUT touching the ONNX runtime — the
// deterministic path a host uses when it ships the CPU-only onnxruntime build. The CUDA/DirectML
// branches are exercised end-to-end by the integration test's provider selection.
func TestFaceSessionOptionsForceCPU(t *testing.T) {
	for _, v := range []string{"cpu", "CPU", " cpu "} {
		t.Setenv(EnvFaceEP, v)
		opts, provider := faceSessionOptions()
		if opts != nil {
			t.Errorf("%q: opts = non-nil, want nil (CPU uses default options)", v)
			opts.Destroy()
		}
		if provider != "cpu" {
			t.Errorf("%q: provider = %q, want cpu", v, provider)
		}
	}
}

// TestAppendProviderUnknown pins the guard for an unrecognised provider name (defensive; the
// switch in faceSessionOptions never passes one, but tryProviders must not silently succeed).
func TestAppendProviderUnknown(t *testing.T) {
	if err := appendProvider(nil, "nope"); err == nil {
		t.Fatal("appendProvider(unknown) = nil, want an error")
	}
}
