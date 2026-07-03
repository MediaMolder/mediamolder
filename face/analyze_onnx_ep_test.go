// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import "testing"

// TestFaceSessionOptionsForceCPU covers the EnvFaceEP opt-out: "cpu" (any case, trimmed) must
// short-circuit to nil options and the "cpu" provider WITHOUT touching the ONNX runtime — the
// deterministic path a host uses when it ships the CPU-only onnxruntime build. The DirectML
// branch is exercised end-to-end by the integration test's provider selection.
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
