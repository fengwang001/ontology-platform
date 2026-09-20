package dedup

import (
	"math"
	"strconv"
	"strings"
)

// rowClass classifies a row by how its dedup columns should be grouped.
type rowClass int

const (
	classNormal  rowClass = iota // keyed by column values
	classMissing                 // at least one dedup column absent
	classNil                     // no column absent, at least one nil
	classNaN                     // at least one column is NaN: always its own group
	classOther                   // at least one column is not comparable: own group
)

// classifyRow inspects the dedup columns of row. Missing beats nil, nil
// beats NaN, NaN beats uncomparable.
func classifyRow(columns []string, row map[string]any) rowClass {
	seenNil := false
	for _, col := range columns {
		v, ok := row[col]
		if !ok {
			return classMissing
		}
		if v == nil {
			seenNil = true
		}
	}
	if seenNil {
		return classNil
	}
	for _, col := range columns {
		if isNaNValue(row[col]) {
			return classNaN
		}
	}
	for _, col := range columns {
		if !isKeyable(row[col]) {
			return classOther
		}
	}
	return classNormal
}

func isNaNValue(v any) bool {
	switch t := v.(type) {
	case float64:
		return math.IsNaN(t)
	case float32:
		return math.IsNaN(float64(t))
	}
	return false
}

func isKeyable(v any) bool {
	switch v.(type) {
	case bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	return false
}

// hasEmptyString reports whether any dedup column holds an empty string.
// Only meaningful for classNormal rows.
func hasEmptyString(columns []string, row map[string]any) bool {
	for _, col := range columns {
		if s, ok := row[col].(string); ok && s == "" {
			return true
		}
	}
	return false
}

// buildKey returns the group key for a row. seq disambiguates classes
// (NaN, uncomparable) where every row forms its own group.
func buildKey(columns []string, row map[string]any, class rowClass, seq uint64) string {
	switch class {
	case classMissing:
		return "\x00missing"
	case classNil:
		return "\x00nil"
	case classNaN:
		return "\x00nan" + strconv.FormatUint(seq, 10)
	case classOther:
		return "\x00other" + strconv.FormatUint(seq, 10)
	}
	var sb strings.Builder
	for _, col := range columns {
		sb.WriteString(keyComponent(row[col]))
	}
	return sb.String()
}

func keyComponent(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "B1"
		}
		return "B0"
	case string:
		return "S" + strconv.Quote(t)
	case int:
		return intKey(int64(t))
	case int8:
		return intKey(int64(t))
	case int16:
		return intKey(int64(t))
	case int32:
		return intKey(int64(t))
	case int64:
		return intKey(t)
	case uint:
		return uintKey(uint64(t))
	case uint8:
		return uintKey(uint64(t))
	case uint16:
		return uintKey(uint64(t))
	case uint32:
		return uintKey(uint64(t))
	case uint64:
		return uintKey(t)
	case float32:
		return floatKey(float64(t))
	case float64:
		return floatKey(t)
	}
	return "?"
}

func intKey(i int64) string {
	return "I" + strconv.FormatInt(i, 10)
}

func uintKey(u uint64) string {
	if u <= math.MaxInt64 {
		return intKey(int64(u))
	}
	return "U" + strconv.FormatUint(u, 10)
}

// floatKey canonicalizes integral, in-range floats to the integer band so
// that float64(3.0) and int64(3) share a key; +0.0 and -0.0 both become 0.
func floatKey(f float64) string {
	if f == math.Trunc(f) && f >= -9223372036854775808.0 && f < 9223372036854775808.0 {
		return intKey(int64(f))
	}
	return "F" + strconv.FormatUint(math.Float64bits(f), 16)
}
