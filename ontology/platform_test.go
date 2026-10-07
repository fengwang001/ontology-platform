package ontology

import (
	"strings"
	"sync"
	"testing"
)

func testPlatform() *Platform {
	p := NewPlatform()
	p.RegisterObjectType(ObjectType{Name: "Person", Hook: func(cur, prop Properties) error {
		if prop != nil {
			if _, ok := prop["name"]; !ok {
				return errStr("missing name")
			}
		}
		return nil
	}})
	p.RegisterObjectType(ObjectType{Name: "Group"})
	return p
}

type errStr string

func (e errStr) Error() string { return string(e) }

func props(m map[string]any) Properties { return Properties(m) }

func TestBatchHappyPathVersionsAndTick(t *testing.T) {
	p := testPlatform()
	r := p.Commit(Batch{ID: "b1", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Alice"})},
		{Instance: "bob", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Bob"})},
	}})
	if !r.OK || r.CommitTick != 1 {
		t.Fatalf("commit: %+v", r)
	}
	if got := p.Read("alice"); got.Version != 1 || got.Props["name"] != "Alice" {
		t.Fatalf("alice: %+v", got)
	}
	if p.Tick() != 1 {
		t.Fatalf("tick %d", p.Tick())
	}
	r2 := p.Commit(Batch{ID: "b2", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "Alicia"})},
	}})
	if !r2.OK || r2.CommitTick != 2 || r2.NewVersions["alice"] != 2 {
		t.Fatalf("commit2: %+v", r2)
	}
}

func TestDuplicateWriteDetectedFirst(t *testing.T) {
	p := testPlatform()
	before := p.Tick()
	r := p.Commit(Batch{ID: "dup", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 99, Props: props(map[string]any{"name": "A"})},
		{Instance: "alice", Type: "Person", BaseVersion: 99, Props: props(map[string]any{"name": "B"})},
	}})
	if r.OK || r.Failure != FailureDuplicateWrite {
		t.Fatalf("want duplicate, got %+v", r)
	}
	if p.Tick() != before || p.InstanceCount() != 0 {
		t.Fatalf("state changed on rollback tick=%d count=%d", p.Tick(), p.InstanceCount())
	}
}

func TestVersionConflictPartialBatch(t *testing.T) {
	p := testPlatform()
	p.Commit(Batch{ID: "init", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Alice"})},
		{Instance: "bob", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Bob"})},
	}})
	before := p.ReadMany("alice", "bob")
	r := p.Commit(Batch{ID: "stale", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "A"})},
		{Instance: "bob", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "B-stale-claims-v0"})},
	}})
	if r.OK || r.Failure != FailureVersionConflict || !strings.Contains(r.Detail, "bob") {
		t.Fatalf("want conflict on bob, got %+v", r)
	}
	for _, want := range before {
		got := p.Read(want.ID)
		if got.Version != want.Version || got.Props["name"] != want.Props["name"] {
			t.Fatalf("rollback changed %s: %+v vs %+v", want.ID, got, want)
		}
	}
}

func TestHookRejectRollsAllBack(t *testing.T) {
	p := testPlatform()
	p.Commit(Batch{ID: "init", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Alice"})},
	}})
	calls := 0
	var mu sync.Mutex
	p.RegisterObjectType(ObjectType{Name: "Strict", Hook: func(cur, prop Properties) error {
		mu.Lock()
		calls++
		mu.Unlock()
		if prop != nil && prop["v"].(int) < 10 {
			return errStr("v too small")
		}
		return nil
	}})
	r := p.Commit(Batch{ID: "hook", Ops: []Operation{
		{Instance: "alice", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "Alice2"})},
		{Instance: "x", Type: "Strict", BaseVersion: 0, Props: props(map[string]any{"v": 3})},
	}})
	if r.OK || r.Failure != FailureHookRejected || calls != 1 {
		t.Fatalf("want hook reject (calls=%d), got %+v", calls, r)
	}
	if a := p.Read("alice"); a.Version != 1 || a.Props["name"] != "Alice" {
		t.Fatalf("alice changed after hook rollback: %+v", a)
	}
	if p.Read("x").Exists {
		t.Fatalf("x created despite failed batch")
	}
}

func TestFailurePriorityVersionBeforeHook(t *testing.T) {
	p := testPlatform()
	r := p.Commit(Batch{ID: "prio", Ops: []Operation{
		{Instance: "ghost", Type: "Person", BaseVersion: 5, Props: props(map[string]any{})},
	}})
	if r.Failure != FailureVersionConflict {
		t.Fatalf("want version conflict, got %+v", r)
	}
}

func TestCardinalityBoundaryOnFinalState(t *testing.T) {
	p := testPlatform()
	// Each group must have >=1 and <=2 members; a person can be in many groups.
	p.RegisterLinkType(LinkType{
		Name: "membership", LeftType: "Person", RightType: "Group",
		CardA: Cardinality{Min: 0, Max: -1}, CardB: Cardinality{Min: 1, Max: 2},
	})
	setup := Batch{ID: "setup", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "A"})},
		{Instance: "b", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "B"})},
		{Instance: "c", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "C"})},
		{Instance: "g", Type: "Group", BaseVersion: 0, Props: props(map[string]any{"title": "G"})},
	}}
	if r := p.Commit(setup); !r.OK {
		t.Fatalf("setup: %+v", r)
	}
	// Final state 3 members: max 2 violated even though each link op is valid alone.
	over := Batch{ID: "over", Links: []LinkOp{
		{Link: "membership", A: "a", B: "g", Add: true},
		{Link: "membership", A: "b", B: "g", Add: true},
		{Link: "membership", A: "c", B: "g", Add: true},
	}}
	if r := p.Commit(over); r.OK || r.Failure != FailureCardinality {
		t.Fatalf("want cardinality reject, got %+v", r)
	}
	if links := p.ExportLinks("membership"); len(links) != 0 {
		t.Fatalf("links partially applied: %+v", links)
	}
	// Exactly at boundary (2): accepted.
	ok := Batch{ID: "two", Links: []LinkOp{
		{Link: "membership", A: "a", B: "g", Add: true},
		{Link: "membership", A: "b", B: "g", Add: true},
	}}
	if r := p.Commit(ok); !r.OK {
		t.Fatalf("boundary 2 rejected: %+v", r)
	}
	// Remove the last member -> min 1 violated on final state.
	drop := Batch{ID: "drop", Links: []LinkOp{
		{Link: "membership", A: "a", B: "g", Add: false},
		{Link: "membership", A: "b", B: "g", Add: false},
	}}
	if r := p.Commit(drop); r.OK || r.Failure != FailureCardinality {
		t.Fatalf("want min-cardinality reject, got %+v", r)
	}
	if links := p.ExportLinks("membership"); len(links) != 2 {
		t.Fatalf("links changed on rejected drop: %+v", links)
	}
}

func TestPerInstanceBasesCheckedIndividually(t *testing.T) {
	p := testPlatform()
	p.Commit(Batch{ID: "i", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "A"})},
		{Instance: "b", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "B"})},
	}})
	// a at v1, b at v1; caller uses mixed correct/incorrect bases.
	r := p.Commit(Batch{ID: "mixed", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "A2"})},
		{Instance: "b", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "B2"})},
	}})
	if r.OK || r.Failure != FailureVersionConflict || !strings.Contains(r.Detail, "b") {
		t.Fatalf("mixed bases: %+v", r)
	}
}

func TestFailedBatchDoesNotAdvanceClockOrVersions(t *testing.T) {
	p := testPlatform()
	p.Commit(Batch{ID: "i", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "A"})},
	}})
	t0 := p.Tick()
	for i := 0; i < 5; i++ {
		p.Commit(Batch{ID: "bad", Ops: []Operation{
			{Instance: "a", Type: "Person", BaseVersion: 999, Props: props(map[string]any{"name": "X"})},
		}})
	}
	if p.Tick() != t0 {
		t.Fatalf("clock advanced on failures: %d != %d", p.Tick(), t0)
	}
	if a := p.Read("a"); a.Version != 1 {
		t.Fatalf("version changed on failures: %d", a.Version)
	}
}

func TestDeleteResurrectAndLinks(t *testing.T) {
	p := testPlatform()
	p.RegisterLinkType(LinkType{Name: "knows", LeftType: "Person", RightType: "Person",
		CardA: Cardinality{0, -1}, CardB: Cardinality{0, -1}})
	p.Commit(Batch{ID: "i", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "A"})},
		{Instance: "b", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "B"})},
	}})
	p.Commit(Batch{ID: "l", Links: []LinkOp{{Link: "knows", A: "a", B: "b", Add: true}}})
	// Deleting a while link exists -> cardinality stage rejects (dangling link).
	aVer := p.Read("a").Version
	bVer := p.Read("b").Version
	r := p.Commit(Batch{ID: "del", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: aVer, Props: nil},
	}})
	if r.OK || r.Failure != FailureCardinality {
		t.Fatalf("dangling delete: %+v", r)
	}
	// Remove link and delete together in one batch: final state consistent.
	r2 := p.Commit(Batch{ID: "del2", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: aVer, Props: nil},
	}, Links: []LinkOp{{Link: "knows", A: "a", B: "b", Add: false}}})
	if !r2.OK {
		t.Fatalf("atomic unlink+delete: %+v", r2)
	}
	if p.Read("a").Exists || len(p.ExportLinks("knows")) != 0 {
		t.Fatalf("unexpected state after del2")
	}
	// b untouched version-wise.
	if p.Read("b").Version != bVer {
		t.Fatalf("b version changed: %d", p.Read("b").Version)
	}
	// Recreate a with base 0 (fresh nonexistent version).
	r3 := p.Commit(Batch{ID: "new", Ops: []Operation{
		{Instance: "a", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Anew"})},
	}})
	if !r3.OK || r3.NewVersions["a"] != 1 {
		t.Fatalf("recreate: %+v", r3)
	}
}

func TestDuplicateLinkAddRemove(t *testing.T) {
	p := testPlatform()
	r := p.Commit(Batch{ID: "d", Links: []LinkOp{
		{Link: "k", A: "a", B: "b", Add: true},
		{Link: "k", A: "b", B: "a", Add: false},
	}})
	if r.OK || r.Failure != FailureDuplicateWrite {
		t.Fatalf("want dup link, got %+v", r)
	}
}

// TestTwoOverlappingBatchesConcurrent: two batches share one instance and
// both read the same base. Exactly one must commit; the loser must get a
// version conflict and must not partially apply. Run repeatedly to cover both
// winner orderings.
func TestTwoOverlappingBatchesConcurrent(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		p := testPlatform()
		p.Commit(Batch{ID: "init", Ops: []Operation{
			{Instance: "a", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "A"})},
			{Instance: "b", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "B"})},
			{Instance: "c", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "C"})},
		}})
		b1 := Batch{ID: "b1", Ops: []Operation{
			{Instance: "a", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "A1"})},
			{Instance: "b", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "B1"})},
		}}
		b2 := Batch{ID: "b2", Ops: []Operation{
			{Instance: "a", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "A2"})},
			{Instance: "c", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "C2"})},
		}}
		r1 := make(chan BatchResult, 1)
		r2 := make(chan BatchResult, 1)
		start := make(chan struct{})
		go func() { <-start; r1 <- p.Commit(b1) }()
		go func() { <-start; r2 <- p.Commit(b2) }()
		close(start)
		rr1, rr2 := <-r1, <-r2
		if rr1.OK == rr2.OK {
			t.Fatalf("iter %d: expected exactly one commit, got %+v %+v", iter, rr1, rr2)
		}
		var lose BatchResult
		var wb, lb Batch
		if rr1.OK {
			lose, wb, lb = rr2, b1, b2
		} else {
			lose, wb, lb = rr1, b2, b1
		}
		if lose.Failure != FailureVersionConflict {
			t.Fatalf("iter %d: loser failed with %d, want version conflict", iter, lose.Failure)
		}
		// Winner's both instances must be at v2 with winner's props.
		for _, op := range wb.Ops {
			s := p.Read(op.Instance)
			if s.Version != 2 || s.Props["name"] != op.Props["name"] {
				t.Fatalf("winner instance %s wrong: %+v", op.Instance, s)
			}
		}
		// Loser's non-shared instance must be untouched (still v1).
		for _, op := range lb.Ops {
			if op.Instance == "a" {
				continue
			}
			if s := p.Read(op.Instance); s.Version != 1 {
				t.Fatalf("loser side effect on %s: %+v", op.Instance, s)
			}
		}
	}
}
