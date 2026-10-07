package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func testRegistry() *Registry {
	return NewRegistry().
		RegisterObjectType(ObjectType{Name: "Person"}).
		RegisterObjectType(ObjectType{Name: "Team"}).
		RegisterLinkType(LinkType{Name: "member", Source: "Person", Target: "Team", SrcMax: 2, DstMax: 0}).
		RegisterLinkType(LinkType{Name: "lead", Source: "Person", Target: "Team", SrcMax: 1, DstMax: 1})
}

func mustApply(t *testing.T, s *Store, in BatchInput) *BatchResult {
	t.Helper()
	res, err := s.ApplyBatch(in)
	if err != nil {
		t.Fatalf("unexpected failure: %v", err)
	}
	return res
}

func failKind(t *testing.T, err error) FailureKind {
	t.Helper()
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %T: %v", err, err)
	}
	return be.Kind
}

func seedPeople(t *testing.T, s *Store, ids ...InstanceID) {
	t.Helper()
	items := make([]BatchItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, BatchItem{ID: id, Type: "Person", Create: true, Properties: Property{"n": string(id)}})
	}
	mustApply(t, s, BatchInput{Items: items})
}

func TestBatchCommitsAtomicallyAndBumpsVersions(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "a", "b")

	res := mustApply(t, s, BatchInput{Items: []BatchItem{
		{ID: "a", Type: "Person", Baseline: 1, Properties: Property{"n": "A"}},
		{ID: "b", Type: "Person", Baseline: 1, Properties: Property{"n": "B"}},
	}})
	if res.CommitSeq != 2 {
		t.Fatalf("commit seq = %d, want 2", res.CommitSeq)
	}
	if res.Versions["a"] != 2 || res.Versions["b"] != 2 {
		t.Fatalf("versions = %v, want a=2 b=2", res.Versions)
	}
	snap := s.Snapshot([]InstanceID{"a", "b"})
	if snap["a"].Properties["n"] != "A" || snap["b"].Properties["n"] != "B" {
		t.Fatalf("post-commit snapshot = %v %v", snap["a"].Properties, snap["b"].Properties)
	}
}

func TestDuplicateDeclarationCheckedFirstAndMutatesNothing(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "a")

	before := s.Snapshot([]InstanceID{"a"})["a"]
	beforeSeq := s.CommitSeq()
	in := BatchInput{Items: []BatchItem{
		{ID: "a", Type: "Person", Baseline: 99, Properties: Property{"n": "stale"}},
		{ID: "a", Type: "Person", Baseline: 1, Properties: Property{"n": "dup"}},
	}}
	_, err := s.ApplyBatch(in)
	if kind := failKind(t, err); kind != FailureDuplicateDecl {
		t.Fatalf("kind = %s, want duplicate", kind)
	}
	if s.CommitSeq() != beforeSeq {
		t.Fatalf("commit clock moved after reject: %d -> %d", beforeSeq, s.CommitSeq())
	}
	after := s.Snapshot([]InstanceID{"a"})["a"]
	if after.Version != before.Version || !reflect.DeepEqual(after.Properties, before.Properties) {
		t.Fatalf("rejected duplicate batch changed instance: before=%v after=%v", before, after)
	}
}

func TestVersionConflictOneOfManyRejectsWholeBatch(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "a", "b", "c")

	in := BatchInput{Items: []BatchItem{
		{ID: "a", Type: "Person", Baseline: 1, Properties: Property{"n": "A"}},
		{ID: "b", Type: "Person", Baseline: 7, Properties: Property{"n": "B"}},
		{ID: "c", Type: "Person", Baseline: 1, Properties: Property{"n": "C"}},
	}}
	_, err := s.ApplyBatch(in)
	be := &BatchError{}
	errors.As(err, &be)
	if be.Kind != FailureVersionConflict || be.Index != 1 {
		t.Fatalf("got %+v, want version conflict at index 1", be)
	}
	for _, id := range []InstanceID{"a", "b", "c"} {
		got := s.Snapshot([]InstanceID{id})[id]
		if got.Version != 1 || got.Properties["n"] != string(id) {
			t.Fatalf("instance %s changed despite rejected batch: %v", id, got)
		}
	}
}

func TestVersionConflictBeatsValidation(t *testing.T) {
	reg := testRegistry()
	reg.RegisterObjectType(ObjectType{
		Name: "Strict",
		Validate: ValidationHook{Run: func(p *Instance, v BatchView) string {
			return "always reject"
		}},
	})
	s := NewStore(reg)
	seedPeople(t, s, "a")
	mustApply(t, s, BatchInput{Items: []BatchItem{
		{ID: "x", Type: "Team", Create: true},
	}})

	in := BatchInput{Items: []BatchItem{
		{ID: "a", Type: "Person", Baseline: 42, Properties: Property{}},
		{ID: "x", Type: "Strict", Baseline: 1, Properties: Property{}},
	}}
	_, err := s.ApplyBatch(in)
	if kind := failKind(t, err); kind != FailureVersionConflict {
		t.Fatalf("want version conflict, got %s (%v)", kind, err)
	}

	_, err = s.ApplyBatch(BatchInput{Items: []BatchItem{
		{ID: "a", Type: "Person", Baseline: 1, Properties: Property{}},
		{ID: "x", Type: "Strict", Baseline: 1, Properties: Property{}},
	}})
	if kind := failKind(t, err); kind != FailureValidationRejected {
		t.Fatalf("want validation rejection, got %s (%v)", kind, err)
	}
}

func TestCardinalityEvaluatedOnFinalImageNotIntermediate(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "p")
	mustApply(t, s, BatchInput{Items: []BatchItem{
		{ID: "t1", Type: "Team", Create: true},
		{ID: "t2", Type: "Team", Create: true},
		{ID: "t3", Type: "Team", Create: true},
		{
			ID:         "p",
			Type:       "Person",
			Baseline:   1,
			Properties: Property{"n": "p"},
			LinkDeltas: []EdgeDelta{
				{Edge: Edge{"member", "p", "t1"}, Add: true},
				{Edge: Edge{"member", "p", "t2"}, Add: true},
			},
		},
	}})

	res := mustApply(t, s, BatchInput{Items: []BatchItem{
		{
			ID:         "p",
			Type:       "Person",
			Baseline:   2,
			Properties: Property{"n": "p"},
			LinkDeltas: []EdgeDelta{
				{Edge: Edge{"member", "p", "t1"}, Add: false},
				{Edge: Edge{"member", "p", "t3"}, Add: true},
			},
		},
	}})
	if res.Versions["p"] != 3 {
		t.Fatalf("version = %d, want 3", res.Versions["p"])
	}
	if !s.HasEdge(Edge{"member", "p", "t3"}) || s.HasEdge(Edge{"member", "p", "t1"}) {
		t.Fatalf("edge swap not published correctly")
	}
}

func TestCardinalityJustOverBoundRejectsWholeBatch(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "p")
	mustApply(t, s, BatchInput{Items: []BatchItem{
		{ID: "t1", Type: "Team", Create: true},
		{ID: "t2", Type: "Team", Create: true},
		{ID: "t3", Type: "Team", Create: true},
		{
			ID:         "p",
			Type:       "Person",
			Baseline:   1,
			Properties: Property{"n": "p"},
			LinkDeltas: []EdgeDelta{
				{Edge: Edge{"member", "p", "t1"}, Add: true},
				{Edge: Edge{"member", "p", "t2"}, Add: true},
			},
		},
	}})

	beforeSeq := s.CommitSeq()
	_, err := s.ApplyBatch(BatchInput{Items: []BatchItem{
		{
			ID:         "p",
			Type:       "Person",
			Baseline:   2,
			Properties: Property{"n": "p"},
			LinkDeltas: []EdgeDelta{{Edge: Edge{"member", "p", "t3"}, Add: true}},
		},
	}})
	be := &BatchError{}
	errors.As(err, &be)
	if be.Kind != FailureCardinality {
		t.Fatalf("got %+v, want cardinality", be)
	}
	if s.HasEdge(Edge{"member", "p", "t3"}) {
		t.Fatalf("rejected edge became visible")
	}
	if s.CommitSeq() != beforeSeq {
		t.Fatalf("clock advanced on cardinality reject")
	}
	if got := s.Snapshot([]InstanceID{"p"})["p"]; got.Version != 2 {
		t.Fatalf("version changed on reject: %d", got.Version)
	}
}

func TestRejectedBatchAfterHookRunsLeavesNoTrace(t *testing.T) {
	reg := testRegistry()
	hookRan := false
	reg.RegisterObjectType(ObjectType{
		Name: "Guarded",
		Validate: ValidationHook{Run: func(p *Instance, v BatchView) string {
			hookRan = true
			return "nope"
		}},
	})
	s := NewStore(reg)
	seedPeople(t, s, "a")

	in := BatchInput{Items: []BatchItem{
		{ID: "g", Type: "Guarded", Create: true, Properties: Property{"n": "g"}},
		{ID: "a", Type: "Person", Baseline: 1, Properties: Property{"n": "A"}},
	}}
	_, err := s.ApplyBatch(in)
	if kind := failKind(t, err); kind != FailureValidationRejected {
		t.Fatalf("got %v", err)
	}
	if !hookRan {
		t.Fatalf("hook did not run before rejection")
	}
	if s.GetOne("g") != nil {
		t.Fatalf("rejected create leaked into store")
	}
	if got := s.GetOne("a"); got.Version != 1 || got.Properties["n"] != "a" {
		t.Fatalf("unrelated item changed: %v", got)
	}
}

func TestDecisionTouchesOnlyBatchInstances(t *testing.T) {
	s := NewStore(testRegistry())
	ids := make([]InstanceID, 0, 200)
	for i := 0; i < 200; i++ {
		ids = append(ids, InstanceID(fmt.Sprintf("bulk-%03d", i)))
	}
	seedPeople(t, s, ids...)

	res := mustApply(t, s, BatchInput{Items: []BatchItem{
		{ID: "bulk-007", Type: "Person", Baseline: 1, Properties: Property{"n": "x"}},
		{ID: "bulk-150", Type: "Person", Baseline: 1, Properties: Property{"n": "y"}},
	}})
	touched := make([]string, 0, len(res.Record.InstanceTouches))
	for id := range res.Record.InstanceTouches {
		touched = append(touched, string(id))
	}
	sort.Strings(touched)
	want := []string{"bulk-007", "bulk-150"}
	if !reflect.DeepEqual(touched, want) {
		t.Fatalf("touched = %v, want %v", touched, want)
	}
}
