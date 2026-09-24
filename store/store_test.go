package store

import (
	"errors"
	"maps"
	"testing"
)

type tstep struct {
	kind    byte
	key     string
	val, sp int
	wantErr error
	want    map[string]int
}

func snapData(s *Store) map[string]int { return maps.Clone(s.data) }
func mapEq(a, b map[string]int) bool   { return maps.Equal(a, b) }
func mustSet(s *Store, k string, v int) {
	if err := s.Set(k, v); err != nil {
		panic(err)
	}
}
func TestTwelveStepTrace(t *testing.T) {
	s, _ := New(100)
	var ids []int
	steps := []tstep{
		{'S', "a", 1, -1, nil, map[string]int{"a": 1}},
		{'P', "", 0, -1, nil, map[string]int{"a": 1}},
		{'S', "a", 2, -1, nil, map[string]int{"a": 2}},
		{'P', "", 0, -1, nil, map[string]int{"a": 2}},
		{'S', "b", 5, -1, nil, map[string]int{"a": 2, "b": 5}},
		{'X', "", 0, 1, nil, map[string]int{"a": 2, "b": 5}},
		{'S', "c", 8, -1, nil, map[string]int{"a": 2, "b": 5, "c": 8}},
		{'B', "", 0, 0, nil, map[string]int{"a": 1}},
		{'S', "b", 3, -1, nil, map[string]int{"a": 1, "b": 3}},
		{'P', "", 0, -1, nil, map[string]int{"a": 1, "b": 3}},
		{'S', "c", 6, -1, nil, map[string]int{"a": 1, "b": 3, "c": 6}},
		{'B', "", 0, 1, ErrSavepoint, map[string]int{"a": 1, "b": 3, "c": 6}},
	}
	for i, st := range steps {
		var err error
		switch st.kind {
		case 'S':
			err = s.Set(st.key, st.val)
		case 'P':
			ids = append(ids, s.Savepoint())
		case 'B':
			err = s.RollbackTo(ids[st.sp])
		case 'X':
			err = s.Release(ids[st.sp])
		}
		if !errors.Is(err, st.wantErr) || !mapEq(snapData(s), st.want) {
			t.Fatalf("step %d: err=%v data=%v want %v/%v", i+1, err, snapData(s), st.wantErr, st.want)
		}
	}
}
func TestReleaseRollbackIndependence(t *testing.T) {
	s, _ := New(100)
	mustSet(s, "a", 1)
	p0 := s.Savepoint()
	mustSet(s, "a", 2)
	p1 := s.Savepoint()
	mustSet(s, "b", 5)
	if err := s.Release(p1); err != nil || !mapEq(snapData(s), map[string]int{"a": 2, "b": 5}) {
		t.Fatal("release must not change store")
	}
	mustSet(s, "c", 8)
	if err := s.RollbackTo(p0); err != nil || !mapEq(snapData(s), map[string]int{"a": 1}) {
		t.Fatalf("rollback across released marker failed: %v", snapData(s))
	}
	q0, q1 := s.Savepoint(), s.Savepoint()
	if s.RollbackTo(q0) != nil || !errors.Is(s.RollbackTo(q0), ErrSavepoint) ||
		!errors.Is(s.RollbackTo(q1), ErrSavepoint) || !errors.Is(s.Release(q1), ErrSavepoint) {
		t.Fatal("consumed savepoints must stay invalid")
	}
}
func TestSentinelErrors(t *testing.T) {
	calls := []struct {
		name string
		call func() error
		want error
	}{
		{"limit zero", func() error { _, e := New(0); return e }, ErrInvalidLimit},
		{"limit negative", func() error { _, e := New(-3); return e }, ErrInvalidLimit},
		{"empty key", func() error { s, _ := New(1); return s.Set("", 1) }, ErrEmptyKey},
		{"log full", func() error { s, _ := New(1); mustSet(s, "a", 1); return s.Set("b", 2) }, ErrLogFull},
		{"rollback unknown", func() error { s, _ := New(1); return s.RollbackTo(7) }, ErrSavepoint},
		{"release unknown", func() error { s, _ := New(1); return s.Release(7) }, ErrSavepoint},
		{"rollback released", func() error { s, _ := New(1); id := s.Savepoint(); s.Release(id); return s.RollbackTo(id) }, ErrSavepoint},
		{"release rolled-back", func() error { s, _ := New(1); id := s.Savepoint(); s.RollbackTo(id); return s.Release(id) }, ErrSavepoint},
	}
	for _, c := range calls {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
}
func TestRejectedLeavesState(t *testing.T) {
	s, _ := New(1)
	mustSet(s, "a", 1)
	b0, n0, l0, x0 := snapData(s), s.nRec, len(s.log), s.nextID
	calls := []func() error{
		func() error { return s.Set("", 9) },
		func() error { return s.Set("b", 9) },
		func() error { return s.RollbackTo(40) },
		func() error { return s.Release(40) },
	}
	for i, f := range calls {
		if err := f(); err == nil || !mapEq(snapData(s), b0) ||
			s.nRec != n0 || len(s.log) != l0 || s.nextID != x0 {
			t.Fatalf("call %d changed state", i)
		}
	}
	id := s.Savepoint()
	if s.RollbackTo(id) != nil {
		t.Fatal("unusable after rejection")
	}
}

// TestSavepointLocateO1: scanCnt measures location only; with an index it
// must stay 1 regardless of m (undo popping itself is unavoidable).
func TestSavepointLocateO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s, _ := New(m + 1)
		id := s.Savepoint()
		for i := 0; i < m; i++ {
			mustSet(s, "k", i)
		}
		s.scanCnt = 0
		if err := s.RollbackTo(id); err != nil || s.scanCnt != 1 {
			t.Fatalf("m=%d: locate scans=%d err=%v, want 1", m, s.scanCnt, err)
		}
		if _, ok := s.Get("k"); ok || s.Set("z", 1) != nil {
			t.Fatalf("m=%d not undone / unusable", m)
		}
		if nid := s.Savepoint(); s.Release(nid) != nil {
			t.Fatalf("m=%d release after rollback failed", m)
		}
	}
}
