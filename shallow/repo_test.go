package shallow

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// linearRemote builds commits c1..cn where ci's parent is c(i+1) and cn is
// the root. Commit ci has time base+i and references content oi (size 10*i).
func linearRemote(n int, base int64) *MemRemote {
	r := NewMemRemote()
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("c%d", i)
		var parents []string
		if i < n {
			parents = []string{fmt.Sprintf("c%d", i+1)}
		}
		r.AddCommit(Commit{ID: id, Parents: parents, Time: base + int64(i), Contents: []string{fmt.Sprintf("o%d", i)}})
		r.AddContent(ContentObject{ID: fmt.Sprintf("o%d", i), Size: int64(10 * i)})
	}
	return r
}

// commitsOf returns the named commits from the remote, in order.
func commitsOf(t *testing.T, r *MemRemote, ids ...string) []Commit {
	t.Helper()
	out := make([]Commit, 0, len(ids))
	for _, id := range ids {
		c, err := r.FetchCommit(id)
		if err != nil {
			t.Fatalf("commitsOf: %v", err)
		}
		out = append(out, c)
	}
	return out
}

// contentsFor returns the deduplicated content objects referenced by commits.
func contentsFor(t *testing.T, r *MemRemote, commits []Commit) []ContentObject {
	t.Helper()
	seen := map[string]bool{}
	var out []ContentObject
	for _, c := range commits {
		for _, oid := range c.Contents {
			if seen[oid] {
				continue
			}
			seen[oid] = true
			o, err := r.FetchContent(oid)
			if err != nil {
				t.Fatalf("contentsFor: %v", err)
			}
			out = append(out, o)
		}
	}
	return out
}

func mustRepo(t *testing.T, r *MemRemote, commitIDs []string, boundary []string, refs map[string]string) *Repo {
	t.Helper()
	commits := commitsOf(t, r, commitIDs...)
	repo, err := NewRepo(r, commits, contentsFor(t, r, commits), boundary, refs)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return repo
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestLoadRejectsInvalidState: states violating the invariants must be
// rejected at load time.
func TestLoadRejectsInvalidState(t *testing.T) {
	remote := linearRemote(3, 100)
	c12 := commitsOf(t, remote, "c1", "c2")
	contents := contentsFor(t, remote, c12)

	// Case 1: boundary commit not held locally.
	_, err := NewRepo(remote, c12, contents, []string{"c3"}, map[string]string{"main": "c1"})
	t.Logf("input: boundary={c3} but local commits={c1,c2}; output: err=%v; basis: boundary commits must exist locally -> ErrInvalidState", err)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("case boundary-not-local: want ErrInvalidState, got %v", err)
	}

	// Case 2: non-boundary commit missing a parent.
	_, err = NewRepo(remote, c12, contents, nil, map[string]string{"main": "c1"})
	t.Logf("input: c2 non-boundary but parent c3 not local; output: err=%v; basis: non-boundary commits must have all parents local -> ErrInvalidState", err)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("case missing-parent: want ErrInvalidState, got %v", err)
	}

	// Case 3: ref points at a commit that is not local.
	_, err = NewRepo(remote, c12, contents, []string{"c2"}, map[string]string{"main": "c3"})
	t.Logf("input: ref main->c3 but c3 not local; output: err=%v; basis: refs must resolve to local commits -> ErrRefNotFound", err)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("case dangling-ref: want ErrRefNotFound, got %v", err)
	}

	// Case 4: valid shallow state loads fine.
	if _, err = NewRepo(remote, c12, contents, []string{"c2"}, map[string]string{"main": "c1"}); err != nil {
		t.Fatalf("case valid: unexpected error %v", err)
	}
	t.Logf("input: boundary={c2}, refs main->c1; output: err=nil; basis: c2 boundary so its missing parent c3 is tolerated")
}

// TestBoundaryLinearGraph checks boundary determination while deepening a
// linear chain step by step.
func TestBoundaryLinearGraph(t *testing.T) {
	remote := linearRemote(5, 100)
	repo := mustRepo(t, remote, []string{"c1", "c2", "c3"}, []string{"c3"}, map[string]string{"main": "c1"})
	t.Logf("input: linear c1->..->c5, local={c1,c2,c3}, boundary={c3}, ref main->c1")

	if got := repo.Boundary(); !reflect.DeepEqual(got, []string{"c3"}) {
		t.Fatalf("initial boundary: got %v", got)
	}
	for _, id := range []string{"c1", "c2", "c3", "o1", "o2", "o3"} {
		if !repo.IsReachable(id) {
			t.Fatalf("%s should be reachable", id)
		}
	}
	t.Logf("output: reachable={c1,c2,c3,o1,o2,o3}; basis: walk from main stops (inclusively) at boundary c3")

	if err := repo.DeepenDepth(4); err != nil {
		t.Fatalf("DeepenDepth(4): %v", err)
	}
	if got := repo.Boundary(); !reflect.DeepEqual(got, []string{"c4"}) {
		t.Fatalf("after deepen(4): boundary=%v, want [c4]", got)
	}
	t.Logf("output: DeepenDepth(4) -> boundary={c4}; basis: c4 is the only local commit with a parent (c5) outside the view")

	if err := repo.DeepenDepth(5); err != nil {
		t.Fatalf("DeepenDepth(5): %v", err)
	}
	if got := repo.Boundary(); len(got) != 0 {
		t.Fatalf("after deepen(5): boundary=%v, want empty (c5 is root)", got)
	}
	t.Logf("output: DeepenDepth(5) -> boundary={}; basis: c5 is the root, no missing parents remain")
}

// TestBoundaryForkMergeGraph checks boundary determination on a
// fork/merge (diamond) graph.
// A -> {B, C}; B -> D; C -> E; D -> F; E -> F; F root.
func TestBoundaryForkMergeGraph(t *testing.T) {
	remote := NewMemRemote()
	add := func(id string, time int64, parents ...string) {
		remote.AddCommit(Commit{ID: id, Parents: parents, Time: time, Contents: []string{"o" + id}})
		remote.AddContent(ContentObject{ID: "o" + id, Size: 7})
	}
	add("A", 60, "B", "C")
	add("B", 50, "D")
	add("C", 40, "E")
	add("D", 30, "F")
	add("E", 20, "F")
	add("F", 10)
	repo := mustRepo(t, remote, []string{"A", "B", "C"}, []string{"B", "C"}, map[string]string{"tip": "A"})
	t.Logf("input: diamond A->{B,C}, B->D, C->E, {D,E}->F; local={A,B,C}, boundary={B,C}, ref tip->A")

	if err := repo.DeepenDepth(3); err != nil {
		t.Fatalf("DeepenDepth(3): %v", err)
	}
	if got := repo.Boundary(); !reflect.DeepEqual(got, []string{"D", "E"}) {
		t.Fatalf("after deepen(3): boundary=%v, want [D E]", got)
	}
	t.Logf("output: DeepenDepth(3) -> boundary={D,E}; basis: depth 3 view={A,B,C,D,E}, D and E each miss parent F")

	if err := repo.DeepenDepth(4); err != nil {
		t.Fatalf("DeepenDepth(4): %v", err)
	}
	if got := repo.Boundary(); len(got) != 0 {
		t.Fatalf("after deepen(4): boundary=%v, want empty (F is root)", got)
	}
	t.Logf("output: DeepenDepth(4) -> boundary={}; basis: merged commit F fetched once and is the root")

	seq := repo.Seq()
	if err := repo.DeepenDepth(5); err != nil {
		t.Fatalf("DeepenDepth(5): %v", err)
	}
	if got := repo.Boundary(); len(got) != 0 {
		t.Fatalf("after deepen(5): boundary=%v, want empty", got)
	}
	if repo.Seq() != seq {
		t.Fatalf("deepen past full history must be side-effect free: seq %d -> %d", seq, repo.Seq())
	}
	t.Logf("output: DeepenDepth(5) -> boundary={}, seq unchanged at %d; basis: fully held history, deepening is a no-op", seq)
}

// TestBoundaryParentHeldButTreatedAbsent: a boundary commit's parents may
// happen to be held locally, yet they are treated as absent for
// reachability, while GC still protects the boundary commit itself.
func TestBoundaryParentHeldButTreatedAbsent(t *testing.T) {
	remote := linearRemote(4, 100)
	// c2 is boundary; c3, c4 are held "by accident" but invisible.
	repo := mustRepo(t, remote, []string{"c1", "c2", "c3", "c4"}, []string{"c2"}, map[string]string{"main": "c1"})
	t.Logf("input: local={c1..c4}, boundary={c2}, ref main->c1; c3,c4 held by accident behind boundary c2")

	for _, id := range []string{"c1", "c2", "o1", "o2"} {
		if !repo.IsReachable(id) {
			t.Fatalf("%s should be reachable", id)
		}
	}
	for _, id := range []string{"c3", "c4", "o3", "o4"} {
		if repo.IsReachable(id) {
			t.Fatalf("%s should be unreachable (behind boundary c2)", id)
		}
	}
	t.Logf("output: reachable={c1,c2,o1,o2}; basis: traversal stops at boundary c2 even though c3,c4 are local")

	n, bytes, err := repo.GC()
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	t.Logf("output: GC deleted count=%d bytes=%d; basis: c3,c4,o3,o4 unreachable and not boundary-protected", n, bytes)
	if n != 4 || bytes != 70 {
		t.Fatalf("GC = (%d,%d), want (4,70)", n, bytes)
	}
	if !repo.HasCommit("c2") {
		t.Fatalf("boundary commit c2 must survive GC")
	}
	if got := repo.Boundary(); !reflect.DeepEqual(got, []string{"c2"}) {
		t.Fatalf("boundary after GC = %v, want [c2]", got)
	}
}

// TestGCIdempotentAndBoundaryProtection: GC deletes exactly the unreachable
// objects, never boundary commits, and a second GC is a no-op.
func TestGCIdempotentAndBoundaryProtection(t *testing.T) {
	remote := linearRemote(3, 100)
	// c3 is boundary and unreachable from any ref; it must be protected.
	repo := mustRepo(t, remote, []string{"c1", "c2", "c3"}, []string{"c1", "c3"}, map[string]string{"main": "c1"})
	t.Logf("input: local={c1,c2,c3}, boundary={c1,c3}, ref main->c1 (c1 boundary hides c2/c3; c3 boundary but unreachable)")

	n, bytes, err := repo.GC()
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	// c1,o1 reachable; c2 unreachable -> deleted with o2; c3 boundary -> kept with o3.
	t.Logf("output: GC count=%d bytes=%d; basis: deleted={c2,o2}; c3 kept (boundary), o3 kept (referenced by boundary c3)", n, bytes)
	if n != 2 || bytes != 20 {
		t.Fatalf("GC = (%d,%d), want (2,20)", n, bytes)
	}
	if !repo.HasCommit("c3") || !repo.HasContent("o3") {
		t.Fatalf("boundary commit c3 and its content o3 must survive GC")
	}
	n2, bytes2, err := repo.GC()
	if err != nil {
		t.Fatalf("GC#2: %v", err)
	}
	t.Logf("output: second GC count=%d bytes=%d; basis: GC is idempotent", n2, bytes2)
	if n2 != 0 || bytes2 != 0 {
		t.Fatalf("second GC = (%d,%d), want (0,0)", n2, bytes2)
	}
}

// TestErrorPrecedenceAdjacentPairs checks the priority of every adjacent
// pair in the error order: invalid param < ref not found < remote object
// not found < fetch failure < invalid local state.
func TestErrorPrecedenceAdjacentPairs(t *testing.T) {
	// Pair 1: invalid param beats ref not found.
	remote := linearRemote(3, 100)
	repo := newRepoUnchecked(remote, nil, nil, nil, map[string]string{"main": "ghost"})
	err := repo.DeepenDepth(0)
	t.Logf("input: DeepenDepth(0) with dangling ref main->ghost; output: %v; basis: param check precedes ref resolution", err)
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("pair1: want ErrInvalidParam, got %v", err)
	}
	if _, err = repo.IsReachableFrom("", "x"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("pair1 (empty ref name): want ErrInvalidParam, got %v", err)
	}
	t.Logf("input: IsReachableFrom(\"\", x); output: %v; basis: empty ref name is an invalid param", err)

	// Pair 2: ref not found beats remote object not found.
	repo = newRepoUnchecked(remote, nil, nil, nil, map[string]string{"main": "ghost"})
	err = repo.DeepenDepth(5)
	t.Logf("input: DeepenDepth(5), ref main->ghost (not local), remote would miss objects too; output: %v; basis: refs resolve before any remote access", err)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("pair2: want ErrRefNotFound, got %v", err)
	}

	// Pair 3: remote object not found beats fetch failure.
	// Remote holds c1,c2 but not c3, and content fetches are broken.
	remote2 := NewMemRemote()
	remote2.AddCommit(Commit{ID: "c1", Parents: []string{"c2"}, Time: 3, Contents: []string{"o1"}})
	remote2.AddCommit(Commit{ID: "c2", Parents: []string{"c3"}, Time: 2, Contents: []string{"o2"}})
	remote2.AddContent(ContentObject{ID: "o1", Size: 10})
	remote2.AddContent(ContentObject{ID: "o2", Size: 20})
	repo = mustRepo(t, remote2, []string{"c1"}, []string{"c1"}, map[string]string{"main": "c1"})
	remote2.FailContents = true
	err = repo.DeepenDepth(3)
	t.Logf("input: DeepenDepth(3), remote missing commit c3 AND content fetches fail; output: %v; basis: commit metadata phase precedes content phase", err)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("pair3: want ErrObjectNotFound, got %v", err)
	}
	if errors.Is(err, ErrFetchFailed) {
		t.Fatalf("pair3: ErrObjectNotFound must not be masked as fetch failure")
	}

	// Pair 4: fetch failure beats invalid local state.
	remote3 := linearRemote(3, 100)
	bad := commitsOf(t, remote3, "c1", "c2") // c2 non-boundary, parent c3 missing -> invalid
	repo = newRepoUnchecked(remote3, bad, contentsFor(t, remote3, bad), []string{"c1"}, map[string]string{"main": "c1"})
	remote3.Down = true
	err = repo.DeepenDepth(3)
	t.Logf("input: DeepenDepth(3), remote down AND local state invalid; output: %v; basis: fetch happens before final state validation", err)
	if !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("pair4: want ErrFetchFailed, got %v", err)
	}
	if errors.Is(err, ErrInvalidState) {
		t.Fatalf("pair4: fetch failure must be reported before invalid state")
	}

	// And invalid state is reported when nothing earlier fails.
	remote4 := linearRemote(3, 100)
	bad = commitsOf(t, remote4, "c1", "c2")
	repo = newRepoUnchecked(remote4, bad, contentsFor(t, remote4, bad), []string{"c1"}, map[string]string{"main": "c1"})
	err = repo.DeepenDepth(2)
	t.Logf("input: DeepenDepth(2), remote healthy but c2 non-boundary misses parent c3; output: %v; basis: state validation is the last check", err)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("tail: want ErrInvalidState, got %v", err)
	}
}

// TestRejectedOpsKeepStateAndSeq: rejected operations must not change the
// boundary, object sets, refs, or the sequence number.
func TestRejectedOpsKeepStateAndSeq(t *testing.T) {
	remote := linearRemote(4, 100)
	repo := mustRepo(t, remote, []string{"c1", "c2"}, []string{"c2"}, map[string]string{"main": "c1"})
	seq := repo.Seq()
	boundary := repo.Boundary()
	refs := repo.Refs()

	remote.FailContents = true
	ops := []func() error{
		func() error { return repo.DeepenDepth(0) },    // invalid param
		func() error { return repo.DeepenTime(-1) },    // invalid param
		func() error { return repo.DeleteRef("") },     // invalid param
		func() error { return repo.DeleteRef("no") },   // ref not found
		func() error { return repo.DeepenDepth(4) },    // fetch failure (contents)
		func() error { return repo.Unshallow() },       // fetch failure
		func() error { return repo.SetRef("x", "c9") }, // target not local
	}
	for i, op := range ops {
		if err := op(); err == nil {
			t.Fatalf("op %d should have been rejected", i)
		}
	}
	if repo.Seq() != seq {
		t.Fatalf("seq changed by rejected ops: %d -> %d", seq, repo.Seq())
	}
	if !reflect.DeepEqual(repo.Boundary(), boundary) {
		t.Fatalf("boundary changed: %v -> %v", boundary, repo.Boundary())
	}
	if !reflect.DeepEqual(repo.Refs(), refs) {
		t.Fatalf("refs changed: %v -> %v", refs, repo.Refs())
	}
	if repo.HasCommit("c3") || repo.HasCommit("c4") || repo.HasContent("o3") {
		t.Fatalf("rejected deepen leaked fetched objects into the repo")
	}
	t.Logf("input: 7 rejected ops (bad param/missing ref/fetch failure/bad target); output: seq=%d boundary=%v refs=%v unchanged, no leaked objects; basis: rollback + no seq bump on rejection", repo.Seq(), repo.Boundary(), repo.Refs())
}

// TestIsReachableFrom checks per-ref reachability and its errors.
func TestIsReachableFrom(t *testing.T) {
	remote := linearRemote(3, 100)
	repo := mustRepo(t, remote, []string{"c1", "c2"}, []string{"c2"}, map[string]string{"main": "c1", "side": "c2"})
	ok, err := repo.IsReachableFrom("main", "c2")
	t.Logf("input: IsReachableFrom(main, c2); output: %v, %v; basis: c2 is main's boundary child", ok, err)
	if err != nil || !ok {
		t.Fatalf("want (true,nil), got (%v,%v)", ok, err)
	}
	ok, err = repo.IsReachableFrom("side", "c1")
	t.Logf("input: IsReachableFrom(side, c1); output: %v, %v; basis: c1 is a child of side's root, not reachable from it", ok, err)
	if err != nil || ok {
		t.Fatalf("want (false,nil), got (%v,%v)", ok, err)
	}
	if _, err = repo.IsReachableFrom("nope", "c1"); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("want ErrRefNotFound, got %v", err)
	}
	t.Logf("input: IsReachableFrom(nope, c1); output: %v; basis: unknown ref name", err)
}
