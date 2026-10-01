// Package index implements a multi-column ordered index with access-path
// derivation: per-column equality, set and range conditions are compiled
// into a minimal set of key intervals so that a scan only examines entries
// inside those intervals.
package index

import "fmt"

// Kind is the physical type of an indexed column.
type Kind int

const (
	KindInt Kind = iota
	KindFloat
	KindString
)

func (k Kind) String() string {
	switch k {
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	}
	return "unknown"
}

// Column describes one indexed column. The index sorts entries
// lexicographically by columns in declaration order; NULL sorts first
// within every column.
type Column struct {
	Name string
	Kind Kind
}

// normalizeValue checks v against the column kind and returns the canonical
// in-memory representation (int64, float64 or string). nil (NULL) is
// allowed for stored entries but rejected for condition values by callers.
func normalizeValue(k Kind, v any) (any, error) {
	switch k {
	case KindInt:
		switch n := v.(type) {
		case int:
			return int64(n), nil
		case int64:
			return n, nil
		}
	case KindFloat:
		if f, ok := v.(float64); ok {
			return f, nil
		}
	case KindString:
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	return nil, fmt.Errorf("%w: value %v (%T) is not %s", ErrTypeMismatch, v, v, k)
}

// compareValues orders two normalized values of the same kind.
// NULL (nil) sorts before any non-NULL value.
func compareValues(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch av := a.(type) {
	case int64:
		bv := b.(int64)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	case float64:
		bv := b.(float64)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	case string:
		bv := b.(string)
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}

// cmpPrefix compares a full key against a bound prefix column by column.
// It returns 0 when the key agrees with the prefix on every prefix column.
func cmpPrefix(key, prefix []any) int {
	for i, pv := range prefix {
		if c := compareValues(key[i], pv); c != 0 {
			return c
		}
	}
	return 0
}
