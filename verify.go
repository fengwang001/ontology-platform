package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// VerifyError reports a divergence between an equality index and a
// full table scan, located by attribute and value.
type VerifyError struct {
	Attr string
	// ValueKey is the canonical key of the diverging value.
	ValueKey string
	// Extra lists IDs present in the index but not in the scan.
	Extra []string
	// Missing lists IDs present in the scan but not in the index.
	Missing []string
}

func (e *VerifyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "index divergence on attr %q value %q", e.Attr, e.ValueKey)
	if len(e.Extra) > 0 {
		fmt.Fprintf(&b, ": extra ids in index [%s]", strings.Join(e.Extra, ","))
	}
	if len(e.Missing) > 0 {
		fmt.Fprintf(&b, ": missing ids in index [%s]", strings.Join(e.Missing, ","))
	}
	return b.String()
}

// Verify recomputes every equality index from scratch via a full
// table scan and compares it element by element with the maintained
// index. It returns nil on exact agreement, otherwise a *VerifyError
// locating the first divergence. It must pass after every mutation.
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, attr := range s.attrs {
		want := make(map[string]map[string]struct{})
		for id, ent := range s.ents {
			v, ok := ent[attr]
			if !ok || v == nil {
				continue
			}
			key, _ := keyOf(v)
			setAdd(want, key, id)
		}
		if err := diffIndex(attr, want, s.index[attr]); err != nil {
			return err
		}
	}
	return nil
}

// diffIndex compares the scanned index (want) with the maintained
// index (got) for one attribute.
func diffIndex(attr string, want, got map[string]map[string]struct{}) error {
	for key, wantIDs := range want {
		gotIDs := got[key]
		extra := diffIDs(gotIDs, wantIDs)
		missing := diffIDs(wantIDs, gotIDs)
		if len(extra) > 0 || len(missing) > 0 {
			return &VerifyError{Attr: attr, ValueKey: key, Extra: extra, Missing: missing}
		}
	}
	for key, gotIDs := range got {
		if _, ok := want[key]; !ok {
			return &VerifyError{Attr: attr, ValueKey: key, Extra: diffIDs(gotIDs, nil)}
		}
	}
	return nil
}

// diffIDs returns the sorted IDs present in a but not in b.
func diffIDs(a, b map[string]struct{}) []string {
	out := make([]string, 0)
	for id := range a {
		if _, ok := b[id]; !ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
