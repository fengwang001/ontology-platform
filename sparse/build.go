package sparse

import (
	"io"

	"ontology/segment"
)

// FromSegment scans an open segment from its first frame and builds a sparse
// index with an anchor at the base event and every every-th event thereafter.
// Scanning stops at the first read error; the caller should repair first.
func FromSegment(r *segment.Reader, every uint64) (*Index, error) {
	x := &Index{Every: every}
	var i uint64
	for {
		ev, off, _, err := r.Next()
		if err != nil {
			if err == io.EOF {
				return x, nil
			}
			return nil, err
		}
		if ShouldAnchor(every, i) {
			x.Anchors = append(x.Anchors, Anchor{Seq: ev.Seq, Offset: off})
		}
		i++
	}
}
