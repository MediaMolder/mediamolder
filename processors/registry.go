// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"sync"
)

var (
	mu       sync.RWMutex
	registry = make(map[string]func() Processor)
)

// Register makes a processor factory available under the given name.
// It is typically called from init() in the package that defines the
// processor or from cmd/mediamolder main.
// Panics if name is already registered.
func Register(name string, factory func() Processor) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("processors: duplicate registration %q", name))
	}
	registry[name] = factory
}

// Get returns a fresh Processor instance for the given name.
// Returns an error if the name is not registered.
func Get(name string) (Processor, error) {
	mu.RLock()
	factory, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("processors: unknown processor %q", name)
	}
	return factory(), nil
}

// Names returns the sorted list of registered processor names.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	return out
}
