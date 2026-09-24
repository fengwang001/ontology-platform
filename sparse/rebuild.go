package sparse

import (
	"ontology/event"
	"ontology/segment"
)

// Rebuild reconstructs the sparse index purely from a segment image: anchors
// are every Nth frame, offsets are accumulated frame lengths relative to the
// frame region. Nothing beyond the segment bytes is consulted, which is why a
// rebuilt index is byte-identical to the original.
func Rebuild(seg []byte, every int) (*Index, error) {
	in, err := segment.InspectBytes(seg)
	if err != nil {
		return nil, err
	}
	x := &Index{Every: every}
	for i := range in.Events {
		x.Add(i, in.Events[i].Seq, uint64(in.Offsets[i]-segment.HeaderSize))
	}
	return x, nil
}

// AnchorEvents is a small helper used by tests/demo to enumerate (seq,off).
func AnchorEvents(evs []event.Event, offs []int64, every int) *Index {
	x := &Index{Every: every}
	for i := range evs {
		x.Add(i, evs[i].Seq, uint64(offs[i]))
	}
	return x
}
