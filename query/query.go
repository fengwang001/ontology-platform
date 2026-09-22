// Package query parses and normalizes URL query strings. It supports two
// comparison modes (order-preserving and sorted), keeps repeated keys, and
// preserves the distinction between "a", "a=" and "a=<value>".
package query

import (
	"sort"
	"strconv"
	"strings"

	"ontology/pct"
)

// Mode selects how repeated/ordered query items are compared.
type Mode int

const (
	// ModeOrdered preserves the original order of query items.
	ModeOrdered Mode = iota
	// ModeSorted sorts items by key, then by value, then by presence of "=".
	ModeSorted
)

// Item is one normalized query key/value pair.
type Item struct {
	Key   string
	Value string
	// HasEq records whether the item carried an "=" sign.
	HasEq bool
}

// Error reports a malformed escape with its offset in the raw query.
type Error struct {
	Offset int
	Kind   string
}

func (e *Error) Error() string {
	return "query: malformed escape (" + e.Kind + ") at byte " + strconv.Itoa(e.Offset)
}

// Parse normalizes a raw query string into items plus a canonical rendering.
func Parse(raw string, mode Mode, scan pct.ByteScanner) ([]Item, string, error) {
	if scan != nil {
		scan(len(raw))
	}
	var items []Item
	if raw != "" {
		pairs := strings.Split(raw, "&")
		off := 0
		for _, pair := range pairs {
			if pair == "" { // leading/trailing/consecutive "&" carries no item
				off += len(pair) + 1
				continue
			}
			item, perr := parseItem(pair, off)
			if perr != nil {
				return nil, "", perr
			}
			items = append(items, item)
			off += len(pair) + 1
		}
	}
	if mode == ModeSorted {
		sorted := append([]Item(nil), items...)
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].Key != sorted[j].Key {
				return sorted[i].Key < sorted[j].Key
			}
			if sorted[i].Value != sorted[j].Value {
				return sorted[i].Value < sorted[j].Value
			}
			if sorted[i].HasEq != sorted[j].HasEq {
				return sorted[j].HasEq // no-eq sorts before eq
			}
			return false
		})
		items = sorted
	}
	return items, render(items), nil
}

func parseItem(pair string, base int) (Item, error) {
	eq := strings.IndexByte(pair, '=')
	keyRaw, valRaw := pair, ""
	hasEq := false
	if eq >= 0 {
		keyRaw, valRaw, hasEq = pair[:eq], pair[eq+1:], true
	}
	key, err := pct.Normalize(keyRaw, querySafe, nil)
	if err != nil {
		return Item{}, mapError(err, base)
	}
	if !hasEq {
		return Item{Key: key, HasEq: false}, nil
	}
	val, err := pct.Normalize(valRaw, querySafe, nil)
	if err != nil {
		return Item{}, mapError(err, base+eq+1)
	}
	return Item{Key: key, Value: val, HasEq: true}, nil
}

func mapError(err error, base int) error {
	e, ok := err.(*pct.EscapeError)
	kind := "utf8"
	if ok {
		switch {
		case pct.IsTruncated(e):
			kind = "truncated"
		case pct.IsBadHex(e):
			kind = "badhex"
		}
		return &Error{Kind: kind, Offset: base + e.Offset}
	}
	return &Error{Kind: kind, Offset: base}
}

func render(items []Item) string {
	parts := make([]string, len(items))
	for i, it := range items {
		s := it.Key
		if it.HasEq {
			s += "=" + it.Value
		}
		parts[i] = s
	}
	return strings.Join(parts, "&")
}

// ParseCanonicalOrder normalizes without sorting or counting: used by callers
// that need the order-preserving canonical text for change detection.
func ParseCanonicalOrder(raw string) (string, error) {
	_, s, err := Parse(raw, ModeOrdered, nil)
	return s, err
}

// querySafe defines query literals: unreserved plus "/", "?" and ":".
func querySafe(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b >= 0x80:
		return true
	}
	return strings.ContainsRune("-._~!$'()*+,;=:@/?", rune(b))
}
