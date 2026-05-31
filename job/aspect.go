package job

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// parseAspectRatio parses an aspect ratio expressed as either
// "<num>:<den>", "<num>/<den>", or a decimal float (e.g. "16:9",
// "16/9", "1.7777"). Returns the reduced numerator + denominator.
// Both num and den must be > 0. Mirrors the grammar libavutil's
// av_parse_ratio accepts (less the "0" -> AV_NOPTS_VALUE sentinel).
func parseAspectRatio(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("aspect ratio is empty")
	}
	var sep byte
	switch {
	case strings.ContainsRune(s, ':'):
		sep = ':'
	case strings.ContainsRune(s, '/'):
		sep = '/'
	}
	if sep != 0 {
		parts := strings.SplitN(s, string(sep), 2)
		if len(parts) != 2 {
			return 0, 0, fmt.Errorf("aspect ratio %q: want NUM%cDEN", s, sep)
		}
		num, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, 0, fmt.Errorf("aspect ratio %q: bad numerator: %w", s, err)
		}
		den, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, fmt.Errorf("aspect ratio %q: bad denominator: %w", s, err)
		}
		if num <= 0 || den <= 0 {
			return 0, 0, fmt.Errorf("aspect ratio %q: numerator and denominator must be > 0", s)
		}
		return num, den, nil
	}
	// Decimal float form.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("aspect ratio %q: %w", s, err)
	}
	if f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, 0, fmt.Errorf("aspect ratio %q: must be > 0", s)
	}
	// Convert to a rational by multiplying through 10000 then reducing.
	const scale = 10000
	num := int(math.Round(f * scale))
	den := scale
	g := gcd(num, den)
	return num / g, den / g, nil
}

func gcd(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// validateAspect rejects malformed Output.SAR / Output.DAR values
// and the SAR + DAR combination (mutually exclusive — they both
// compute the encoder's sample_aspect_ratio but from different
// inputs).
func validateAspect(out Output) error {
	if out.SAR != "" && out.DAR != "" {
		return fmt.Errorf("output %q: sar and dar are mutually exclusive", out.ID)
	}
	if out.SAR != "" {
		if _, _, err := parseAspectRatio(out.SAR); err != nil {
			return fmt.Errorf("output %q: invalid sar: %w", out.ID, err)
		}
	}
	if out.DAR != "" {
		if _, _, err := parseAspectRatio(out.DAR); err != nil {
			return fmt.Errorf("output %q: invalid dar: %w", out.ID, err)
		}
	}
	return nil
}

// resolveSAR computes the encoder's sample_aspect_ratio (num, den)
// from a SAR or DAR string and the encoder's resolved frame
// width/height. SAR is returned verbatim. DAR -> SAR is
// (DAR_num * height) / (DAR_den * width), reduced. Returns
// (0, 0, nil) when both inputs are empty (caller should leave the
// encoder default unchanged).
func resolveSAR(sar, dar string, width, height int) (int, int, error) {
	switch {
	case sar != "":
		return parseAspectRatio(sar)
	case dar != "":
		dn, dd, err := parseAspectRatio(dar)
		if err != nil {
			return 0, 0, err
		}
		if width <= 0 || height <= 0 {
			return 0, 0, fmt.Errorf("dar %q: encoder dimensions unknown (width=%d height=%d)", dar, width, height)
		}
		num := dn * height
		den := dd * width
		g := gcd(num, den)
		return num / g, den / g, nil
	}
	return 0, 0, nil
}
