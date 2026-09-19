package coercion

import (
	"math"
	"strings"
	"testing"
)

func TestStringInt64OverflowCarriesOriginalText(t *testing.T) {
	_, err := New(Strict).Convert("9223372036854775808", Target{Kind: Int64Kind})
	category, ok := CategoryOf(err)
	if !ok || category != Overflow {
		t.Fatalf("category = %q, err = %v", category, err)
	}
	if !strings.Contains(err.Error(), "9223372036854775808") {
		t.Fatalf("overflow error lost original text: %v", err)
	}
}

func TestFloatToInt64FractionReportsDiscardedPart(t *testing.T) {
	result, err := New(Lenient).Convert(2.75, Target{Kind: Int64Kind})
	if err != nil || result.Value != int64(2) {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(result.Degradations) != 1 || result.Degradations[0].Category != PrecisionLoss {
		t.Fatalf("degradations = %#v", result.Degradations)
	}
	if !strings.Contains(result.Degradations[0].Detail, "0.75") {
		t.Fatalf("detail = %q", result.Degradations[0].Detail)
	}
}

func TestFloatToInt64SpecialValuesHaveSeparateBehavior(t *testing.T) {
	strict := New(Strict)
	lenient := New(Lenient)

	negativeZeroStrict, strictErr := strict.Convert(math.Copysign(0, -1), Target{Kind: Int64Kind})
	if strictErr == nil || negativeZeroStrict.Value != int64(0) {
		t.Fatalf("-0 strict = %#v, %v", negativeZeroStrict, strictErr)
	}
	negativeZeroLenient, err := lenient.Convert(math.Copysign(0, -1), Target{Kind: Int64Kind})
	if err != nil || negativeZeroLenient.Value != int64(0) {
		t.Fatalf("-0 lenient = %#v, %v", negativeZeroLenient, err)
	}
	if got := negativeZeroLenient.Categories(); len(got) != 1 || got[0] != PrecisionLoss {
		t.Fatalf("-0 categories = %v", got)
	}

	exact, err := strict.Convert(42.0, Target{Kind: Int64Kind})
	if err != nil || exact.Value != int64(42) || len(exact.Degradations) != 0 {
		t.Fatalf("exact integral float = %#v, %v", exact, err)
	}

	largeValue := math.Nextafter(9007199254740992.0, math.Inf(1))
	large, err := strict.Convert(largeValue, Target{Kind: Int64Kind})
	if err == nil || large.Value != int64(9007199254740994) {
		t.Fatalf("above 2^53 = %#v, %v", large, err)
	}
	category, _ := CategoryOf(err)
	if category != PrecisionLoss || !strings.Contains(err.Error(), "2^53") {
		t.Fatalf("large error = %v", err)
	}
}

func TestFloatToInt64ExactBoundary(t *testing.T) {
	result, err := New(Strict).Convert(9007199254740992.0, Target{Kind: Int64Kind})
	if err != nil || result.Value != int64(9007199254740992) || len(result.Degradations) != 0 {
		t.Fatalf("2^53 = %#v, %v", result, err)
	}
}

func TestInt64ToFloat64PrecisionLoss(t *testing.T) {
	strict := New(Strict)
	lenient := New(Lenient)
	exact, err := strict.Convert(int64(9007199254740991), Target{Kind: Float64Kind})
	if err != nil || exact.Value != 9007199254740991.0 {
		t.Fatalf("max safe int = %#v, %v", exact, err)
	}

	_, strictErr := strict.Convert(int64(9007199254740993), Target{Kind: Float64Kind})
	category, _ := CategoryOf(strictErr)
	if category != PrecisionLoss {
		t.Fatalf("strict category = %v, err = %v", category, strictErr)
	}
	loose, err := lenient.Convert(int64(9007199254740993), Target{Kind: Float64Kind})
	if err != nil || loose.Categories()[0] != PrecisionLoss {
		t.Fatalf("lenient = %#v, %v", loose, err)
	}
}

func TestFloatToInt64Overflow(t *testing.T) {
	_, err := New(Strict).Convert(1e300, Target{Kind: Int64Kind})
	category, _ := CategoryOf(err)
	if category != Overflow {
		t.Fatalf("category = %v, err = %v", category, err)
	}
	loose, err := New(Lenient).Convert(1e300, Target{Kind: Int64Kind})
	if err != nil || loose.Value != int64(math.MaxInt64) || loose.Categories()[0] != Overflow {
		t.Fatalf("lenient overflow = %#v, %v", loose, err)
	}
}
