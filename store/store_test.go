package store

import (
	"testing"

	"ontology/key"
)

func mkEntry(path, owner string, storedAt, ttl, size int64, vary ...string) *Entry {
	return &Entry{
		Path: path, ID: key.Build(owner, vary, nil), Size: size,
		StoredAt: storedAt, TTL: ttl, ExpireAt: storedAt + ttl, Public: owner == "",
	}
}

func TestSameIdentityReplacement(t *testing.T) {
	s := New(1000, 4)
	r1 := s.Put(mkEntry("/a", "", 0, 10, 10))
	r2 := s.Put(mkEntry("/a", "", 5, 10, 20))
	if r2.Replaced == nil || r2.Replaced.Size != 10 {
		t.Fatalf("same identity must replace old entry: %+v", r2)
	}
	if s.Bytes() != 20 || s.Len() != 1 {
		t.Fatalf("bytes=%d len=%d, want 20/1", s.Bytes(), s.Len())
	}
	_ = r1
}

func TestPerPathLimitEvictsEarliest(t *testing.T) {
	s := New(100000, 2)
	e1 := mkEntry("/a", "", 0, 100, 10, "h1")
	e2 := mkEntry("/a", "", 1, 100, 10, "h2")
	e3 := mkEntry("/a", "", 2, 100, 10, "h3")
	s.Put(e1)
	s.Put(e2)
	r := s.Put(e3)
	if len(r.Evicted) != 1 || r.Evicted[0] != e1 {
		t.Fatalf("want e1 evicted, got %+v", r.Evicted)
	}
	if len(s.PathEntries("/a")) != 2 {
		t.Fatalf("path variant count must stay <= 2")
	}

	// Ties on StoredAt: smaller Seq loses.
	s2 := New(100000, 1)
	a := mkEntry("/b", "u1", 3, 100, 10)
	b := mkEntry("/b", "u2", 3, 100, 10)
	s2.Put(a)
	r2 := s2.Put(b)
	if len(r2.Evicted) != 1 || r2.Evicted[0] != a {
		t.Fatalf("same StoredAt tie must evict smaller seq")
	}
}

func TestCapacityEvictionOrder(t *testing.T) {
	s := New(100, 4)
	// expiry 10
	s.Put(mkEntry("/a", "", 0, 10, 60, "l"))
	// expiry 19 forces eviction of the (10) entry.
	r := s.Put(mkEntry("/a", "", 9, 10, 50, "l", "x"))
	if len(r.Evicted) != 1 || r.Evicted[0].ExpireAt != 10 {
		t.Fatalf("must evict smallest expiry first: %+v", r.Evicted)
	}
	if s.Bytes() != 50 {
		t.Fatalf("bytes=%d want 50", s.Bytes())
	}

	// Expiry ties: the smallest Seq among the global minimum-expiry
	// entries is evicted. /p (seq1) and /q (seq2) share expiry 10;
	// inserting /r exceeds capacity and must remove /p.
	s2 := New(40, 4)
	r1 := s2.Put(mkEntry("/p", "", 0, 10, 20, "a"))
	r2 := s2.Put(mkEntry("/q", "", 0, 10, 20, "b"))
	if !r1.Stored || !r2.Stored {
		t.Fatalf("setup failed")
	}
	rr := s2.Put(mkEntry("/r", "", 5, 5, 20, "c"))
	if len(rr.Evicted) != 1 || rr.Evicted[0].Path != "/p" {
		t.Fatalf("expiry tie must evict smaller seq; got %+v", rr.Evicted)
	}
}

func TestInvalidatePath(t *testing.T) {
	s := New(1000, 4)
	s.Put(mkEntry("/a", "", 0, 10, 10, "x"))
	s.Put(mkEntry("/a", "u1", 0, 10, 10))
	s.Put(mkEntry("/b", "", 0, 10, 10))
	removed := s.InvalidatePath("/a")
	if len(removed) != 2 || s.Bytes() != 10 || len(s.PathEntries("/a")) != 0 {
		t.Fatalf("invalidate must remove all variants on path only: %d bytes=%d", len(removed), s.Bytes())
	}
}
