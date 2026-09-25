package coercion

import (
	"math"
	"testing"
)

// boundaryCase drives every characterization case through both Strict and
// Lenient. These tests pin the *current* implementation behavior, including
// behavior that disagrees with the comments; they are not specifications of
// desired behavior.
type boundaryCase struct {
	name           string
	input          any
	target         Target
	wantValue      any
	wantCategory   ErrorCategory // "" means no strict error / no lenient degradation
	wantDegraded   bool          // Lenient records a non-skipped degradation
	wantSkipError  bool          // Lenient keeps the error and skips (Invalid)
	wantDetailPart string
}

func runBoundaryCases(t *testing.T, cases []boundaryCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strictResult, strictErr := New(Strict).Convert(tc.input, tc.target)
			lenientResult, lenientErr := New(Lenient).Convert(tc.input, tc.target)

			if got := strictResult.Value; got != tc.wantValue {
				t.Fatalf("strict value = %#v, want %#v", got, tc.wantValue)
			}
			if got := lenientResult.Value; got != tc.wantValue {
				t.Fatalf("lenient value = %#v, want %#v", got, tc.wantValue)
			}

			gotStrictCategory, strictOK := CategoryOf(strictErr)
			if tc.wantCategory == "" {
				if strictErr != nil {
					t.Fatalf("strict err = %v, want nil", strictErr)
				}
				if lenientErr != nil {
					t.Fatalf("lenient err = %v, want nil", lenientErr)
				}
				if len(lenientResult.Degradations) != 0 {
					t.Fatalf("lenient degradations = %#v, want none", lenientResult.Degradations)
				}
				return
			}
			if !strictOK || gotStrictCategory != tc.wantCategory {
				t.Fatalf("strict category = %q (ok=%v), want %q; err=%v",
					gotStrictCategory, strictOK, tc.wantCategory, strictErr)
			}
			if tc.wantSkipError {
				if lenientErr == nil {
					t.Fatalf("lenient err = nil, want retained %q error", tc.wantCategory)
				}
				gotLenientCategory, lenientOK := CategoryOf(lenientErr)
				if !lenientOK || gotLenientCategory != tc.wantCategory {
					t.Fatalf("lenient error category = %q, want %q",
						gotLenientCategory, tc.wantCategory)
				}
				if len(lenientResult.Degradations) != 0 {
					t.Fatalf("lenient degradations = %#v, want none for skipped value",
						lenientResult.Degradations)
				}
			} else {
				if lenientErr != nil {
					t.Fatalf("lenient err = %v, want nil", lenientErr)
				}
				if !tc.wantDegraded {
					t.Fatalf("case misconfigured: wantCategory %q requires wantDegraded", tc.wantCategory)
				}
				if len(lenientResult.Degradations) != 1 {
					t.Fatalf("lenient degradations = %#v, want exactly one", lenientResult.Degradations)
				}
				degradation := lenientResult.Degradations[0]
				if degradation.Category != tc.wantCategory {
					t.Fatalf("degradation category = %q, want %q", degradation.Category, tc.wantCategory)
				}
				if degradation.Skipped {
					t.Fatalf("degradation unexpectedly marked skipped: %#v", degradation)
				}
				if tc.wantDetailPart != "" && !stringsContains(degradation.Detail, tc.wantDetailPart) {
					t.Fatalf("degradation detail = %q, want substring %q",
						degradation.Detail, tc.wantDetailPart)
				}
			}
		})
	}
}

func stringsContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestCharacterizeFloatToInt64ZeroSign pins the -0.0/+0.0 asymmetry:
// negative zero reports PrecisionLoss ("negative zero sign discarded") while
// the numerically identical positive zero is a lossless conversion.
func TestCharacterizeFloatToInt64ZeroSign(t *testing.T) {
	runBoundaryCases(t, []boundaryCase{
		{
			name:           "negative zero loses signbit",
			input:          math.Copysign(0, -1),
			target:         Target{Kind: Int64Kind},
			wantValue:      int64(0),
			wantCategory:   PrecisionLoss,
			wantDegraded:   true,
			wantDetailPart: "negative zero sign discarded",
		},
		{
			name:         "positive zero is lossless",
			input:        0.0,
			target:       Target{Kind: Int64Kind},
			wantValue:    int64(0),
			wantCategory: "",
		},
	})
}

// TestCharacterizeFloatToInt64ExactIntegerBoundary pins the 2^53 threshold and
// the float64 rounding trap: the untyped literal 9007199254740993.0 rounds to
// 2^53 when stored as float64, so it escapes the PrecisionLoss check even
// though 2^53+1 as a mathematical integer is not exactly representable.
func TestCharacterizeFloatToInt64ExactIntegerBoundary(t *testing.T) {
	const (
		exactBoundary = 9007199254740992.0 // 2^53, exactly representable
		aboveBoundary = 9007199254740993.0 // rounds to 2^53 when stored as float64
	)
	roundedAbove := aboveBoundary // force conversion to float64 storage
	if roundedAbove != exactBoundary {
		t.Fatalf("test precondition failed: %v rounded to %v, want %v",
			aboveBoundary, roundedAbove, exactBoundary)
	}
	firstDistinct := math.Nextafter(exactBoundary, math.Inf(1)) // 2^53 + 2

	for _, tc := range []struct {
		name     string
		input    float64
		expected int64
		lost     bool
	}{
		{"below 2^53", exactBoundary - 1, int64(exactBoundary - 1), false},
		{"exactly 2^53", exactBoundary, int64(exactBoundary), false},
		{"literal 2^53+1 rounded to 2^53", roundedAbove, int64(exactBoundary), false},
		{"first distinct float above 2^53", firstDistinct, int64(firstDistinct), true},
		{"negative exactly -2^53", -exactBoundary, -int64(exactBoundary), false},
		{"negative literal rounded to -2^53", -aboveBoundary, -int64(exactBoundary), false},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			bc := boundaryCase{
				name:      tc.name,
				input:     tc.input,
				target:    Target{Kind: Int64Kind},
				wantValue: tc.expected,
			}
			if tc.lost {
				bc.wantCategory = PrecisionLoss
				bc.wantDegraded = true
				bc.wantDetailPart = "magnitude exceeds exact float64 integer range 2^53"
			}
			runBoundaryCases(t, []boundaryCase{bc})
		})
	}
}

// TestCharacterizeInt64ToFloat64Threshold pins the threshold mismatch:
// toFloat64 uses 2^53-1, so int64(2^53) — exactly representable as float64 —
// is falsely reported as PrecisionLoss.
func TestCharacterizeInt64ToFloat64Threshold(t *testing.T) {
	const (
		maxSafeInteger    = int64(9007199254740991) // 2^53 - 1
		exactBoundary     = int64(9007199254740992) // 2^53, exactly representable
		firstTrulyLossy   = int64(9007199254740994) // 2^53 + 2
		negMaxSafeInteger = -maxSafeInteger
		negExactBoundary  = -exactBoundary
	)
	runBoundaryCases(t, []boundaryCase{
		{
			name:      "2^53-1 converts losslessly",
			input:     maxSafeInteger,
			target:    Target{Kind: Float64Kind},
			wantValue: float64(maxSafeInteger),
		},
		{
			name:           "exactly 2^53 is falsely flagged",
			input:          exactBoundary,
			target:         Target{Kind: Float64Kind},
			wantValue:      float64(exactBoundary),
			wantCategory:   PrecisionLoss,
			wantDegraded:   true,
			wantDetailPart: "not exactly representable as float64",
		},
		{
			name:           "2^53+2 genuinely loses precision",
			input:          firstTrulyLossy,
			target:         Target{Kind: Float64Kind},
			wantValue:      float64(firstTrulyLossy),
			wantCategory:   PrecisionLoss,
			wantDegraded:   true,
			wantDetailPart: "not exactly representable as float64",
		},
		{
			name:      "negative 2^53-1 converts losslessly",
			input:     negMaxSafeInteger,
			target:    Target{Kind: Float64Kind},
			wantValue: float64(negMaxSafeInteger),
		},
		{
			name:           "negative exactly 2^53 is falsely flagged",
			input:          negExactBoundary,
			target:         Target{Kind: Float64Kind},
			wantValue:      float64(negExactBoundary),
			wantCategory:   PrecisionLoss,
			wantDegraded:   true,
			wantDetailPart: "not exactly representable as float64",
		},
	})
}

// TestCharacterizeInt64OverflowTextReturnsZero pins that overflowing decimal
// text yields 0 (with Overflow) instead of the MaxInt64/MinInt64 value that
// strconv.ParseInt clamps to. The clamp value is also absent from Converted.
func TestCharacterizeInt64OverflowTextReturnsZero(t *testing.T) {
	runBoundaryCases(t, []boundaryCase{
		{
			name:           "above MaxInt64 returns zero not clamped max",
			input:          "9223372036854775808",
			target:         Target{Kind: Int64Kind},
			wantValue:      int64(0),
			wantCategory:   Overflow,
			wantDegraded:   true,
			wantDetailPart: "overflows int64",
		},
		{
			name:           "below MinInt64 returns zero not clamped min",
			input:          "-9223372036854775809",
			target:         Target{Kind: Int64Kind},
			wantValue:      int64(0),
			wantCategory:   Overflow,
			wantDegraded:   true,
			wantDetailPart: "overflows int64",
		},
	})

	for _, text := range []string{"9223372036854775808", "-9223372036854775809"} {
		_, err := New(Strict).Convert(text, Target{Kind: Int64Kind})
		var errs Errors
		if !asErrors(err, &errs) || len(errs) != 1 {
			t.Fatalf("%q: err = %v, want one Errors entry", text, err)
		}
		if errs[0].Converted != nil {
			t.Fatalf("%q: Converted = %#v, want nil (clamped value discarded)", text, errs[0].Converted)
		}
	}
}

// TestCharacterizeBoolNumericVsTextAcceptance pins the asymmetric acceptance:
// numeric inputs must be exactly 0 or 1 (0.5 and 2 are Invalid, even in
// Lenient), while text inputs tolerate surrounding whitespace and any case.
func TestCharacterizeBoolNumericVsTextAcceptance(t *testing.T) {
	runBoundaryCases(t, []boundaryCase{
		{name: "numeric 0.5 rejected", input: float64(0.5), target: Target{Kind: BoolKind},
			wantValue: false, wantCategory: Invalid, wantSkipError: true},
		{name: "numeric int64 2 rejected", input: int64(2), target: Target{Kind: BoolKind},
			wantValue: false, wantCategory: Invalid, wantSkipError: true},
		{name: "numeric float64 2 rejected", input: float64(2), target: Target{Kind: BoolKind},
			wantValue: false, wantCategory: Invalid, wantSkipError: true},
		{name: "numeric zero accepted", input: float64(0), target: Target{Kind: BoolKind},
			wantValue: false},
		{name: "numeric one accepted", input: int64(1), target: Target{Kind: BoolKind},
			wantValue: true},
		{name: "padded uppercase TRUE text accepted", input: " TRUE ", target: Target{Kind: BoolKind},
			wantValue: true},
		{name: "plain 1 text accepted", input: "1", target: Target{Kind: BoolKind},
			wantValue: true},
		{name: "padded lowercase false text accepted", input: "\tfalse\n", target: Target{Kind: BoolKind},
			wantValue: false},
	})
}
