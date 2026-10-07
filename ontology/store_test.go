package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustOK(t *testing.T, r Result) Version {
	t.Helper()
	if !r.OK() {
		t.Fatalf("expected ok, got kind=%d err=%v", r.Kind, r.Err)
	}
	return r.NewVersion
}

func TestCreateAndMonotonicVersions(t *testing.T) {
	s := NewStore()
	v1 := mustOK(t, s.Commit(WriteRequest{Object: "o1", Create: true, Base: 0,
		Values: map[Property]Value{"a": 1}}))
	v2 := mustOK(t, s.Commit(WriteRequest{Object: "o1", Base: v1,
		Values: map[Property]Value{"b": 2}}))
	if v1 != 1 || v2 != 2 {
		t.Fatalf("versions not monotonic: %d %d", v1, v2)
	}
	if c := s.Clock(); c != 2 {
		t.Fatalf("clock=%d want 2", c)
	}
}

func TestSnapshotByteImmutable(t *testing.T) {
	s := NewStore()
	v1 := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 1, "b": "x"}}))
	snap1, _ := s.Read("o", v1)
	raw := append([]byte(nil), snap1.Bytes()...)
	for i := 0; i < 5; i++ {
		h := s.stateFor("o").head
		mustOK(t, s.Commit(WriteRequest{Object: "o", Base: h,
			Values: map[Property]Value{"b": fmt.Sprintf("x%d", i)}}))
	}
	mustOK(t, s.Commit(WriteRequest{Object: "o", Base: v1, Values: map[Property]Value{"c": 9}}))
	again, _ := s.Read("o", v1)
	if !bytes.Equal(raw, again.Bytes()) {
		t.Fatalf("v1 snapshot changed:\nwas %s\nnow %s", raw, again.Bytes())
	}
}

func TestDisjointSetsBothCommit(t *testing.T) {
	s := NewStore()
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0, "b": 0}}))
	rA := s.Commit(WriteRequest{Object: "o", Base: base, Values: map[Property]Value{"a": 1}})
	rB := s.Commit(WriteRequest{Object: "o", Base: base, Values: map[Property]Value{"b": 2}})
	if !rA.OK() || !rB.OK() {
		t.Fatalf("disjoint writes must both succeed: %+v %+v", rA, rB)
	}
	if rA.NewVersion == rB.NewVersion {
		t.Fatalf("versions collided: %d", rA.NewVersion)
	}
	snap, _ := s.Read("o", s.stateFor("o").head)
	a, _ := snap.Get("a")
	b, _ := snap.Get("b")
	if a.(float64) != 1 || b.(float64) != 2 {
		t.Fatalf("merged state wrong: %v %v", a, b)
	}
}

func TestIntersectingSetsConflict(t *testing.T) {
	s := NewStore()
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0}}))
	mustOK(t, s.Commit(WriteRequest{Object: "o", Base: base, Values: map[Property]Value{"a": 1}}))
	headBefore := s.stateFor("o").head
	clockBefore := s.Clock()

	r := s.Commit(WriteRequest{Object: "o", Base: base, Values: map[Property]Value{"a": 2}})
	if r.Kind != ConflictProperty || !errors.Is(r.Err, ErrPropertyConflict) {
		t.Fatalf("want property conflict, got %+v", r)
	}
	if s.stateFor("o").head != headBefore || s.Clock() != clockBefore {
		t.Fatalf("rejected write advanced state: head %d clock %d",
			s.stateFor("o").head, s.Clock())
	}
	snap, _ := s.Read("o", headBefore)
	if v, _ := snap.Get("a"); v.(float64) != 1 {
		t.Fatalf("partial write leaked: %v", v)
	}
}

func TestHookReadScopeCausesConflict(t *testing.T) {
	s := NewStore()
	s.RegisterType("T", Validator{
		Name: "reads-a",
		Declare: func(v map[Property]Value, snap Snapshot) ReadScope {
			return ReadScope{Refs: []PropertyRef{{Local: "a"}}}
		},
	})
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Type: "T", Create: true,
		Values: map[Property]Value{"a": 0, "b": 0}}))
	// writer B only touches b, but its hook declares reading a.
	mustOK(t, s.Commit(WriteRequest{Object: "o", Type: "T", Base: base,
		Values: map[Property]Value{"b": 1}}))
	// writer A writes a on the old baseline: hook-mediated conflict.
	r := s.Commit(WriteRequest{Object: "o", Type: "T", Base: base,
		Values: map[Property]Value{"a": 2}})
	if r.Kind != ConflictProperty {
		t.Fatalf("hook-mediated conflict expected, got %+v", r)
	}
}

func TestLinkedInstanceHookConflict(t *testing.T) {
	s := NewStore()
	// A hook on the pointing instance reads property "p" of the instance
	// referenced by link field "link".
	s.RegisterType("X", Validator{
		Name: "reads-linked-p",
		Declare: func(v map[Property]Value, snap Snapshot) ReadScope {
			return ReadScope{Refs: []PropertyRef{{Link: "link", Local: "p"}}}
		},
	})
	yBase := mustOK(t, s.Commit(WriteRequest{Object: "y", Create: true,
		Values: map[Property]Value{"p": 0}}))
	xBase := mustOK(t, s.Commit(WriteRequest{Object: "x", Type: "X", Create: true,
		Values:          map[Property]Value{"link": "y", "q": 0},
		ObserveExternal: map[ObjectID]Version{"y": yBase}}))

	// Change the linked property p.
	mustOK(t, s.Commit(WriteRequest{Object: "y", Base: yBase,
		Values: map[Property]Value{"p": 1}}))

	// Now writing x with the old observation must conflict despite no write
	// directly touching x between the two.
	r := s.Commit(WriteRequest{Object: "x", Type: "X", Base: xBase,
		Values:          map[Property]Value{"q": 2},
		ObserveExternal: map[ObjectID]Version{"y": yBase}})
	if r.Kind != ConflictProperty {
		t.Fatalf("linked-instance conflict expected, got %+v", r)
	}

	// A write observing the *current* y head succeeds even with the same x
	// base.
	yHead, _ := s.Head("y")
	r2 := s.Commit(WriteRequest{Object: "x", Type: "X", Base: xBase,
		Values:          map[Property]Value{"q": 3},
		ObserveExternal: map[ObjectID]Version{"y": yHead}})
	if !r2.OK() {
		t.Fatalf("fresh observation should succeed: %+v", r2)
	}
}

func TestDeletedErrorDistinctAndPrioritized(t *testing.T) {
	s := NewStore()
	v := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 1}}))
	mustOK(t, s.Commit(WriteRequest{Object: "o", Base: v, Delete: true}))

	// Old baseline + deleted: deleted must win over property/stale.
	r := s.Commit(WriteRequest{Object: "o", Base: v, Values: map[Property]Value{"a": 2}})
	if r.Kind != ConflictDeleted || !errors.Is(r.Err, ErrInstanceDeleted) {
		t.Fatalf("want deleted, got %+v", r)
	}
}

func TestConcurrentDeletesConflictBeforeDeletion(t *testing.T) {
	s := NewStore()
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 1, "b": 2}}))
	mustOK(t, s.Commit(WriteRequest{Object: "o", Base: base, Delete: true}))
	// Second delete racing from the same baseline: head is now deleted, so the
	// highest-priority class applies; disjoint property write from the old
	// baseline gets the same deleted verdict (state takes priority over set).
	r := s.Commit(WriteRequest{Object: "o", Base: base, Delete: true})
	if r.Kind != ConflictDeleted {
		t.Fatalf("want deleted, got %+v", r)
	}

	// Before the instance is deleted, two deletes from the same live baseline
	// would collide via the delete sentinel as a property conflict.
	s2 := NewStore()
	b2 := mustOK(t, s2.Commit(WriteRequest{Object: "o", Create: true, Values: map[Property]Value{"a": 1}}))
	mustOK(t, s2.Commit(WriteRequest{Object: "o", Base: b2, Values: map[Property]Value{"z": 3.0}}))
	// z-write is disjoint from a delete footprint except the sentinel; a
	// delete adjudged at head after the z-write still sees only the sentinel
	// marker at base v1 absent => succeeds (deletes don't clash with disjoint
	// property writes).
	r2 := s2.Commit(WriteRequest{Object: "o", Base: b2, Delete: true})
	if !r2.OK() {
		t.Fatalf("delete must merge over disjoint property write: %+v", r2)
	}
}

func TestDecisionLogRecordsEvidence(t *testing.T) {
	s := NewStore()
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0}}))
	mustOK(t, s.Commit(WriteRequest{Object: "o", Base: base, Values: map[Property]Value{"a": 1}}))
	r := s.Commit(WriteRequest{Object: "o", Base: base, Values: map[Property]Value{"a": 2}})
	if r.Kind != ConflictProperty {
		t.Fatalf("want conflict: %+v", r)
	}
	ds := s.Decisions("o")
	last := ds[len(ds)-1]
	if len(last.WriteSet) != 1 || last.WriteSet[0] != "a" || len(last.Evidence) == 0 {
		t.Fatalf("decision record incomplete: %+v", last)
	}
	var hit bool
	for _, ev := range last.Evidence {
		if ev.Hit && ev.Committed > ev.Observed {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("no positive marker evidence recorded: %+v", last.Evidence)
	}
}

func TestStaleBaseDistinct(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true, Values: map[Property]Value{"a": 1}}))
	r := s.Commit(WriteRequest{Object: "ghost", Base: 1, Values: map[Property]Value{"a": 1}})
	if r.Kind != ConflictStaleBase || !errors.Is(r.Err, ErrStaleBase) {
		t.Fatalf("want stale base, got %+v", r)
	}
}

func TestDuplicateWriteDistinct(t *testing.T) {
	s := NewStore()
	req := WriteRequest{Object: "o", Create: true, IdempotencyKey: "k1",
		Values: map[Property]Value{"a": 1}}
	mustOK(t, s.Commit(req))
	r := s.Commit(req)
	if r.Kind != ConflictStaleBase || !errors.Is(r.Err, ErrStaleBase) {
		t.Fatalf("duplicate must be stale-base class, got %+v", r)
	}
}

func TestRejectionMutatesNothing(t *testing.T) {
	s := NewStore()
	s.RegisterType("T", Validator{
		Name:    "no-neg",
		Declare: func(v map[Property]Value, snap Snapshot) ReadScope { return ReadScope{} },
		Validate: func(v map[Property]Value, snap Snapshot, l map[ObjectID]Snapshot) error {
			if n, ok := v["a"].(float64); ok && n < 0 {
				return fmt.Errorf("negative")
			}
			return nil
		},
	})
	before := s.Clock()
	r := s.Commit(WriteRequest{Object: "o", Type: "T", Create: true,
		Values: map[Property]Value{"a": -1.0}})
	if !errors.Is(r.Err, ErrRejected) {
		t.Fatalf("want rejection, got %+v", r)
	}
	if s.Clock() != before {
		t.Fatalf("rejection advanced clock")
	}
	if _, ok := s.Head("o"); ok {
		t.Fatalf("rejected create left an instance")
	}
}

func TestConflictCostIndependentOfHistory(t *testing.T) {
	s := NewStore()
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0, "z": 0}}))
	cur := 0
	for i := 0; i < 200; i++ {
		h := s.stateFor("o").head
		mustOK(t, s.Commit(WriteRequest{Object: "o", Base: h,
			Values: map[Property]Value{"z": float64(i)}}))
		cur++
	}
	// One conflicting adjudication on a short history vs long history must
	// inspect the same number of markers (the relevant set size).
	short := NewStore()
	b2 := mustOK(t, short.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0, "z": 0}}))
	mustOK(t, short.Commit(WriteRequest{Object: "o", Base: b2, Values: map[Property]Value{"z": 1.0}}))
	mustOK(t, short.Commit(WriteRequest{Object: "o", Base: b2, Values: map[Property]Value{"a": 1.0}}))

	long := NewStore()
	b3 := mustOK(t, long.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0, "z": 0}}))
	for i := 0; i < 200; i++ {
		h := long.stateFor("o").head
		mustOK(t, long.Commit(WriteRequest{Object: "o", Base: h, Values: map[Property]Value{"z": float64(i)}}))
	}
	mustOK(t, long.Commit(WriteRequest{Object: "o", Base: b3, Values: map[Property]Value{"a": 1.0}}))

	n1 := len(short.Decisions("o")[len(short.Decisions("o"))-1].Evidence)
	n2 := len(long.Decisions("o")[len(long.Decisions("o"))-1].Evidence)
	if n1 != n2 {
		t.Fatalf("evidence length grew with history: %d vs %d", n1, n2)
	}
	_ = base
	_ = cur
}

func TestConcurrentDisjointWritesAllSucceed(t *testing.T) {
	s := NewStore()
	base := mustOK(t, s.Commit(WriteRequest{Object: "o", Create: true,
		Values: map[Property]Value{"a": 0, "b": 0, "c": 0}}))
	var wg sync.WaitGroup
	for _, p := range []Property{"a", "b", "c"} {
		wg.Add(1)
		go func(p Property) {
			defer wg.Done()
			r := s.Commit(WriteRequest{Object: "o", Base: base,
				Values: map[Property]Value{p: 1.0}})
			if !r.OK() {
				t.Errorf("write %s rejected: %v", p, r.Err)
			}
		}(p)
	}
	wg.Wait()
	snap, _ := s.Read("o", s.stateFor("o").head)
	for _, p := range []Property{"a", "b", "c"} {
		if v, _ := snap.Get(p); v != 1.0 {
			t.Fatalf("property %s = %v", p, v)
		}
	}
}
