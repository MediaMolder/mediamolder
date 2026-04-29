// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"math"
	"testing"
)

func TestEvalExpression(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		vars    map[string]float64
		want    float64
		wantErr bool
	}{
		{"constant", "42", nil, 42, false},
		{"identity", "t", map[string]float64{"t": 3.5}, 3.5, false},
		{"between_true", "between(t,1,8)", map[string]float64{"t": 4}, 1, false},
		{"between_false", "between(t,1,8)", map[string]float64{"t": 0.5}, 0, false},
		{"scrolling_x", "w-mod(40*t,w+tw)", map[string]float64{"w": 1280, "tw": 120, "t": 0}, 1280, false},
		{"unknown_var_at_eval", "foo+1", nil, 0, true},
		{"syntax_error", "between(t,1,", map[string]float64{"t": 0}, 0, true},
		{"div_by_zero_returns_inf", "1/0", nil, math.Inf(1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalExpression(tc.expr, tc.vars)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err: got %v, wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("value: got %v want %v", got, tc.want)
			}
		})
	}
}
