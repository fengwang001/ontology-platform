package resume

import (
	"testing"
	"time"

	"ontology/chunker"
)

func TestStoreSaveLoad(t *testing.T) {
	var s Store
	if _, ok := s.Load(); ok {
		t.Fatal("empty store reported a checkpoint")
	}
	cp := Checkpoint{
		Accepted: 100, Confirmed: 40, Chunks: 3, Closed: true,
		Chunker:    chunker.State{Pending: 2, Open: time.Unix(1, 0)},
		Backlog:    [][]byte{[]byte("abc"), []byte("def")},
		RawPending: []byte("xy"),
		Sizes:      []int{10, 20},
	}
	s.Save(cp)
	got, ok := s.Load()
	if !ok {
		t.Fatal("checkpoint not found")
	}
	if got.Accepted != 100 || got.Confirmed != 40 || got.Chunks != 3 || !got.Closed {
		t.Fatalf("scalars = %+v", got)
	}
	if got.Chunker.Pending != 2 || !got.Chunker.Open.Equal(time.Unix(1, 0)) {
		t.Fatalf("chunker state = %+v", got.Chunker)
	}
	if string(got.Backlog[0]) != "abc" || string(got.RawPending) != "xy" || got.Sizes[1] != 20 {
		t.Fatalf("slices = %+v", got)
	}
	if p := got.Pending(); p != 8 {
		t.Fatalf("Pending = %d, want 8", p)
	}
}
