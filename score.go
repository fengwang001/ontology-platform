package ontology

import "math"

// scoreOf extracts a numeric score from a row. It reports whether the value
// is numeric at all, and separately whether it is NaN (which is numeric but
// excluded from ranking). +Inf and -Inf are valid scores.
func scoreOf(row map[string]any, column string) (score float64, numeric bool, nan bool) {
	v, ok := row[column]
	if !ok {
		return 0, false, false
	}
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int8:
		f = float64(n)
	case int16:
		f = float64(n)
	case int32:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint:
		f = float64(n)
	case uint8:
		f = float64(n)
	case uint16:
		f = float64(n)
	case uint32:
		f = float64(n)
	case uint64:
		f = float64(n)
	default:
		return 0, false, false
	}
	if math.IsNaN(f) {
		return 0, true, true
	}
	return f, true, false
}

// tieOf extracts the tie-break string; missing or non-string values yield "".
func tieOf(row map[string]any, column string) string {
	if s, ok := row[column].(string); ok {
		return s
	}
	return ""
}
