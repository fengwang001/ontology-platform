package coercion

import (
	"math"
	"testing"
)

// Characterization tests: every assertion in this file pins the CURRENT,
// observed behavior of the coercion library at numeric boundaries, whether
// or not that behavior matches the documented intent. They exist to make
// the asymmetries and off-by-one thresholds described in FINDINGS.md
// executable and to guard against silent behavior changes.

// strictCategories converts input in Strict mode and returns the ordered
// error categories (empty when the conversion is clean).
func strictCategories(t *testing.T, input any, kind Kind) ([]ErrorCategory, any) {
	t.Helper()
	result, err := New(Strict).Convert(input, Target{Kind: kind})
	if err == nil {
		return []ErrorCategory{}, result.Value
	}
	var errs Errors
	if asErrors(err, &errs) {
		return errs.Categories(), result.Value
	}
	category, ok := CategoryOf(err)
	if !ok {
		t.Fatalf("unrecognized error for %#v: %v", input, err)
	}
	return []ErrorCategory{category}, result.Value
}

// lenientCategories converts input in Lenient mode and returns the ordered
// degradation categories (empty when the conversion is clean).
func lenientCategories(t *testing.T, input any, kind Kind) ([]ErrorCategory, any) {
	t.Helper()
	result, err := New(Lenient).Convert(input, Target{Kind: kind})
	if err != nil {
		t.Fatalf("lenient conversion of %#v failed: %v", input, err)
	}
	return result.Categories(), result.Value
}

func sameCategories(got, want []ErrorCategory) bool {
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

// TestSignedZeroAsymmetry pins that -0.0 reports PrecisionLoss while +0.0
// converts cleanly, even though both are the same integer zero.
func TestSignedZeroAsymmetry(t *testing.T) {
	cases := []struct {
		name  string
		input float64
		want  []ErrorCategory
	}{
		{"positive zero", 0.0, []ErrorCategory{}},
		{"negative zero", math.Copysign(0, -1), []ErrorCategory{PrecisionLoss}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strictGot, strictValue := strictCategories(t, tc.input, Int64Kind)
			if !sameCategories(strictGot, tc.want) || strictValue != int64(0) {
				t.Fatalf("strict: categories = %v, value = %#v, want categories %v, value 0",
					strictGot, strictValue, tc.want)
			}
			lenientGot, lenientValue := lenientCategories(t, tc.input, Int64Kind)
			if !sameCategories(lenientGot, tc.want) || lenientValue != int64(0) {
				t.Fatalf("lenient: categories = %v, value = %#v, want categories %v, value 0",
					lenientGot, lenientValue, tc.want)
			}
		})
	}
}

// TestExactIntegerRangeBoundary pins the 2^53 boundary of float64 -> int64,
// including the rounding trap: the literal 9007199254740993.0 is rounded to
// 2^53 by the compiler before the range check ever runs, so it converts
// silently to 9007199254740992.
func TestExactIntegerRangeBoundary(t *testing.T) {
	above := math.Nextafter(9007199254740992.0, math.Inf(1))
	below := math.Nextafter(9007199254740992.0, math.Inf(-1))
	cases := []struct {
		name      string
		input     float64
		want      []ErrorCategory
		wantValue int64
	}{
		{"below 2^53", below, []ErrorCategory{}, int64(9007199254740991)},
		{"exactly 2^53", 9007199254740992.0, []ErrorCategory{}, int64(9007199254740992)},
		{"exactly -2^53", -9007199254740992.0, []ErrorCategory{}, int64(-9007199254740992)},
		// The literal 2^53+1 is not representable; it rounds to 2^53 and
		// therefore escapes the PrecisionLoss check entirely.
		{"literal 2^53+1 rounds to 2^53", 9007199254740993.0, []ErrorCategory{}, int64(9007199254740992)},
		{"float64(int64 2^53+1) rounds to 2^53", float64(int64(9007199254740993)), []ErrorCategory{}, int64(9007199254740992)},
		{"first float above 2^53", above, []ErrorCategory{PrecisionLoss}, int64(above)},
		{"first float below -2^53", -above, []ErrorCategory{PrecisionLoss}, int64(-above)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, value := strictCategories(t, tc.input, Int64Kind)
			if !sameCategories(got, tc.want) || value != tc.wantValue {
				t.Fatalf("categories = %v, value = %#v, want categories %v, value %d",
					got, value, tc.want, tc.wantValue)
			}
		})
	}
}

// TestInt64ToFloat64ThresholdOffByOne pins that the int64 -> float64 check
// uses +/-(2^53-1) while float64 -> int64 uses 2^53: int64(2^53) is exactly
// representable as float64 yet is reported as PrecisionLoss.
func TestInt64ToFloat64ThresholdOffByOne(t *testing.T) {
	cases := []struct {
		name  string
		input int64
		want  []ErrorCategory
	}{
		{"2^53-1 exact, accepted", 9007199254740991, []ErrorCategory{}},
		{"-(2^53-1) exact, accepted", -9007199254740991, []ErrorCategory{}},
		// 2^53 IS exactly representable as float64, but the threshold is
		// 2^53-1, so this is a false-positive PrecisionLoss.
		{"2^53 exact, falsely reported", 9007199254740992, []ErrorCategory{PrecisionLoss}},
		{"-2^53 exact, falsely reported", -9007199254740992, []ErrorCategory{PrecisionLoss}},
		{"2^53+1 genuinely lossy", 9007199254740993, []ErrorCategory{PrecisionLoss}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, value := strictCategories(t, tc.input, Float64Kind)
			if !sameCategories(got, tc.want) {
				t.Fatalf("categories = %v, want %v", got, tc.want)
			}
			if value != float64(tc.input) {
				t.Fatalf("value = %#v, want %#v", value, float64(tc.input))
			}
		})
	}
}

// TestOverflowingIntegerTextReturnsZero pins that out-of-range integer text
// yields 0 (plus Overflow), discarding the clamped MaxInt64/MinInt64 that
// strconv.ParseInt computed. Contrast with float64 overflow, which returns
// the clamped bound.
func TestOverflowingIntegerTextReturnsZero(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"above max int64", "9223372036854775808"},
		{"below min int64", "-9223372036854775809"},
		{"far above max int64", "99999999999999999999999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strictGot, strictValue := strictCategories(t, tc.input, Int64Kind)
			if !sameCategories(strictGot, []ErrorCategory{Overflow}) {
				t.Fatalf("strict categories = %v, want [overflow]", strictGot)
			}
			if strictValue != int64(0) {
				t.Fatalf("strict value = %#v, want 0 (clamped value discarded)", strictValue)
			}
			lenientGot, lenientValue := lenientCategories(t, tc.input, Int64Kind)
			if !sameCategories(lenientGot, []ErrorCategory{Overflow}) {
				t.Fatalf("lenient categories = %v, want [overflow]", lenientGot)
			}
			if lenientValue != int64(0) {
				t.Fatalf("lenient value = %#v, want 0 (clamped value discarded)", lenientValue)
			}
		})
	}
}

// TestBoolNumericVersusTextAcceptance pins the asymmetry: the numeric side
// accepts exactly 0 and 1, while the text side additionally tolerates
// surrounding whitespace and arbitrary case.
func TestBoolNumericVersusTextAcceptance(t *testing.T) {
	cases := []struct {
		name      string
		input     any
		want      []ErrorCategory
		wantValue bool
	}{
		{"int64 zero", int64(0), []ErrorCategory{}, false},
		{"int64 one", int64(1), []ErrorCategory{}, true},
		{"int64 two", int64(2), []ErrorCategory{Invalid}, false},
		{"int64 negative one", int64(-1), []ErrorCategory{Invalid}, false},
		{"float zero", float64(0), []ErrorCategory{}, false},
		{"float one", float64(1), []ErrorCategory{}, true},
		{"float half", 0.5, []ErrorCategory{Invalid}, false},
		{"float nearly one", 1.0000000000000002, []ErrorCategory{Invalid}, false},
		{"text true upper padded", " TRUE ", []ErrorCategory{}, true},
		{"text false mixed case", "False", []ErrorCategory{}, false},
		{"text one padded", " 1 ", []ErrorCategory{}, true},
		{"text zero tabbed", "\t0\n", []ErrorCategory{}, false},
		{"text two", "2", []ErrorCategory{Invalid}, false},
		{"text yes", "yes", []ErrorCategory{Invalid}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, value := strictCategories(t, tc.input, BoolKind)
			if !sameCategories(got, tc.want) {
				t.Fatalf("categories = %v, want %v", got, tc.want)
			}
			if value != tc.wantValue {
				t.Fatalf("value = %#v, want %#v", value, tc.wantValue)
			}
		})
	}
}
