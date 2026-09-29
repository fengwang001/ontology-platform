package ontology

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func testGraph() *Graph {
	return &Graph{
		Types: []*ObjectType{
			{
				RID:  "r.user",
				Name: "User",
				Properties: []Property{
					{Name: "id", TypeRef: ""},
					{Name: "manager", TypeRef: "User"},
					{Name: "primaryOrder", TypeRef: "Order"},
				},
				Links: []Link{
					{Name: "orders", SourceType: "User", TargetType: "Order"},
				},
			},
			{
				RID:  "r.order",
				Name: "Order",
				Properties: []Property{
					{Name: "buyer", TypeRef: "User"},
				},
			},
		},
		Actions: []*Action{
			{
				Name:    "placeOrder",
				Params:  []Param{{Name: "by", TypeRef: "User"}, {Name: "what", TypeRef: "Order"}},
				Inputs:  []string{"User", "Order"},
				Creates: []string{"Order"},
				Reads:   []string{"User"},
			},
			{
				Name:    "deleteUser",
				Updates: []string{"Order"},
				Deletes: []string{"User"},
			},
		},
	}
}

func newTestStore(t *testing.T, g *Graph) (*Store, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s, err := NewStore(g, log)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, &buf
}

func countRefsTo(g *Graph, name string) int {
	n := 0
	walkRefs(g, func(_ refLocation, ref string) {
		if ref == name {
			n++
		}
	})
	return n
}

func TestRenameUpdatesAllReferences(t *testing.T) {
	g := testGraph()
	oldRefs := countRefsTo(g, "User")
	if oldRefs == 0 {
		t.Fatal("fixture should contain references to User")
	}
	s, logs := newTestStore(t, g)

	out, err := s.RenameObjectType("User", "Person")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if out.RID != "r.user" || out.Name != "Person" {
		t.Fatalf("renamed type = %+v, want r.user/Person", out)
	}

	snap := s.Snapshot()
	if got := countRefsTo(snap, "User"); got != 0 {
		t.Fatalf("residual User refs = %d, want 0", got)
	}
	if got := countRefsTo(snap, "Person"); got != oldRefs {
		t.Fatalf("Person refs = %d, want %d (updated in place)", got, oldRefs)
	}
	if snap.Pending != nil {
		t.Fatal("pending marker must be cleared after commit")
	}

	wantRefs := map[string]int{
		"property.type":  2,
		"link.source":    1,
		"action.param":   1,
		"action.inputs":  1,
		"action.reads":   1,
		"action.deletes": 1,
	}
	byKind := map[string]int{}
	walkRefs(snap, func(loc refLocation, ref string) {
		if ref == "Person" {
			byKind[loc.Kind]++
		}
	})
	for kind, want := range wantRefs {
		if byKind[kind] != want {
			t.Fatalf("ref kind %s updated %d times, want %d", kind, byKind[kind], want)
		}
	}

	logText := logs.String()
	for _, want := range []string{
		`msg="reference updated"`, `location="property.type`,
		`location="link.source`, `location="action.deletes`,
		`msg="rename committed"`, "references_updated=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n%s", want, logText)
		}
	}
}

func TestRenameInvalidatesOldName(t *testing.T) {
	s, _ := newTestStore(t, testGraph())
	if _, err := s.RenameObjectType("User", "Person"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	g := s.Snapshot()
	if t2, ok := g.Resolve("User"); ok || t2 != nil {
		t.Fatalf("old name resolved to %+v, must be invalid", t2)
	}
	if t2 := g.Lookup("User"); t2 != nil {
		t.Fatal("Lookup on stale name must return nil")
	}

	_, err := s.RenameObjectType("User", "Someone")
	if !errors.Is(err, ErrTypeNotFound) {
		t.Fatalf("rename stale name err = %v, want TypeNotFound", err)
	}

	if t2 := s.Snapshot().Lookup("Person"); t2 == nil || t2.RID != "r.user" {
		t.Fatal("rejected rename mutated the type")
	}
	if countRefsTo(s.Snapshot(), "User") != 0 {
		t.Fatal("rejected rename left residual old refs")
	}
}

func TestRenameNameConflict(t *testing.T) {
	s, logs := newTestStore(t, testGraph())
	_, err := s.RenameObjectType("User", "Order")
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("err = %v, want NameConflict", err)
	}
	if ve, ok := err.(*VerificationError); !ok || ve.Detail == "" {
		t.Fatalf("error must carry decision detail: %v", err)
	}
	if t2 := s.Snapshot().Lookup("User"); t2 == nil || t2.RID != "r.user" {
		t.Fatal("type changed after rejected rename")
	}
	if !strings.Contains(logs.String(), "NameConflict") {
		t.Fatal("rejection reason not logged")
	}
}

func TestRenameTypeNotFound(t *testing.T) {
	s, _ := newTestStore(t, testGraph())
	_, err := s.RenameObjectType("Ghost", "Phantom")
	if !errors.Is(err, ErrTypeNotFound) {
		t.Fatalf("err = %v, want TypeNotFound", err)
	}
}

func TestNewStoreRejectsDanglingReference(t *testing.T) {
	g := testGraph()
	g.Actions[0].Inputs = append(g.Actions[0].Inputs, "Ghost")
	_, err := NewStore(g, slog.Default())
	if !errors.Is(err, ErrDanglingReference) {
		t.Fatalf("err = %v, want DanglingReference", err)
	}
}

func TestVerifyResidualOldNameAndStaleName(t *testing.T) {
	g := testGraph()

	residual := cloneGraph(g)
	for _, t2 := range residual.Types {
		if t2.RID == "r.user" {
			t2.Name = "Person"
		}
	}
	p := &pendingRename{RID: "r.user", OldName: "User", NewName: "Person"}
	residual.Pending = p
	if err := verifyGraph(residual, p); !errors.Is(err, ErrResidualOldName) {
		t.Fatalf("residual err = %v, want ResidualOldName", err)
	}

	stale := cloneGraph(g)
	stale.Pending = p
	if err := verifyGraph(stale, p); !errors.Is(err, ErrStaleName) {
		t.Fatalf("stale err = %v, want StaleName", err)
	}
}

func TestRenameAtomicOnInterruption(t *testing.T) {
	s, logs := newTestStore(t, testGraph())
	cause := errors.New("simulated crash before commit")
	s.failPoint = func(oldName, newName string) error { return cause }

	before := s.Snapshot()
	_, err := s.RenameObjectType("User", "Person")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want injected failure", err)
	}

	after := s.Snapshot()
	if after != before {
		t.Fatal("published snapshot must be untouched after rollback")
	}
	if after.Pending != nil {
		t.Fatal("no half-applied pending marker may be published")
	}
	if t2 := after.Lookup("User"); t2 == nil || t2.RID != "r.user" {
		t.Fatal("old type must still exist under old name after rollback")
	}
	if after.Lookup("Person") != nil {
		t.Fatal("new name must not exist after rollback")
	}
	if got := countRefsTo(after, "User"); got != countRefsTo(before, "User") {
		t.Fatal("references changed after rollback")
	}
	if !strings.Contains(logs.String(), "rolling back") {
		t.Fatal("rollback not logged")
	}

	s.failPoint = nil
	if _, err := s.RenameObjectType("User", "Person"); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestNewStoreRejectsHalfApplied(t *testing.T) {
	half := cloneGraph(testGraph())
	for _, t2 := range half.Types {
		if t2.RID == "r.user" {
			t2.Name = "Person"
		}
	}
	half.Pending = &pendingRename{RID: "r.user", OldName: "User", NewName: "Person"}

	_, err := NewStore(half, slog.Default())
	if !errors.Is(err, ErrHalfApplied) {
		t.Fatalf("err = %v, want HalfAppliedRename", err)
	}
}

func renameWithoutCommit(g *Graph, oldName, newName string) *Graph {
	target := g.Lookup(oldName)
	g.Pending = &pendingRename{RID: target.RID, OldName: oldName, NewName: newName}
	walkRefs(g, func(loc refLocation, ref string) {
		if ref == oldName {
			rewriteRef(g, loc, newName)
		}
	})
	for _, t2 := range g.Types {
		if t2.RID == target.RID {
			t2.Name = newName
		}
	}
	return g
}

func TestRecoverPendingRollbackAndCommit(t *testing.T) {
	prepared := cloneGraph(testGraph())
	prepared.Pending = &pendingRename{RID: "r.user", OldName: "User", NewName: "Person"}
	s, err := NewStore(prepared, slog.Default())
	if err != nil {
		t.Fatalf("rollback recovery: %v", err)
	}
	if t2 := s.Snapshot().Lookup("User"); t2 == nil || t2.RID != "r.user" {
		t.Fatal("expected rollback to old name")
	}

	staged := renameWithoutCommit(cloneGraph(testGraph()), "User", "Person")
	s2, err := NewStore(staged, slog.Default())
	if err != nil {
		t.Fatalf("commit recovery: %v", err)
	}
	snap := s2.Snapshot()
	if snap.Lookup("Person") == nil || snap.Lookup("User") != nil {
		t.Fatal("expected recovered commit to Person")
	}
	if snap.Pending != nil || countRefsTo(snap, "User") != 0 {
		t.Fatal("recovered graph must be clean")
	}
}

func TestConcurrentRenames(t *testing.T) {
	run := func() *Graph {
		s, _ := newTestStore(t, testGraph())
		var workers sync.WaitGroup
		var readers sync.WaitGroup

		for _, rn := range [][2]string{{"User", "Person"}, {"Order", "Purchase"}} {
			workers.Add(1)
			go func(oldName, newName string) {
				defer workers.Done()
				for range 100 {
					if _, err := s.RenameObjectType(oldName, newName); err != nil &&
						!errors.Is(err, ErrTypeNotFound) && !errors.Is(err, ErrNameConflict) {
						t.Errorf("unexpected rename error: %v", err)
						return
					}
				}
			}(rn[0], rn[1])
		}

		stop := make(chan struct{})
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := s.Snapshot()
					known := map[string]bool{}
					for _, ty := range snap.Types {
						known[ty.Name] = true
					}
					walkRefs(snap, func(loc refLocation, ref string) {
						if ref != "" && !known[ref] {
							t.Errorf("dangling reference %s at %s", ref, loc)
						}
					})
					if snap.Pending != nil {
						t.Error("reader observed half-applied pending state")
					}
				}
			}
		}()

		workers.Wait()
		close(stop)
		readers.Wait()
		return s.Snapshot()
	}

	final := run()
	want := map[string]string{"r.user": "Person", "r.order": "Purchase"}
	for _, ty := range final.Types {
		if got := want[ty.RID]; got != ty.Name {
			t.Fatalf("rid %s name = %s, want %s", ty.RID, ty.Name, got)
		}
	}

	// 串行按两种顺序执行同一组重命名，最终状态必须完全一致（并发结果也与此一致）。
	serialize := func(first, second [2]string) *Graph {
		s, _ := newTestStore(t, testGraph())
		if _, err := s.RenameObjectType(first[0], first[1]); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RenameObjectType(second[0], second[1]); err != nil {
			t.Fatal(err)
		}
		return s.Snapshot()
	}
	a := serialize([2]string{"User", "Person"}, [2]string{"Order", "Purchase"})
	b := serialize([2]string{"Order", "Purchase"}, [2]string{"User", "Person"})
	if canonical(a) != canonical(b) {
		t.Fatal("same rename set in different orders produced different states")
	}
	if canonical(final) != canonical(a) {
		t.Fatal("concurrent result differs from serial canonical state")
	}
}

func canonical(g *Graph) string {
	var b strings.Builder
	for _, ty := range g.Types {
		b.WriteString(ty.RID)
		b.WriteByte('=')
		b.WriteString(ty.Name)
		b.WriteByte(';')
	}
	walkRefs(g, func(loc refLocation, ref string) {
		b.WriteString(loc.String())
		b.WriteByte('=')
		b.WriteString(ref)
		b.WriteByte(';')
	})
	return b.String()
}
