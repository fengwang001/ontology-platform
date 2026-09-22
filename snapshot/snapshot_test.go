package snapshot

import (
	"errors"
	"testing"

	"ontology/txid"
)

func TestVisibilityHalfOpenBoundary(t *testing.T) {
	s := newSnapshot(5, []txid.ID{3}, txid.Invalid)
	cases := []struct {
		cid txid.ID
		vis bool
	}{
		{1, true},
		{2, true},
		{3, false}, // active at snapshot
		{4, true},
		{5, false}, // equal to point: right-open
		{6, false}, // later
	}
	for _, tc := range cases {
		if got := s.Visible(tc.cid); got != tc.vis {
			t.Errorf("Visible(%d) = %v, want %v", tc.cid, got, tc.vis)
		}
	}
}

func TestRepeatableReads(t *testing.T) {
	reg := NewRegistry(txid.NewSourceAt(10), 0)
	s, err := reg.Open(nil)
	if err != nil || s.Point != 10 {
		t.Fatalf("open point=%d err=%v", s.Point, err)
	}
	for i := 0; i < 5; i++ {
		if s.Visible(9) != true || s.Visible(10) != false {
			t.Fatal("snapshot visibility changed across repeated checks")
		}
	}
	reg.Close(s)
	reg.Close(s) // idempotent
	if reg.Count() != 0 {
		t.Fatal("close not idempotent")
	}
}

func TestSnapshotLimit(t *testing.T) {
	reg := NewRegistry(txid.NewSource(), 2)
	var opens []*Snapshot
	for i := 0; i < 2; i++ {
		s, err := reg.Open(nil)
		if err != nil {
			t.Fatal(err)
		}
		opens = append(opens, s)
	}
	if _, err := reg.Open(nil); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("limit err = %v", err)
	}
	reg.Close(opens[0])
	if _, err := reg.Open(nil); err != nil {
		t.Fatalf("slot reuse failed: %v", err)
	}
}

func TestWatermarkTracksMinPoint(t *testing.T) {
	src := txid.NewSourceAt(100)
	reg := NewRegistry(src, 0)
	w, has, err := reg.ComputeWatermark()
	if err != nil || has || w != 100 {
		t.Fatalf("empty watermark: w=%d has=%v err=%v", w, has, err)
	}
	s1, _ := reg.Open(nil) // point 100
	if _, err := src.Allocate(); err != nil {
		t.Fatal(err)
	}
	s2, _ := reg.Open(nil) // point 101
	_ = s2
	w, has, _ = reg.ComputeWatermark()
	if !has || w != 100 {
		t.Fatalf("watermark = %d/%v", w, has)
	}
	reg.Close(s1)
	w, has, _ = reg.ComputeWatermark()
	if !has || w != 101 {
		t.Fatalf("after close watermark = %d", w)
	}
}

func TestWithSelfReadYourWrites(t *testing.T) {
	s := newSnapshot(5, nil, txid.Invalid)
	if s.IsSelf(7) {
		t.Fatal("pure snapshot claims a self txn")
	}
	s2 := s.WithSelf(7)
	if !s2.IsSelf(7) || s2.Point != 5 {
		t.Fatal("derived self view broken")
	}
	if s.IsSelf(7) {
		t.Fatal("WithSelf mutated original snapshot")
	}
}
