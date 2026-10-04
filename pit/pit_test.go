package pit

import (
	"errors"
	"testing"

	"ontology/segstore"
)

func dd(id string, v int64) segstore.Doc { return segstore.Doc{ID: id, SortVal: v} }

func ids(es []segstore.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func setupExample(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(2)
	if _, err := m.AddSegment(0, []segstore.Doc{dd("a", 5), dd("b", 7)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddSegment(0, []segstore.Doc{dd("c", 5), dd("d", 9)}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExamplePITVisibility(t *testing.T) {
	m := setupExample(t)
	p, err := m.Open(10, 20)
	if err != nil || p.ID != 1 || p.Exp != 30 {
		t.Fatalf("open: %+v err=%v", p, err)
	}
	if err := m.Delete(11, "b"); err != nil {
		t.Fatal(err)
	}
	if id, err := m.Merge(12, []int{1, 2}); err != nil || id != 3 {
		t.Fatalf("merge id=%d err=%v", id, err)
	}
	if rel := m.Released(); len(rel) != 0 {
		t.Fatalf("segs held by PIT must not release: %v", rel)
	}

	// PIT search: b is still visible; cross-segment same sortVal order.
	got, err := m.Search(15, 1, 2, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStrings(ids(got), []string{"a", "c"}) {
		t.Fatalf("page1=%v", ids(got))
	}
	if got[0].Key != (segstore.Key{SortVal: 5, Seg: 1, Idx: 0}) {
		t.Fatalf("a key=%+v", got[0].Key)
	}
	if got[1].Key != (segstore.Key{SortVal: 5, Seg: 2, Idx: 0}) {
		t.Fatalf("c key=%+v", got[1].Key)
	}
	next := got[len(got)-1].Key
	got2, err := m.Search(15, 1, 2, &next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStrings(ids(got2), []string{"b", "d"}) {
		t.Fatalf("page2=%v", ids(got2))
	}

	// Current view excludes b and uses merged seg 3.
	cur, err := m.Search(15, 0, 10, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStrings(ids(cur), []string{"a", "c", "d"}) {
		t.Fatalf("current=%v", ids(cur))
	}
	wantKeys := []segstore.Key{{SortVal: 5, Seg: 3, Idx: 0}, {SortVal: 5, Seg: 3, Idx: 1}, {SortVal: 9, Seg: 3, Idx: 2}}
	for i, e := range cur {
		if e.Key != wantKeys[i] {
			t.Fatalf("current key[%d]=%+v want %+v", i, e.Key, wantKeys[i])
		}
	}
}

func TestExpiryBoundaryAndRenewal(t *testing.T) {
	m := setupExample(t)
	p, _ := m.Open(10, 20) // exp=30
	_ = m.Delete(11, "b")
	if _, err := m.Merge(12, []int{1, 2}); err != nil {
		t.Fatal(err)
	}

	// now == exp-1 is still alive; ka extends only upward.
	if _, err := m.Search(29, 1, 1, nil, 10); err != nil {
		t.Fatalf("renew at 29: %v", err)
	}
	if m.pits[p.ID].exp != 39 {
		t.Fatalf("exp should extend to 39, got %d", m.pits[p.ID].exp)
	}
	// now=38 succeeds thanks to renewal.
	if _, err := m.Search(38, 1, 1, nil, 0); err != nil {
		t.Fatalf("alive at 38: %v", err)
	}
	// Shorter keep-alive never shrinks: max(39, 38+0)=39; ka=1 -> max(39,39)=39.
	if _, err := m.Search(38, 1, 1, nil, 1); err != nil {
		t.Fatal(err)
	}
	if m.pits[p.ID].exp != 39 {
		t.Fatalf("exp stays 39, got %d", m.pits[p.ID].exp)
	}

	// at exp exactly: expiry lands before PIT lookup; rejected op still lands.
	_, err := m.Search(39, 1, 1, nil, 0)
	if !errors.Is(err, ErrPITNotFound) {
		t.Fatalf("at exp got %v", err)
	}
	if rel := m.Released(); len(rel) != 2 || rel[0] != 1 || rel[1] != 2 {
		t.Fatalf("expired PIT releases [1 2], got %v", rel)
	}
	if m.now != 39 {
		t.Fatalf("clock advanced to 39, got %d", m.now)
	}
	// Rollback after the rejected-but-landed op.
	_, err = m.Search(38, 0, 1, nil, 0)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback got %v", err)
	}
}

func TestCloseReleasesImmediately(t *testing.T) {
	m := setupExample(t)
	_, _ = m.Open(10, 20)
	_, _ = m.Merge(11, []int{1, 2})
	if err := m.Close(20, 1); err != nil {
		t.Fatal(err)
	}
	if rel := m.Released(); len(rel) != 2 || rel[0] != 1 || rel[1] != 2 {
		t.Fatalf("close releases [1 2], got %v", rel)
	}
	if err := m.Close(21, 1); !errors.Is(err, ErrPITNotFound) {
		t.Fatalf("double close got %v", err)
	}
}

func TestSharedPITsLastCloseWins(t *testing.T) {
	m := setupExample(t)
	_, _ = m.Open(5, 100)
	_, _ = m.Open(6, 100)
	_, _ = m.Merge(7, []int{1, 2})
	if rel := m.Released(); len(rel) != 0 {
		t.Fatalf("shared segs retained: %v", rel)
	}
	if err := m.Close(8, 1); err != nil {
		t.Fatal(err)
	}
	if rel := m.Released(); len(rel) != 0 {
		t.Fatalf("still held by PIT 2: %v", rel)
	}
	if err := m.Close(9, 2); err != nil {
		t.Fatal(err)
	}
	if rel := m.Released(); len(rel) != 2 {
		t.Fatalf("last landing frees both: %v", rel)
	}
}

func TestPITLimitAndRejectionOrder(t *testing.T) {
	m := NewManager(1)
	_, _ = m.AddSegment(0, []segstore.Doc{dd("a", 1)})
	_, _ = m.Open(0, 10) // exp=10

	// Invalid argument beats clock rollback beats expiry beats state errors.
	if _, err := m.Open(5, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad ka got %v", err)
	}
	if err := m.Delete(5, "a"); err != nil {
		t.Fatal(err)
	}
	err := m.Close(4, 1)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback got %v", err)
	}

	// At t=10 the old PIT expires and lands first, so Open must succeed
	// instead of failing with limit; rejected ops would also have landed it.
	p2, err := m.Open(10, 10)
	if err != nil || p2.ID != 2 {
		t.Fatalf("expiry frees capacity: p=%+v err=%v", p2, err)
	}
	if rel := m.Released(); len(rel) != 0 {
		t.Fatalf("seg still held by new view membership: %v", rel)
	}
	_, err = m.Open(11, 10)
	if !errors.Is(err, ErrPITLimit) {
		t.Fatalf("limit got %v", err)
	}
}

func TestRejectedOpLandsExpiry(t *testing.T) {
	m := setupExample(t)
	_, _ = m.Open(0, 10) // exp=10
	_, _ = m.Merge(1, []int{1, 2})
	// A state-invalid delete at t=10 still lands the expired PIT first.
	if err := m.Delete(10, "ghost"); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("got %v", err)
	}
	if rel := m.Released(); len(rel) != 2 {
		t.Fatalf("rejected delete landed expiry: %v", rel)
	}
}

func TestOperationNumbersAndRollback(t *testing.T) {
	m := NewManager(2)
	_, _ = m.AddSegment(0, []segstore.Doc{dd("a", 1), dd("b", 2)})
	// Rejected delete must not consume an operation number.
	before := m.nextOp
	if err := m.Delete(0, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid delete got %v", err)
	}
	if m.nextOp != before {
		t.Fatalf("rejected op consumed number")
	}
	_ = m.Delete(0, "a")
	p, _ := m.Open(0, 5)
	// PIT opened after the delete must not see a.
	es, _ := m.Search(0, p.ID, 10, nil, 0)
	if !eqStrings(ids(es), []string{"b"}) {
		t.Fatalf("new PIT hides deleted a: %v", ids(es))
	}
	_, err := m.AddSegment(-1, []segstore.Doc{dd("x", 1)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad now got %v", err)
	}
	err = m.Delete(1_000_000_000_001, "b")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge now got %v", err)
	}
}

func TestTouchedBound(t *testing.T) {
	for _, docsPerSeg := range []int{10, 10000} {
		m := NewManager(2)
		for seg := 0; seg < 4; seg++ {
			docs := make([]segstore.Doc, docsPerSeg)
			for i := range docs {
				docs[i] = segstore.Doc{ID: key("id", seg*100000+i), SortVal: int64(i)}
			}
			if _, err := m.AddSegment(int64(seg), docs); err != nil {
				t.Fatal(err)
			}
		}
		m.Touched = 0
		p, err := m.Open(100, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Close(101, p.ID); err != nil {
			t.Fatal(err)
		}
		viewSegs := int64(4)
		heldSegs := int64(len(p.Segs))
		if m.Touched > viewSegs+heldSegs {
			t.Fatalf("docsPerSeg=%d touched=%d exceeds bound %d",
				docsPerSeg, m.Touched, viewSegs+heldSegs)
		}
		if m.Touched != viewSegs+heldSegs {
			t.Fatalf("docsPerSeg=%d touched=%d want %d",
				docsPerSeg, m.Touched, viewSegs+heldSegs)
		}
		t.Logf("docsPerSeg=%d totalDocs=%d touched=%d (bound=%d)",
			docsPerSeg, 4*docsPerSeg, m.Touched, viewSegs+heldSegs)
	}
}

func key(prefix string, i int) string {
	if i == 0 {
		return prefix + "_0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('a' + i%26)}, b...)
		i /= 26
	}
	return prefix + "_" + string(b)
}
