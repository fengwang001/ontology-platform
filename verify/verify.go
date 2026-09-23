// Package verify checks segment files for corruption and validates
// inverted-index invariants.
package verify

import (
	"errors"
	"fmt"

	"ontology/segment"
)

// ErrInvariant reports a violated inverted-index invariant.
var ErrInvariant = errors.New("verify: invariant violated")

// File opens a segment file strictly (truncation classes propagate via
// errors.Is) and validates its invariants.
func File(path string) error {
	seg, err := segment.Open(path)
	if err != nil {
		return err
	}
	return Segment(seg)
}

// Segment validates: terms sorted and unique; doc IDs strictly ascending
// per list; positions strictly ascending and non-empty per entry.
func Segment(seg *segment.Segment) error {
	terms := seg.Terms()
	for i := 1; i < len(terms); i++ {
		if terms[i-1] >= terms[i] {
			return fmt.Errorf("%w: terms not sorted at %q", ErrInvariant, terms[i])
		}
	}
	for _, term := range terms {
		list, ok := seg.Postings(term)
		if !ok {
			return fmt.Errorf("%w: dangling term %q", ErrInvariant, term)
		}
		prevDoc := int64(-1)
		for _, e := range list {
			if int64(e.Doc) <= prevDoc {
				return fmt.Errorf("%w: docs not ascending in %q", ErrInvariant, term)
			}
			prevDoc = int64(e.Doc)
			if len(e.Pos) == 0 {
				return fmt.Errorf("%w: empty positions in %q doc %d", ErrInvariant, term, e.Doc)
			}
			prevPos := int64(-1)
			for _, p := range e.Pos {
				if int64(p) <= prevPos {
					return fmt.Errorf("%w: positions not ascending in %q doc %d", ErrInvariant, term, e.Doc)
				}
				prevPos = int64(p)
			}
		}
	}
	return nil
}
