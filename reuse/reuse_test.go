package reuse

import "testing"

func TestHeapOrdering(t *testing.T) {
	s := New()
	mk := func(peer, prefix, at int64) *Entry {
		return &Entry{Key: Key{Peer: peer, Prefix: prefix}, ReuseAt: at}
	}
	s.Add(mk(2, 1, 100))
	s.Add(mk(1, 9, 100))
	s.Add(mk(1, 1, 50))
	s.Add(mk(1, 5, 100))
	got := s.PopDue(100)
	want := []struct {
		peer, prefix, at int64
	}{
		{1, 1, 50}, {1, 5, 100}, {1, 9, 100}, {2, 1, 100},
	}
	if len(got) != len(want) {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Peer != w.peer || got[i].Prefix != w.prefix || got[i].ReuseAt != w.at {
			t.Fatalf("pos %d got (%d,%d,%d) want (%d,%d,%d)", i,
				got[i].Peer, got[i].Prefix, got[i].ReuseAt, w.peer, w.prefix, w.at)
		}
	}
	if s.Len() != 0 {
		t.Fatalf("heap not empty: %d", s.Len())
	}
}

func TestHeapRemove(t *testing.T) {
	s := New()
	a := &Entry{Key: Key{Peer: 1, Prefix: 1}, ReuseAt: 10}
	b := &Entry{Key: Key{Peer: 1, Prefix: 2}, ReuseAt: 20}
	s.Add(a)
	s.Add(b)
	s.Remove(a)
	got := s.PopDue(100)
	if len(got) != 1 || got[0] != b {
		t.Fatalf("unexpected %v", got)
	}
}
