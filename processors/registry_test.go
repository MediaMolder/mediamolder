// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"sort"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	// Reset registry for test isolation. We can't easily reset the global,
	// but the built-in init() registrations are already there, so just test
	// that they work and that we can add another.
	const name = "test_only_proc"

	Register(name, func() Processor { return &Null{} })

	p, err := Get(name)
	if err != nil {
		t.Fatalf("Get(%q) error: %v", name, err)
	}
	if p == nil {
		t.Fatalf("Get(%q) returned nil", name)
	}
}

func TestGetUnknown(t *testing.T) {
	_, err := Get("does_not_exist_abcxyz")
	if err == nil {
		t.Fatal("expected error for unknown processor")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register("null", func() Processor { return &Null{} })
}

func TestNamesIncludesBuiltins(t *testing.T) {
	names := Names()
	sort.Strings(names)

	want := map[string]bool{"null": false, "frame_counter": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("built-in processor %q not found in Names()", n)
		}
	}
}
