package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// VerifyError reports a divergence between the maintained index and a
// full-table rescan, located by attribute and value. Extra lists IDs
// present in the index but not in the rescan; Missing lists IDs found by
// the rescan but absent from the index.
type VerifyError struct {
	Attr    string
	Value   string
	Extra   []string
	Missing []string
}

func (e *VerifyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "index divergence on attr %q value %s", e.Attr, e.Value)
	if len(e.Extra) > 0 {
		fmt.Fprintf(&b, "; extra IDs in index: %s", strings.Join(e.Extra, ","))
	}
	if len(e.Missing) > 0 {
		fmt.Fprintf(&b, "; missing IDs from index: %s", strings.Join(e.Missing, ","))
	}
	return b.String()
}

// Verify recomputes every equality index (including nil tracking) by
// scanning all entities and compares it against the maintained state.
// It returns nil when they agree exactly, otherwise a *VerifyError
// describing the first divergence found.
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	attrs := make([]string, 0, len(s.indexes))
	for attr := range s.indexes {
		attrs = append(attrs, attr)
	}
	sort.Strings(attrs)
	for _, attr := range attrs {
		if err := s.verifyAttr(attr, s.indexes[attr]); err != nil {
			return err
		}
	}
	return nil
}

// verifyAttr checks one attribute. Callers must hold the read lock.
func (s *Store) verifyAttr(attr string, ix *attrIndex) error {
	want := newAttrIndex()
	for id, props := range s.entities {
		if v, ok := props[attr]; ok {
			want.add(id, v)
		}
	}
	// Compare value buckets, including the representative values.
	keys := make([]string, 0, len(ix.buckets)+len(want.buckets))
	seen := make(map[string]bool)
	for key := range ix.buckets {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range want.buckets {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		got := ix.buckets[key]
		exp := want.buckets[key]
		extra := diffIDs(got, exp)
		missing := diffIDs(exp, got)
		if len(extra) > 0 || len(missing) > 0 {
			return &VerifyError{
				Attr:    attr,
				Value:   fmt.Sprintf("%v", firstValue(ix, want, key)),
				Extra:   extra,
				Missing: missing,
			}
		}
	}
	// Compare nil tracking.
	if extra, missing := diffIDs(ix.nilIDs, want.nilIDs), diffIDs(want.nilIDs, ix.nilIDs); len(extra) > 0 || len(missing) > 0 {
		return &VerifyError{Attr: attr, Value: "<nil>", Extra: extra, Missing: missing}
	}
	return nil
}

func firstValue(ix, want *attrIndex, key string) any {
	if v, ok := want.values[key]; ok {
		return v
	}
	return ix.values[key]
}

// diffIDs returns the sorted IDs present in a but absent from b.
func diffIDs(a, b map[string]struct{}) []string {
	var out []string
	for id := range a {
		if _, ok := b[id]; !ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
