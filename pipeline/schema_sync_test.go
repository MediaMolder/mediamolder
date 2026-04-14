// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

// schema_sync_test.go — Validates that schema/v1.0.json stays in sync with Go structs.

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestSchemaSyncWithGoStructs validates that every JSON field in Config/Input/Output/etc
// has a matching property in schema/v1.0.json, and vice versa.
func TestSchemaSyncWithGoStructs(t *testing.T) {
	schemaBytes, err := os.ReadFile("../schema/v1.0.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema missing $defs")
	}

	// Map Go struct types to their schema $defs names.
	checks := []struct {
		name     string
		goType   reflect.Type
		defName  string
		topLevel bool // if true, check in top-level properties instead of $defs
	}{
		{"Config (top-level)", reflect.TypeOf(Config{}), "", true},
		{"Input", reflect.TypeOf(Input{}), "input", false},
		{"StreamSelect", reflect.TypeOf(StreamSelect{}), "stream_select", false},
		{"NodeDef", reflect.TypeOf(NodeDef{}), "node", false},
		{"EdgeDef", reflect.TypeOf(EdgeDef{}), "edge", false},
		{"Output", reflect.TypeOf(Output{}), "output", false},
		{"Options", reflect.TypeOf(Options{}), "global_options", false},
		{"ErrorPolicy", reflect.TypeOf(ErrorPolicy{}), "error_policy", false},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			var schemaProps map[string]any
			if c.topLevel {
				props, ok := schema["properties"].(map[string]any)
				if !ok {
					t.Fatal("schema missing top-level properties")
				}
				schemaProps = props
			} else {
				def, ok := defs[c.defName].(map[string]any)
				if !ok {
					t.Fatalf("schema missing $defs/%s", c.defName)
				}
				props, ok := def["properties"].(map[string]any)
				if !ok {
					t.Fatalf("$defs/%s missing properties", c.defName)
				}
				schemaProps = props
			}

			goFields := jsonFieldNames(c.goType)
			schemaFields := mapKeys(schemaProps)

			sort.Strings(goFields)
			sort.Strings(schemaFields)

			// Check: every Go JSON field must exist in schema.
			for _, f := range goFields {
				if !contains(schemaFields, f) {
					t.Errorf("Go field %q (from %s) missing in schema", f, c.goType.Name())
				}
			}

			// Check: every schema property must exist in Go struct.
			for _, f := range schemaFields {
				if !contains(goFields, f) {
					t.Errorf("Schema property %q missing in Go struct %s", f, c.goType.Name())
				}
			}
		})
	}
}

// jsonFieldNames extracts the JSON tag names from a Go struct.
func jsonFieldNames(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
