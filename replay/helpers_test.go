package replay

import (
	"testing"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

func writeSegments(t *testing.T, dir string, total, perSeg int) {
	t.Helper()
	idx := 1
	for start := 0; start < total; start += perSeg {
		w, err := segment.Create(dir, idx, uint64(start))
		if err != nil {
			t.Fatal(err)
		}
		end := start + perSeg
		if end > total {
			end = total
		}
		for seq := start; seq < end; seq++ {
			if _, err := w.Append(event.Event{Seq: uint64(seq), Payload: []byte("payload")}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		idx++
	}
}

func buildIndexes(t *testing.T, dir string, interval uint64) {
	t.Helper()
	paths, err := segment.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		x, err := sparse.Build(p, interval)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.Save(p); err != nil {
			t.Fatal(err)
		}
	}
}
