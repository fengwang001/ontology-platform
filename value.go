package ontology

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
)

// family groups key types that are mutually comparable.
type family int

const (
	famInvalid family = iota
	famNumeric
	famString
	famBool
)

// valueFamily classifies a non-nil key value. ok is false for unsupported
// types; NaN is reported as a valid numeric here and handled as an empty
// key by the caller.
func valueFamily(v any) (family, bool) {
	switch v.(type) {
	case int64, float64:
		return famNumeric, true
	case string:
		return famString, true
	case bool:
		return famBool, true
	default:
		return famInvalid, false
	}
}

func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

// numRat returns the exact numeric value of an int64 or float64 as a
// rational. -0.0 normalizes to 0. ok is false for NaN and non-numerics.
func numRat(v any) (*big.Rat, bool) {
	switch n := v.(type) {
	case int64:
		return big.NewRat(n, 1), true
	case float64:
		if math.IsNaN(n) {
			return nil, false
		}
		r := new(big.Rat)
		if r.SetFloat64(n) == nil { // ±Inf
			return nil, false
		}
		return r, true
	default:
		return nil, false
	}
}

// compareValues orders two key values known to share a family.
// Numerics compare by exact value; strings bytewise; bools false<true.
func compareValues(a, b any) int {
	fa, _ := valueFamily(a)
	switch fa {
	case famNumeric:
		ra, oka := numRat(a)
		rb, okb := numRat(b)
		if !oka || !okb { // ±Inf: fall back to float64 order
			fa, fb := toFloat64(a), toFloat64(b)
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			}
			return 0
		}
		return ra.Cmp(rb)
	case famString:
		return strings.Compare(a.(string), b.(string))
	case famBool:
		ba, bb := a.(bool), b.(bool)
		switch {
		case ba == bb:
			return 0
		case !ba:
			return -1
		default:
			return 1
		}
	}
	return 0
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return 0
}

// encodeValue renders a key value canonically for grouping and identity.
func encodeValue(v any) string {
	switch fam, _ := valueFamily(v); fam {
	case famNumeric:
		r, ok := numRat(v)
		if !ok { // NaN or ±Inf (NaN may appear in non-key columns)
			f := v.(float64)
			switch {
			case math.IsNaN(f):
				return "n:NaN"
			case math.IsInf(f, 1):
				return "n:+Inf"
			default:
				return "n:-Inf"
			}
		}
		return "n:" + r.RatString()
	case famString:
		return "s:" + v.(string)
	case famBool:
		return fmt.Sprintf("b:%t", v.(bool))
	default:
		return fmt.Sprintf("?:%T:%v", v, v)
	}
}

// rowID derives a position-independent identity from row content: columns
// sorted by name, each rendered as "name=type:value". Identical rows have
// identical IDs and are interchangeable in output.
func rowID(row map[string]any) string {
	names := make([]string, 0, len(row))
	for name := range row {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, name := range names {
		fmt.Fprintf(&sb, "%s=%s;", name, encodeValue(row[name]))
	}
	return sb.String()
}

// deepCopy copies a row so results and inputs share no mutable state.
func deepCopy(row map[string]any) map[string]any {
	out := make(map[string]any, len(row))
	for k, v := range row {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopy(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	default:
		return v
	}
}

// IsMissing reports whether col is absent from row. For an unmatched left
// row in Left mode every right-side column is missing (not a zero value).
func IsMissing(row map[string]any, col string) bool {
	_, ok := row[col]
	return !ok
}
