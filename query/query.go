// Package query parses and normalizes URL query strings. It keeps the
// three states "key" (no '='), "key=" (empty value) and "key=%20"
// (space value) strictly distinguishable, never deduplicates repeated
// keys, and supports both order-preserving and sorted canonical forms.
package query

import (
	"sort"
	"strings"

	"ontology/pct"
)

// Item is one key/value pair. HasValue distinguishes "a" (false) from
// "a=" (true, Value == "").
type Item struct {
	Key      string
	Value    string
	HasValue bool
}

// Parse splits raw on '&', then each part on the first '='. Keys and
// values are percent-normalized (unreserved escapes folded, reserved
// escapes such as %3D kept, so structure never changes). Empty parts
// (from "&&") are preserved as items with an empty key.
func Parse(raw string) ([]Item, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, "&")
	items := make([]Item, 0, len(parts))
	for _, p := range parts {
		key, val, has := p, "", false
		if i := strings.IndexByte(p, '='); i >= 0 {
			key, val, has = p[:i], p[i+1:], true
		}
		nk, err := pct.Normalize(key)
		if err != nil {
			return nil, err
		}
		nv, err := pct.Normalize(val)
		if err != nil {
			return nil, err
		}
		items = append(items, Item{Key: nk, Value: nv, HasValue: has})
	}
	return items, nil
}

// Sorted returns a copy of items ordered by key, then value, then
// HasValue. Repeated keys are kept, never merged.
func Sorted(items []Item) []Item {
	out := make([]Item, len(items))
	copy(out, items)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return !out[i].HasValue && out[j].HasValue
	})
	return out
}

// Render produces the canonical query string (without the leading '?').
// "a" renders as "a", "a=" as "a=", so the three states stay apart.
func Render(items []Item) string {
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
