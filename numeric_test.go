package ontology

import (
	"math"
	"strings"
	"testing"
)

func TestStringOverflowInt64(t *testing.T) {
	c := NewConverter(Strict)
	_, err := c.CoerceValue("9223372036854775808", Target{Kind: Int64})
	ce := AsCoerceError(err)
	if ce == nil || ce.Category != CatOverflow {
		t.Fatalf("want overflow, got %v", err)
	}
	if !strings.Contains(ce.Message, "9223372036854775808") {
		t.Fatalf("overflow error must carry raw text, got %q", ce.Message)
	}

	_, err = c.CoerceValue("-9223372036854775809", Target{Kind: Int64})
	if ce = AsCoerceError(err); ce == nil || ce.Category != CatOverflow {
		t.Fatalf("want negative overflow, got %v", err)
	}
	// Exactly at the boundaries is valid.
	out, err := c.CoerceValue("9223372036854775807", Target{Kind: Int64})
	if err != nil || out.Int64 != math.MaxInt64 {
		t.Fatalf("max int64 boundary: %+v %v", out, err)
	}
	out, err = c.CoerceValue("-9223372036854775808", Target{Kind: Int64})
	if err != nil || out.Int64 != math.MinInt64 {
		t.Fatalf("min int64 boundary: %+v %v", out, err)
	}
}

func TestFloatFractionLost(t *testing.T) {
	c := NewConverter(Strict)
	_, err := c.CoerceValue(3.75, Target{Kind: Int64})
	ce := AsCoerceError(err)
	if ce == nil || ce.Category != CatFractionLost {
		t.Fatalf("want fraction_lost, got %v", err)
	}
	if !strings.Contains(ce.Message, "0.75") {
		t.Fatalf("error must report dropped fraction 0.75, got %q", ce.Message)
	}
}

// -0.0, an exactly-integral float, and an integral float beyond 2^53
// must each have their own assertable behavior.
func TestFloatIntBoundaries(t *testing.T) {
	c := NewConverter(Strict)

	// -0.0 converts to 0 cleanly: no fraction, no precision record.
	out, err := c.CoerceValue(math.Copysign(0, -1), Target{Kind: Int64})
	if err != nil || out.Int64 != 0 || len(out.Records) != 0 {
		t.Fatalf("-0.0 must be exact zero, got %+v err=%v", out, err)
	}

	// Exact integral float within 2^53: clean.
	out, err = c.CoerceValue(42.0, Target{Kind: Int64})
	if err != nil || out.Int64 != 42 || len(out.Records) != 0 {
		t.Fatalf("42.0 must be exact, got %+v err=%v", out, err)
	}

	// 2^53 itself: integral, but beyond exact int64-from-float zone.
	out, err = c.CoerceValue(float64(1<<53), Target{Kind: Int64})
	if ce := AsCoerceError(err); ce == nil || ce.Category != CatPrecisionLost {
		t.Fatalf("2^53 must be precision_lost, got %+v err=%v", out, err)
	}

	// Just below 2^53: exact.
	out, err = c.CoerceValue(float64(1<<53-1), Target{Kind: Int64})
	if err != nil || out.Int64 != (1<<53-1) {
		t.Fatalf("2^53-1 must be exact, got %+v err=%v", out, err)
	}
}

func TestIntToFloatPrecision(t *testing.T) {
	c := NewConverter(Strict)

	out, err := c.CoerceValue(int64(1<<53+1), Target{Kind: Float64})
	if ce := AsCoerceError(err); ce == nil || ce.Category != CatPrecisionLost {
		t.Fatalf("2^53+1 -> float64 must be precision_lost, got %+v err=%v", out, err)
	}

	out, err = c.CoerceValue(int64(1<<53-1), Target{Kind: Float64})
	if err != nil || out.Float64 != float64(1<<53-1) {
		t.Fatalf("2^53-1 -> float64 must be exact, got %+v err=%v", out, err)
	}
}

func TestFloatOverflowInt64(t *testing.T) {
	c := NewConverter(Strict)
	for _, f := range []float64{1e20, -1e20, math.Inf(1), math.Inf(-1), math.NaN()} {
		if _, err := c.CoerceValue(f, Target{Kind: Int64}); err == nil {
			t.Fatalf("%v must overflow int64", f)
		} else if ce := AsCoerceError(err); ce.Category != CatOverflow {
			t.Fatalf("%v: want overflow, got %s", f, ce.Category)
		}
	}
}
