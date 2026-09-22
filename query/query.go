// Package query parses and rebuilds query strings. Keys and values are
// escape-normalized via pct; item order is the caller's choice. Duplicate
// keys are never merged, and "a" (no '='), "a=" (empty value) and
// "a=%20" (space value) stay three distinguishable forms.
package query

import (
	"sort"
	"strings"

	"ontology/pct"
)

// Item is one query parameter. HasValue distinguishes "a" from "a=".
type Item struct {
	Key      string
	Value    string
	HasValue bool
}

// Stats reports what Parse changed.
type Stats struct {
	EscFired bool // escape normalization changed a key or value
	Dropped  bool // empty items (from "&&" etc.) were dropped
}

// Parse splits raw on '&'. Empty items are dropped; pct normalization is
// applied to each key and value. No partial result is returned on error.
func Parse(raw string) ([]Item, Stats, error) {
	var st Stats
	if raw == "" {
		return nil, st, nil
	}
	parts := strings.Split(raw, "&")
	items := make([]Item, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			st.Dropped = true
			continue
		}
		var it Item
		if i := strings.IndexByte(p, '='); i >= 0 {
			k, err := pct.Normalize(p[:i])
			if err != nil {
				return nil, st, err
			}
			v, err := pct.Normalize(p[i+1:])
			if err != nil {
				return nil, st, err
			}
			if k != p[:i] || v != p[i+1:] {
				st.EscFired = true
			}
			it = Item{Key: k, Value: v, HasValue: true}
		} else {
			k, err := pct.Normalize(p)
			if err != nil {
				return nil, st, err
			}
			if k != p {
				st.EscFired = true
			}
			it = Item{Key: k}
		}
		items = append(items, it)
	}
	return items, st, nil
}

// Sort orders items by key, then by value presence, then by value.
// Duplicates are kept, only their relative order is canonicalized.
func Sort(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.HasValue != b.HasValue {
			return !a.HasValue
		}
		return a.Value < b.Value
	})
}

// Build joins items back into a query string.
func Build(items []Item) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(it.Key)
		if it.HasValue {
			b.WriteByte('=')
			b.WriteString(it.Value)
		}
	}
	return b.String()
}
