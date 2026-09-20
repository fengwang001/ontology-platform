package ontology

import (
	"fmt"
	"math"
	"strings"
)

// KeyTypeError reports a join key column whose values have incomparable
// types, either between the two tables or within one table.
type KeyTypeError struct {
	Key       string // offending key column
	LeftType  string // type observed on the left (or first seen)
	RightType string // type observed on the right (or conflicting one)
}

func (e *KeyTypeError) Error() string {
	return fmt.Sprintf("join key %q: incomparable types %s and %s",
		e.Key, e.LeftType, e.RightType)
}

// keyOf extracts the join key values for a row. empty is true when any key
// column is absent, nil, or NaN; an empty key never matches anything.
func keyOf(row map[string]any, keys []string) (vals []any, empty bool) {
	vals = make([]any, len(keys))
	for i, k := range keys {
		v, ok := row[k]
		if !ok || v == nil {
			return nil, true
		}
		if f, isFloat := v.(float64); isFloat && math.IsNaN(f) {
			return nil, true
		}
		vals[i] = v
	}
	return vals, false
}

// encodeKey renders a non-empty key canonically for grouping. Numerics
// encode by exact value, so int64(3) and float64(3.0) group together and
// +0.0 groups with -0.0.
func encodeKey(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = encodeValue(v)
	}
	return strings.Join(parts, "\x00")
}

// checkKeyTypes validates that every key column holds comparable, supported
// types on both sides. It returns a *KeyTypeError naming the column and the
// two conflicting types otherwise. Rows with empty keys are skipped.
func checkKeyTypes(left, right []map[string]any, keys []string) error {
	for _, k := range keys {
		lf, lft, err := sideFamily(left, k)
		if err != nil {
			return err
		}
		rf, rft, err := sideFamily(right, k)
		if err != nil {
			return err
		}
		if lf != famInvalid && rf != famInvalid && lf != rf {
			return &KeyTypeError{Key: k, LeftType: lft, RightType: rft}
		}
	}
	return nil
}

// sideFamily returns the common family of column k's non-empty values in
// rows, plus a representative type name. famInvalid means no typed value
// was seen. Mixed families within the side yield a *KeyTypeError.
func sideFamily(rows []map[string]any, k string) (family, string, error) {
	seen := famInvalid
	seenType := ""
	for _, row := range rows {
		v, ok := row[k]
		if !ok || v == nil {
			continue
		}
		if f, isFloat := v.(float64); isFloat && math.IsNaN(f) {
			continue
		}
		fam, ok := valueFamily(v)
		if !ok {
			return famInvalid, "", &KeyTypeError{
				Key: k, LeftType: typeName(v), RightType: typeName(v),
			}
		}
		if seen == famInvalid {
			seen, seenType = fam, typeName(v)
			continue
		}
		if fam != seen {
			return famInvalid, "", &KeyTypeError{
				Key: k, LeftType: seenType, RightType: typeName(v),
			}
		}
	}
	return seen, seenType, nil
}
