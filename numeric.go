package ontology

import (
	"math"
	"strconv"
	"strings"
)

const (
	minInt64   = math.MinInt64
	maxInt64   = math.MaxInt64
	exactLimit = 1 << 53 // integers beyond +/-2^53 may round in float64
)

func toInt64(raw any, mode Mode, out *Outcome) (int64, error) {
	switch v := raw.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return intFromFloat(v, mode, out)
	case float32:
		return intFromFloat(float64(v), mode, out)
	case string:
		return intFromString(v, mode, out)
	case bool:
		err := newErr(CatTypeMismatch, "cannot coerce bool to Int64")
		return 0, settle(mode, err, newRecord(CatTypeMismatch, raw, int64(0), ""), out)
	default:
		err := newErr(CatTypeMismatch, "cannot coerce %s to Int64", typeName(raw))
		return 0, settle(mode, err, newRecord(CatTypeMismatch, raw, int64(0), ""), out)
	}
}

// intFromFloat enforces the three float->int boundary behaviors in a
// fixed priority: overflow, dropped fraction, lost precision.
//   - -0.0 is an exact zero: succeeds with 0.
//   - integral floats within +/-2^53: exact.
//   - integral floats beyond +/-2^53: precision_lost; lenient truncates.
//   - floats with a fractional part: fraction_lost carrying the dropped
//     fraction; overflow takes precedence when out of int64 range.
func intFromFloat(f float64, mode Mode, out *Outcome) (int64, error) {
	if math.IsNaN(f) || f < float64(minInt64) || f >= float64(maxInt64)+1 {
		clamped := int64(maxInt64)
		if math.IsNaN(f) || f < 0 {
			clamped = minInt64
		}
		err := newErr(CatOverflow, "float %s is outside int64 range", formatFloat(f))
		return clamped, settle(mode, err,
			newRecord(CatOverflow, f, clamped, "clamped to int64 boundary"), out)
	}
	trunc := math.Trunc(f)
	if frac := f - trunc; frac != 0 {
		n := int64(trunc)
		err := newErr(CatFractionLost, "float %s loses fractional part %s",
			formatFloat(f), formatFloat(math.Abs(frac)))
		return n, settle(mode, err,
			newRecord(CatFractionLost, f, n, "dropped fraction "+formatFloat(math.Abs(frac))), out)
	}
	n := int64(trunc)
	if n >= exactLimit || n <= -exactLimit {
		err := newErr(CatPrecisionLost,
			"integral float %s is beyond +/-2^53 and is not exact as int64", formatFloat(f))
		return n, settle(mode, err,
			newRecord(CatPrecisionLost, f, n, "value beyond +/-2^53"), out)
	}
	return n, nil
}

func intFromString(s string, mode Mode, out *Outcome) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return n, nil
	}
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		var clamped int64 = maxInt64
		if strings.HasPrefix(strings.TrimSpace(s), "-") {
			clamped = minInt64
		}
		e := newErr(CatOverflow, "string %s overflows int64", valueString(s))
		return clamped, settle(mode, e,
			newRecord(CatOverflow, s, clamped, "raw text: "+s), out)
	}
	e := newErr(CatMalformedNumber, "string %s is not a valid int64", valueString(s))
	return 0, settle(mode, e, newRecord(CatMalformedNumber, s, int64(0), ""), out)
}

func toFloat64(raw any, mode Mode, out *Outcome) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int64:
		return floatFromInt(v, mode, out)
	case int:
		return floatFromInt(int64(v), mode, out)
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f, nil
		}
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			clamped := math.Inf(1)
			if strings.HasPrefix(strings.TrimSpace(v), "-") {
				clamped = math.Inf(-1)
			}
			e := newErr(CatOverflow, "string %s overflows float64", valueString(v))
			return clamped, settle(mode, e,
				newRecord(CatOverflow, v, clamped, "raw text: "+v), out)
		}
		e := newErr(CatMalformedNumber, "string %s is not a valid float64", valueString(v))
		return 0, settle(mode, e, newRecord(CatMalformedNumber, v, 0.0, ""), out)
	case bool:
		err := newErr(CatTypeMismatch, "cannot coerce bool to Float64")
		return 0, settle(mode, err, newRecord(CatTypeMismatch, raw, 0.0, ""), out)
	default:
		err := newErr(CatTypeMismatch, "cannot coerce %s to Float64", typeName(raw))
		return 0, settle(mode, err, newRecord(CatTypeMismatch, raw, 0.0, ""), out)
	}
}

// floatFromInt reports precision_lost for integers that float64 cannot
// represent exactly (|n| >= 2^53); lenient mode still returns the
// rounded float64.
func floatFromInt(n int64, mode Mode, out *Outcome) (float64, error) {
	f := float64(n)
	if n >= exactLimit || n <= -exactLimit {
		err := newErr(CatPrecisionLost,
			"integer %d cannot be represented exactly as float64 (got %s)", n, formatFloat(f))
		return f, settle(mode, err,
			newRecord(CatPrecisionLost, n, f, "rounded by float64"), out)
	}
	return f, nil
}

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
}
