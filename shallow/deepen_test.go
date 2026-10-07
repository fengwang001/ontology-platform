package shallow

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// TestDeepenDepthNoOp: deepening to a depth already held by every ref is a
// side-effect-free success (no seq bump, no boundary change, no remote
// calls).
func TestDeepenDepthNoOp(t *testing.T) {
	remote := linearRemote(5, 100)
	repo := mustRepo(t, remote, []string{"c1", "c2", "c3"}, []string{"c3"}, map[string]string{"main": "c1"})
	seq := repo.Seq()
	remote.ResetCounters()

	for _, d := range []int{1, 2, 3} {
		if err := repo.DeepenDepth(d); err != nil {
			t.Fatalf("DeepenDepth(%d): %v", d, err)
		}
	}
	calls := remote.CommitCalls.Load() + remote.ContentCalls.Load()
	t.Logf("input: DeepenDepth(1..3) with held depth 3; output: err=nil, remote calls=%d, seq %d->%d, boundary=%v; basis: target depth not greater than held depth is a no-op", calls, seq, repo.Seq(), repo.Boundary())
	if calls != 0 {
		t.Fatalf("no-op deepen made %d remote calls", calls)
	}
	if repo.Seq() != seq {
		t.Fatalf("seq bumped by no-op deepen")
	}
	if !reflect.DeepEqual(repo.Boundary(), []string{"c3"}) {
		t.Fatalf("boundary changed: %v", repo.Boundary())
	}
}

// TestDeepenTimeIndependentPaths: commit times are not monotonic; each
// path stops independently at its first commit older than the cutoff.
//
//	A(t=100) -> B(t=10) -> D(t=80)
//	         -> C(t=90) -> D, E(t=95) -> F(t=5)
//
// DeepenTime(50): B stops its path (excluded), but D is still included via
// C; F stops its path (excluded).
func TestDeepenTimeIndependentPaths(t *testing.T) {
	remote := NewMemRemote()
	add := func(id string, time int64, parents ...string) {
		remote.AddCommit(Commit{ID: id, Parents: parents, Time: time, Contents: []string{"o" + id}})
		remote.AddContent(ContentObject{ID: "o" + id, Size: 5})
	}
	add("A", 100, "B", "C")
	add("B", 10, "D")
	add("C", 90, "D", "E")
	add("D", 80)
	add("E", 95, "F")
	add("F", 5)
	repo := mustRepo(t, remote, []string{"A"}, []string{"A"}, map[string]string{"main": "A"})

	if err := repo.DeepenTime(50); err != nil {
		t.Fatalf("DeepenTime(50): %v", err)
	}
	gotBoundary := repo.Boundary()
	t.Logf("input: DeepenTime(50) on non-monotonic graph; output: boundary=%v", gotBoundary)
	// View = {A,C,D,E}. A misses parent B -> boundary; E misses parent F -> boundary.
	if !reflect.DeepEqual(gotBoundary, []string{"A", "E"}) {
		t.Fatalf("boundary=%v, want [A E]", gotBoundary)
	}
	for _, id := range []string{"C", "D", "E"} {
		if !repo.HasCommit(id) {
			t.Fatalf("%s should have been fetched", id)
		}
	}
	for _, id := range []string{"B", "F"} {
		if repo.HasCommit(id) {
			t.Fatalf("%s is older than cutoff on every path and must be excluded", id)
		}
	}
	t.Logf("output: fetched={C,D,E}, excluded={B,F}; basis: path via B stopped at B(t=10), but D(t=80) still entered via C(t=90); F(t=5) excluded")
	// A is boundary, so reachability from main stops at A.
	if !repo.IsReachable("A") || repo.IsReachable("C") {
		t.Fatalf("reachability after time deepen wrong: A reachable, C hidden behind boundary A")
	}
	t.Logf("output: reachable={A,oA}; basis: A is boundary (parent B missing) so traversal stops at A")
}

// TestUnshallowThenDeepenNoOp: after unshallow the boundary is empty and
// any deepen is a side-effect-free success.
func TestUnshallowThenDeepenNoOp(t *testing.T) {
	remote := linearRemote(5, 100)
	repo := mustRepo(t, remote, []string{"c1", "c2"}, []string{"c2"}, map[string]string{"main": "c1"})
	if err := repo.Unshallow(); err != nil {
		t.Fatalf("Unshallow: %v", err)
	}
	if got := repo.Boundary(); len(got) != 0 {
		t.Fatalf("boundary after unshallow = %v, want empty", got)
	}
	for i := 1; i <= 5; i++ {
		if !repo.HasCommit(fmt.Sprintf("c%d", i)) || !repo.HasContent(fmt.Sprintf("o%d", i)) {
			t.Fatalf("c%d/o%d missing after unshallow", i, i)
		}
	}
	t.Logf("output: Unshallow -> boundary={}, all 5 commits + contents local; basis: full ancestor closure fetched")

	seq := repo.Seq()
	remote.ResetCounters()
	if err := repo.DeepenDepth(2); err != nil {
		t.Fatalf("DeepenDepth after unshallow: %v", err)
	}
	if err := repo.DeepenTime(0); err != nil {
		t.Fatalf("DeepenTime after unshallow: %v", err)
	}
	if err := repo.DeepenTime(1 << 40); err != nil {
		t.Fatalf("DeepenTime(far future) after unshallow: %v", err)
	}
	calls := remote.CommitCalls.Load() + remote.ContentCalls.Load()
	t.Logf("input: DeepenDepth(2), DeepenTime(0), DeepenTime(2^40) after unshallow; output: err=nil, remote calls=%d, seq %d->%d; basis: boundary can only advance, nothing left to fetch", calls, seq, repo.Seq())
	if calls != 0 || repo.Seq() != seq {
		t.Fatalf("deepen after unshallow had side effects: calls=%d seq %d->%d", calls, seq, repo.Seq())
	}
}

// TestFetchFailureRollback: a failing remote (down, or failing mid-fetch)
// rolls the whole deepen back: boundary, objects, refs and seq unchanged.
func TestFetchFailureRollback(t *testing.T) {
	newRepo := func(remote *MemRemote) *Repo {
		return mustRepo(t, remote, []string{"c1", "c2"}, []string{"c2"}, map[string]string{"main": "c1"})
	}
	snapshot := func(repo *Repo) string {
		return fmt.Sprintf("seq=%d boundary=%v refs=%v", repo.Seq(), repo.Boundary(), repo.Refs())
	}

	// Scenario 1: content fetch fails mid-deepen (commit metadata fetched).
	remote := linearRemote(4, 100)
	repo := newRepo(remote)
	before := snapshot(repo)
	remote.FailContents = true
	err := repo.DeepenDepth(4)
	t.Logf("input: DeepenDepth(4) with content fetches failing; output: %v; basis: mid-fetch failure -> ErrFetchFailed", err)
	if !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("want ErrFetchFailed, got %v", err)
	}
	if errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("fetch failure must be distinguishable from object-not-found")
	}
	if after := snapshot(repo); after != before {
		t.Fatalf("state changed despite rollback: %s -> %s", before, after)
	}
	if repo.HasCommit("c3") || repo.HasContent("o3") {
		t.Fatalf("rolled-back deepen leaked objects")
	}
	t.Logf("output: state after rollback: %s; basis: staging discarded, nothing merged", snapshot(repo))

	// Scenario 2: remote completely down.
	remote2 := linearRemote(4, 100)
	repo2 := newRepo(remote2)
	before2 := snapshot(repo2)
	remote2.Down = true
	err = repo2.Unshallow()
	t.Logf("input: Unshallow with remote down; output: %v; basis: remote unavailable -> ErrFetchFailed, full rollback", err)
	if !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("want ErrFetchFailed, got %v", err)
	}
	if after := snapshot(repo2); after != before2 {
		t.Fatalf("state changed despite rollback: %s -> %s", before2, after)
	}

	// Scenario 3: remote object missing is a different, distinguishable error.
	remote3 := NewMemRemote()
	remote3.AddCommit(Commit{ID: "c1", Parents: []string{"c2"}, Time: 2, Contents: []string{"o1"}})
	remote3.AddContent(ContentObject{ID: "o1", Size: 10})
	// c2 does not exist in this remote.
	repo3 := mustRepo(t, remote3, []string{"c1"}, []string{"c1"}, map[string]string{"main": "c1"})
	err = repo3.DeepenDepth(2)
	t.Logf("input: DeepenDepth(2), remote lacks c2; output: %v; basis: missing remote object -> ErrObjectNotFound", err)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("want ErrObjectNotFound, got %v", err)
	}
	if errors.Is(err, ErrFetchFailed) {
		t.Fatalf("object-not-found must not be reported as fetch failure")
	}
}

// TestDeepenCostIndependentOfUnreachableRemote proves that deepen cost does
// not grow with the unreachable part of the remote graph: the number of
// remote fetches equals the number of missing in-view objects, no matter
// how large the unreachable remote side is.
func TestDeepenCostIndependentOfUnreachableRemote(t *testing.T) {
	build := func(unreachable int) *MemRemote {
		remote := linearRemote(10, 100)
		for i := 0; i < unreachable; i++ {
			id := fmt.Sprintf("u%d", i)
			remote.AddCommit(Commit{ID: id, Time: 1, Contents: []string{"uo" + id}})
			remote.AddContent(ContentObject{ID: "uo" + id, Size: 1})
		}
		return remote
	}
	var callsSmall, callsLarge int64
	for _, unreachable := range []int{10, 5000} {
		remote := build(unreachable)
		repo := mustRepo(t, remote, []string{"c1"}, []string{"c1"}, map[string]string{"main": "c1"})
		remote.ResetCounters()
		if err := repo.DeepenDepth(4); err != nil {
			t.Fatalf("DeepenDepth(4): %v", err)
		}
		calls := remote.CommitCalls.Load() + remote.ContentCalls.Load()
		t.Logf("input: DeepenDepth(4), remote has %d unreachable commits; output: %d remote fetches; basis: only in-view missing objects (c2..c4,o2..o4) are fetched", unreachable, calls)
		if calls != 6 {
			t.Fatalf("unreachable=%d: %d fetches, want exactly 6", unreachable, calls)
		}
		if unreachable == 10 {
			callsSmall = calls
		} else {
			callsLarge = calls
		}
	}
	if callsSmall != callsLarge {
		t.Fatalf("deepen cost grew with unreachable remote size: %d vs %d", callsSmall, callsLarge)
	}
	t.Logf("output: fetches(%d unreachable)=%d == fetches(%d unreachable)=%d; basis: BFS visits only the deepened view", 10, callsSmall, 5000, callsLarge)
}

// TestReachableQueryDoesNotScan: point reachability queries never touch the
// remote and never traverse; the maintained index makes them O(1).
func TestReachableQueryDoesNotScan(t *testing.T) {
	remote := linearRemote(1000, 0)
	ids := make([]string, 0, 500)
	for i := 1; i <= 500; i++ {
		ids = append(ids, fmt.Sprintf("c%d", i))
	}
	repo := mustRepo(t, remote, ids, []string{"c500"}, map[string]string{"main": "c1"})
	remote.ResetCounters()
	for _, q := range []string{"c1", "c250", "c500", "o500", "c501", "nope"} {
		_ = repo.IsReachable(q)
	}
	calls := remote.CommitCalls.Load() + remote.ContentCalls.Load()
	t.Logf("input: 6 IsReachable queries on 500-commit repo; output: %d remote calls; basis: queries are pure index lookups (see BenchmarkIsReachable for size independence)", calls)
	if calls != 0 {
		t.Fatalf("reachability query hit the remote %d times", calls)
	}
}

// BenchmarkIsReachable demonstrates that point-query cost does not depend
// on the number of local objects: run with -bench=.
func BenchmarkIsReachable(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("commits=%d", n), func(b *testing.B) {
			remote := linearRemote(n, 0)
			ids := make([]string, 0, n)
			for i := 1; i <= n; i++ {
				ids = append(ids, fmt.Sprintf("c%d", i))
			}
			commits := make([]Commit, 0, n)
			var contents []ContentObject
			for _, id := range ids {
				c, _ := remote.FetchCommit(id)
				commits = append(commits, c)
				o, _ := remote.FetchContent(c.Contents[0])
				contents = append(contents, o)
			}
			repo, err := NewRepo(remote, commits, contents, []string{ids[n-1]}, map[string]string{"main": "c1"})
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = repo.IsReachable("c42")
			}
		})
	}
}

// BenchmarkDeepenDepth shows deepen cost tracks the deepened view, not the
// remote graph size (the remote holds a large unreachable side graph).
func BenchmarkDeepenDepth(b *testing.B) {
	remote := linearRemote(64, 0)
	for i := 0; i < 20000; i++ {
		id := fmt.Sprintf("u%d", i)
		remote.AddCommit(Commit{ID: id, Time: 1})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		c, _ := remote.FetchCommit("c1")
		o, _ := remote.FetchContent("o1")
		repo, err := NewRepo(remote, []Commit{c}, []ContentObject{o}, []string{"c1"}, map[string]string{"main": "c1"})
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := repo.DeepenDepth(8); err != nil {
			b.Fatal(err)
		}
	}
}
