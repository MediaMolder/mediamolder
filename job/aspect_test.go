package job

import "testing"

func TestParseAspectRatio(t *testing.T) {
	cases := []struct {
		in       string
		num, den int
	}{
		{"16:9", 16, 9},
		{"4:3", 4, 3},
		{"1:1", 1, 1},
		{"16/9", 16, 9},
		{"  4 : 3 ", 4, 3},
		{"1.5", 15000, 10000},
	}
	for _, c := range cases {
		n, d, err := parseAspectRatio(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		// reduce expected
		g := gcd(c.num, c.den)
		wantN, wantD := c.num/g, c.den/g
		gotG := gcd(n, d)
		gotN, gotD := n/gotG, d/gotG
		if gotN != wantN || gotD != wantD {
			t.Errorf("%q: got %d:%d, want %d:%d", c.in, n, d, wantN, wantD)
		}
	}
}

func TestParseAspectRatio_Errors(t *testing.T) {
	for _, in := range []string{"", "abc", "16:", ":9", "16:0", "0:9", "-1:1", "16:9:1"} {
		if _, _, err := parseAspectRatio(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestValidateAspect(t *testing.T) {
	if err := validateAspect(Output{ID: "o", SAR: "1:1"}); err != nil {
		t.Errorf("sar=1:1: %v", err)
	}
	if err := validateAspect(Output{ID: "o", DAR: "16:9"}); err != nil {
		t.Errorf("dar=16:9: %v", err)
	}
	if err := validateAspect(Output{ID: "o", SAR: "1:1", DAR: "16:9"}); err == nil {
		t.Error("sar+dar should be rejected as mutually exclusive")
	}
	if err := validateAspect(Output{ID: "o", SAR: "bad"}); err == nil {
		t.Error("sar=bad should be rejected")
	}
	if err := validateAspect(Output{ID: "o", DAR: "bad"}); err == nil {
		t.Error("dar=bad should be rejected")
	}
}

func TestResolveSAR(t *testing.T) {
	// SAR passes through.
	n, d, err := resolveSAR("16:11", "", 720, 576)
	if err != nil || n != 16 || d != 11 {
		t.Errorf("sar passthrough: got %d:%d err=%v", n, d, err)
	}
	// DAR -> SAR for DV-PAL 720x576 @ 4:3 should be 16:15.
	n, d, err = resolveSAR("", "4:3", 720, 576)
	if err != nil {
		t.Fatalf("dar 4:3 720x576: %v", err)
	}
	g := gcd(n, d)
	if n/g != 16 || d/g != 15 {
		t.Errorf("dar 4:3 720x576: got %d:%d (reduced %d:%d), want 16:15", n, d, n/g, d/g)
	}
	// DAR -> SAR for NTSC 720x480 @ 4:3 should be 8:9.
	n, d, err = resolveSAR("", "4:3", 720, 480)
	if err != nil {
		t.Fatalf("dar 4:3 720x480: %v", err)
	}
	g = gcd(n, d)
	if n/g != 8 || d/g != 9 {
		t.Errorf("dar 4:3 720x480: got %d:%d (reduced %d:%d), want 8:9", n, d, n/g, d/g)
	}
	// Square pixels via DAR.
	n, d, err = resolveSAR("", "16:9", 1920, 1080)
	if err != nil {
		t.Fatalf("dar 16:9 1920x1080: %v", err)
	}
	g = gcd(n, d)
	if n/g != 1 || d/g != 1 {
		t.Errorf("dar 16:9 1920x1080: got %d:%d, want 1:1", n/g, d/g)
	}
	// Empty inputs: no-op.
	n, d, err = resolveSAR("", "", 1920, 1080)
	if err != nil || n != 0 || d != 0 {
		t.Errorf("empty: got %d:%d err=%v", n, d, err)
	}
	// DAR with unknown dimensions: error.
	if _, _, err := resolveSAR("", "4:3", 0, 0); err == nil {
		t.Error("dar with zero dims should error")
	}
}
