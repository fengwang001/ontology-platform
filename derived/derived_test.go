package derived

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

type sliceLogger struct {
	mu      sync.Mutex
	records []ChangeRecord
}

func (l *sliceLogger) Log(r ChangeRecord) {
	l.mu.Lock()
	l.records = append(l.records, r)
	l.mu.Unlock()
}

func mustAddObject(t *testing.T, s *Store, o Object) {
	t.Helper()
	if err := s.AddObject(o); err != nil {
		t.Fatal(err)
	}
}

func entryStateKeys(t *testing.T, s *Store, decl, id string) (EntryState, []Value) {
	t.Helper()
	e, ok := s.Entry(decl, id)
	if !ok {
		t.Fatalf("entry %s/%s missing", decl, id)
	}
	return e.State, e.Keys
}

func expectIndexed(t *testing.T, s *Store, decl, id, want string) {
	t.Helper()
	st, keys := entryStateKeys(t, s, decl, id)
	if st != StateIndexed || len(keys) != 1 || keys[0] != want {
		t.Fatalf("entry %s/%s = %v %v, want INDEXED %q", decl, id, st, keys, want)
	}
}

func expectState(t *testing.T, s *Store, decl, id string, want EntryState) {
	t.Helper()
	st, _ := entryStateKeys(t, s, decl, id)
	if st != want {
		t.Fatalf("entry %s/%s = %v, want %v", decl, id, st, want)
	}
}

func buildBasic(t *testing.T) *Store {
	t.Helper()
	s := NewStore(nil)
	mustAddObject(t, s, Object{ID: "a1", Type: "A", Properties: map[string]Value{"name": "red"}})
	mustAddObject(t, s, Object{ID: "b1", Type: "B"})
	mustAddObject(t, s, Object{ID: "b2", Type: "B"})
	if err := s.AddDeclaration(Declaration{
		Name: "idx", DownstreamType: "B", LinkType: "owns",
		SourceType: "A", SourceProperty: "name", RequireUnique: true,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// Single-level index tracks source writes and link changes; missing and
// non-unique linkage are explicit non-indexable states.
func TestSingleLevelConsistency(t *testing.T) {
	s := buildBasic(t)

	expectState(t, s, "idx", "b1", StateNoLink)

	if r := s.AddLink("b1", "a1", "owns"); !r.Committed {
		t.Fatalf("add link rolled back: %v", r.Err)
	}
	expectIndexed(t, s, "idx", "b1", "red")
	if got := s.Lookup("idx", "red"); !reflect.DeepEqual(got, []string{"b1"}) {
		t.Fatalf("lookup red = %v", got)
	}

	r := s.SetProperty("a1", "name", "blue")
	if !r.Committed {
		t.Fatalf("set property rolled back: %v", r.Err)
	}
	if !reflect.DeepEqual(r.Affected, []string{"b1"}) {
		t.Fatalf("affected = %v, want [b1]", r.Affected)
	}
	expectIndexed(t, s, "idx", "b1", "blue")

	mustAddObject(t, s, Object{ID: "a2", Type: "A", Properties: map[string]Value{"name": "green"}})
	r = s.AddLink("b1", "a2", "owns")
	if !r.Committed {
		t.Fatalf("second link rolled back: %v", r.Err)
	}
	expectState(t, s, "idx", "b1", StateNotUnique)
	if got := s.Lookup("idx", "green"); len(got) != 0 {
		t.Fatalf("non-unique entry must not resolve, got %v", got)
	}

	r = s.DeleteLink("b1", "a1", "owns")
	if !r.Committed {
		t.Fatalf("delete link rolled back: %v", r.Err)
	}
	expectIndexed(t, s, "idx", "b1", "green")

	s.DeleteLink("b1", "a2", "owns")
	expectState(t, s, "idx", "b1", StateNoLink)
}

// Not-unique transition is produced by the AddLink unit itself, so it can
// never be observed later than the link change.
func TestUniquenessBreakImmediate(t *testing.T) {
	s := buildBasic(t)
	mustAddObject(t, s, Object{ID: "a2", Type: "A", Properties: map[string]Value{"name": "x"}})
	s.AddLink("b1", "a1", "owns")
	r := s.AddLink("b1", "a2", "owns")
	if !r.Committed {
		t.Fatalf("add link rolled back: %v", r.Err)
	}
	found := false
	for _, e := range r.Entries {
		if e.ObjectID == "b1" && e.State == StateNotUnique {
			found = true
		}
	}
	if !found {
		t.Fatalf("AddLink result must already carry NOT_UNIQUE entry, got %+v", r.Entries)
	}
}

// A failing downstream update rolls the whole processing unit back.
func TestDownstreamFailureRollback(t *testing.T) {
	s := buildBasic(t)
	s.AddLink("b1", "a1", "owns")
	s.FailHook = func(decl, id string) bool { return decl == "idx" && id == "b1" }

	r := s.SetProperty("a1", "name", "blue")
	if r.Committed || r.Err == nil || r.Err.Kind != KindDownstreamUpdateFailed {
		t.Fatalf("want rollback with downstream failure, got %+v", r)
	}
	expectIndexed(t, s, "idx", "b1", "red")

	s.FailHook = func(decl, id string) bool { return id == "b2" }
	r = s.AddLink("b2", "a1", "owns")
	if r.Committed || r.Err == nil || r.Err.Kind != KindDownstreamUpdateFailed {
		t.Fatalf("want add-link rollback, got %+v", r)
	}
	expectState(t, s, "idx", "b2", StateNoLink)
}

// Multi-level transitive propagation through chained declarations.
func TestMultiLevelPropagation(t *testing.T) {
	s := NewStore(nil)
	mustAddObject(t, s, Object{ID: "root", Type: "R", Properties: map[string]Value{"v": "one"}})
	mustAddObject(t, s, Object{ID: "mid", Type: "M"})
	mustAddObject(t, s, Object{ID: "leaf", Type: "L"})
	if err := s.AddDeclaration(Declaration{
		Name: "d_mid", DownstreamType: "M", LinkType: "toR",
		SourceType: "R", SourceProperty: "v", RequireUnique: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDeclaration(Declaration{
		Name: "d_leaf", DownstreamType: "L", LinkType: "toM",
		SourceType: "M", SourceProperty: "d_mid", RequireUnique: true,
	}); err != nil {
		t.Fatal(err)
	}
	s.AddLink("mid", "root", "toR")
	s.AddLink(("leaf"), "mid", "toM")
	expectIndexed(t, s, "d_leaf", "leaf", "one")

	r := s.SetProperty("root", "v", "two")
	if !r.Committed {
		t.Fatalf("set rolled back: %v", r.Err)
	}
	sort.Strings(r.Affected)
	if !reflect.DeepEqual(r.Affected, []string{"leaf", "mid"}) {
		t.Fatalf("affected = %v, want [leaf mid]", r.Affected)
	}
	expectIndexed(t, s, "d_leaf", "leaf", "two")

	s.DeleteLink("mid", "root", "toR")
	expectState(t, s, "d_mid", "mid", StateNoLink)
	expectState(t, s, "d_leaf", "leaf", StateNoLink)
}

// A cyclic declaration reference (d1<->d2) is registered as schema, but any
// link addition that would realize the transitive loop is rejected before
// becoming visible.
func TestDeclarationCycleRejected(t *testing.T) {
	s := NewStore(nil)
	mustAddObject(t, s, Object{ID: "b1", Type: "B"})
	mustAddObject(t, s, Object{ID: "a1", Type: "A"})
	d1 := Declaration{Name: "d1", DownstreamType: "B", LinkType: "l1",
		SourceType: "A", SourceProperty: "d2", RequireUnique: true}
	d2 := Declaration{Name: "d2", DownstreamType: "A", LinkType: "l2",
		SourceType: "B", SourceProperty: "d1", RequireUnique: true}
	if err := s.AddDeclaration(d1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDeclaration(d2); err != nil {
		t.Fatal(err)
	}
	s.AddLink("b1", "a1", "l1")
	r := s.AddLink("a1", "b1", "l2")
	if r.Committed || r.Err == nil || r.Err.Kind != KindCycleDetected {
		t.Fatalf("want instance cycle rejection, got %+v", r)
	}
	if _, ok := s.cur.links[linkKey{"a1", "l2", "b1"}]; ok {
		t.Fatal("cyclic link must not be present")
	}
}

// Instance-level cycle: the link closing a transitive value loop is refused
// before it becomes visible.
func TestInstanceCycleRejected(t *testing.T) {
	s := NewStore(nil)
	mustAddObject(t, s, Object{ID: "x", Type: "M", Properties: map[string]Value{"v": "p"}})
	mustAddObject(t, s, Object{ID: "y", Type: "M"})
	mustAddObject(t, s, Object{ID: "z", Type: "M"})
	if err := s.AddDeclaration(Declaration{
		Name: "d", DownstreamType: "M", LinkType: "next",
		SourceType: "M", SourceProperty: "d", RequireUnique: true,
	}); err != nil {
		t.Fatal(err)
	}
	s.AddLink("y", "x", "next")
	s.AddLink("z", "y", "next")
	r := s.AddLink("x", "z", "next")
	if r.Committed || r.Err == nil || r.Err.Kind != KindCycleDetected {
		t.Fatalf("want cycle rejection, got %+v", r)
	}
	if _, ok := s.cur.links[linkKey{"x", "next", "z"}]; ok {
		t.Fatal("cyclic link must not be present")
	}
	mustAddObject(t, s, Object{ID: "w", Type: "M", Properties: map[string]Value{"v": "q"}})
	if r := s.AddLink("x", "w", "next"); !r.Committed {
		t.Fatalf("acyclic add rejected: %v", r.Err)
	}
}

// Cascade behavior when source and downstream instances are deleted.
func TestDeleteCascade(t *testing.T) {
	s := buildBasic(t)
	s.AddLink("b1", "a1", "owns")
	s.AddLink("b2", "a1", "owns")
	expectIndexed(t, s, "idx", "b1", "red")
	expectIndexed(t, s, "idx", "b2", "red")

	s.DeleteObject("b1")
	if _, ok := s.Entry("idx", "b1"); ok {
		t.Fatal("deleted downstream entry must disappear")
	}
	expectIndexed(t, s, "idx", "b2", "red")

	r := s.DeleteObject("a1")
	if !r.Committed {
		t.Fatalf("source delete rolled back: %v", r.Err)
	}
	if !reflect.DeepEqual(r.Affected, []string{"b2"}) {
		t.Fatalf("source delete affected = %v, want [b2]", r.Affected)
	}
	expectState(t, s, "idx", "b2", StateNoLink)
}

// Fixed error priority when several conditions coincide.
func TestErrorPriority(t *testing.T) {
	s := buildBasic(t)

	r := s.AddLink("b1", "ghost", "unknownType")
	if r.Err == nil || r.Err.Kind != KindSourceNotFound {
		t.Fatalf("want source-not-found, got %v", r.Err)
	}

	mustAddObject(t, s, Object{ID: "c1", Type: "C"})
	r = s.AddLink("b1", "c1", "strange")
	if r.Err == nil || r.Err.Kind != KindUnsupportedLinkType {
		t.Fatalf("want unsupported, got %v", r.Err)
	}

	r = s.SetProperty("ghost", "name", "v")
	if r.Err == nil || r.Err.Kind != KindSourceNotFound {
		t.Fatalf("want source-not-found, got %v", r.Err)
	}

	errs := []*IndexError{
		{Kind: KindDownstreamUpdateFailed},
		{Kind: KindSourceNotFound},
		{Kind: KindCycleDetected},
	}
	if highestPriorityError(errs).Kind != KindSourceNotFound {
		t.Fatal("priority helper wrong")
	}
}
