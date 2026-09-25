package coercion

import (
	"math"
	"testing"
)

// This file pins the CURRENT behavior of numeric boundary cases.
// Several entries document behaviors that contradict the implementation
// comments; see FINDINGS.md for the analysis. Do not "fix" these tests
// without changing the implementation deliberately.

func lenientOutcome(t *testing.T, value any, kind Kind) (any, []ErrorCategory) {
	t.Helper()
	result, err := New(Lenient).Convert(value, Target{Kind: kind})
	if err != nil {
		t.Fatalf("lenient convert %#v to %s failed: %v", value, kind, err)
	}
	return result.Value, result.Categories()
}

func equalCategories(got, want []ErrorCategory) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// -0.0 reports PrecisionLoss while +0.0 reports nothing, although both
// convert to the same int64(0). The only difference is the sign bit.
func TestFloatToInt64SignedZeroAsymmetry(t *testing.T) {
	cases := []struct {
		name           string
		input          float64
		wantValue      int64
		wantCategories []ErrorCategory
	}{
		{"positive zero", 0.0, 0, nil},
		{"negative zero", math.Copysign(0, -1), 0, []ErrorCategory{PrecisionLoss}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, categories := lenientOutcome(t, tc.input, Int64Kind)
			if value != tc.wantValue {
				t.Fatalf("value = %#v, want %#v", value, tc.wantValue)
			}
			if !equalCategories(categories, tc.wantCategories) {
				t.Fatalf("categories = %v, want %v", categories, tc.wantCategories)
			}
		})
	}
}

// The 2^53 exact-integer boundary for float64 -> int64. The literal
// 9007199254740993.0 (2^53+1) is rounded to 2^53 by the compiler before
// the check ever runs, so it escapes the PrecisionLoss report.
func TestFloatToInt64ExactIntegerBoundary(t *testing.T) {
	cases := []struct {
		name           string
		input          float64
		wantValue      int64
		wantCategories []ErrorCategory
	}{
		{"2^53 - 2", 9007199254740990.0, 9007199254740990, nil},
		{"2^53 exactly", 9007199254740992.0, 9007199254740992, nil},
		// 2^53+1 is not representable; the literal rounds down to 2^53
		// and therefore reports nothing.
		{"2^53+1 literal rounds to 2^53", 9007199254740993.0, 9007199254740992, nil},
		{"2^53 + 2", 9007199254740994.0, 9007199254740994, []ErrorCategory{PrecisionLoss}},
		{"nextafter above 2^53", math.Nextafter(9007199254740992.0, math.Inf(1)), 9007199254740994, []ErrorCategory{PrecisionLoss}},
		{"-2^53 exactly", -9007199254740992.0, -9007199254740992, nil},
		{"-2^53 - 2", -9007199254740994.0, -9007199254740994, []ErrorCategory{PrecisionLoss}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, categories := lenientOutcome(t, tc.input, Int64Kind)
			if value != tc.wantValue {
				t.Fatalf("value = %#v, want %#v", value, tc.wantValue)
			}
			if !equalCategories(categories, tc.wantCategories) {
				t.Fatalf("categories = %v, want %v", categories, tc.wantCategories)
			}
		})
	}
}

// int64 -> float64 uses a 2^53-1 threshold while float64 -> int64 uses
// 2^53, so int64(2^53) — exactly representable as float64 — is falsely
// reported as PrecisionLoss.
func TestInt64ToFloat64ThresholdOffByOne(t *testing.T) {
	cases := []struct {
		name           string
		input          int64
		wantValue      float64
		wantCategories []ErrorCategory
	}{
		{"2^53 - 1", 9007199254740991, 9007199254740991.0, nil},
		// Exactly representable, yet flagged: thresholds differ by one.
		{"2^53 exactly representable but flagged", 9007199254740992, 9007199254740992.0, []ErrorCategory{PrecisionLoss}},
		{"2^53 + 1 rounds down", 9007199254740993, 9007199254740992.0, []ErrorCategory{PrecisionLoss}},
		{"-2^53 exactly representable but flagged", -9007199254740992, -9007199254740992.0, []ErrorCategory{PrecisionLoss}},
		{"-2^53 - 1 rounds", -9007199254740993, -9007199254740992.0, []ErrorCategory{PrecisionLoss}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, categories := lenientOutcome(t, tc.input, Float64Kind)
			if value != tc.wantValue {
				t.Fatalf("value = %#v, want %#v", value, tc.wantValue)
			}
			if !equalCategories(categories, tc.wantCategories) {
				t.Fatalf("categories = %v, want %v", categories, tc.wantCategories)
			}
		})
	}
}

// Overflowing integer text returns 0, not the clamped MaxInt64/MinInt64
// that strconv.ParseInt hands back; the clamped value is discarded and
// the degradation carries no Converted value at all.
func TestInt64TextOverflowReturnsZeroNotClamp(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		wantValue      int64
		wantCategories []ErrorCategory
	}{
		{"max int64 text", "9223372036854775807", math.MaxInt64, nil},
		{"min int64 text", "-9223372036854775808", math.MinInt64, nil},
		{"max+1 returns zero", "9223372036854775808", 0, []ErrorCategory{Overflow}},
		{"min-1 returns zero", "-9223372036854775809", 0, []ErrorCategory{Overflow}},
		{"huge text returns zero", "99999999999999999999999999", 0, []ErrorCategory{Overflow}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := New(Lenient).Convert(tc.input, Target{Kind: Int64Kind})
			if err != nil {
				t.Fatalf("lenient convert failed: %v", err)
			}
			if result.Value != tc.wantValue {
				t.Fatalf("value = %#v, want %#v (clamped value discarded)", result.Value, tc.wantValue)
			}
			if !equalCategories(result.Categories(), tc.wantCategories) {
				t.Fatalf("categories = %v, want %v", result.Categories(), tc.wantCategories)
			}
			if len(result.Degradations) > 0 && result.Degradations[0].Converted != nil {
				t.Fatalf("degradation Converted = %#v, want nil (clamp never recorded)", result.Degradations[0].Converted)
			}
		})
	}
}

// toBool accepts only exactly 0/1 on the numeric side, but trims
// whitespace and ignores case on the text side.
func TestBoolNumericStrictTextLenientAsymmetry(t *testing.T) {
	accepted := []struct {
		input any
		want  bool
	}{
		{int64(0), false},
		{int64(1), true},
		{float64(0), false},
		{float64(1), true},
		{" TRUE ", true},
		{"1", true},
		{"\tFalse\n", false},
		{" 0 ", false},
	}
	for _, tc := range accepted {
		result, err := New(Strict).Convert(tc.input, Target{Kind: BoolKind})
		if err != nil || result.Value != tc.want {
			t.Fatalf("accepted input %#v: value = %#v, err = %v", tc.input, result.Value, err)
		}
	}

	rejected := []any{float64(0.5), float64(-1), int64(2), int64(-1), "2", "yes"}
	for _, input := range rejected {
		_, err := New(Strict).Convert(input, Target{Kind: BoolKind})
		category, ok := CategoryOf(err)
		if !ok || category != Invalid {
			t.Fatalf("rejected input %#v: category = %q, err = %v", input, category, err)
		}
	}
}
