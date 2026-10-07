package shallow

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naive is an independent, deliberately simple reference model used to
// cross-check the optimized Repo on random graphs and random operation
// sequences. It recomputes everything from scratch on every operation.
type naive struct {
	remote   *MemRemote
	commits  map[string]Commit
	contents map[string]ContentObject
	boundary map[string]bool
	refs     map[string]string
}

func newNaive(remote *MemRemote) *naive {
	return &naive{
		remote:   remote,
		commits:  map[string]Commit{},
		contents: map[string]ContentObject{},
		boundary: map[string]bool{},
		refs:     map[string]string{},
	}
}

// lookup returns commit metadata without storing it.
func (n *naive) lookup(id string) Commit {
	if c, ok := n.commits[id]; ok {
		return c
	}
	c, err := n.remote.FetchCommit(id)
	if err != nil {
		panic(err)
	}
	return c
}

// fetch pulls a commit and all of its content objects into the local set.
func (n *naive) fetch(id string) {
	if _, ok := n.commits[id]; ok {
		return
	}
	c := n.lookup(id)
	n.commits[id] = c
	for _, oid := range c.Contents {
		if _, ok := n.contents[oid]; ok {
			continue
		}
		o, err := n.remote.FetchContent(oid)
		if err != nil {
			panic(err)
		}
		n.contents[oid] = o
	}
}

func (n *naive) recomputeBoundary() {
	n.boundary = map[string]bool{}
	for id, c := range n.commits {
		for _, p := range c.Parents {
			if _, ok := n.commits[p]; !ok {
				n.boundary[id] = true
				break
			}
		}
	}
}

func (n *naive) minHeldDepth() int {
	best := math.MaxInt
	visited := map[string]bool{}
	type item struct {
		id string
		d  int
	}
	var queue []item
	for _, target := range n.refs {
		queue = append(queue, item{target, 1})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if visited[it.id] {
			continue
		}
		visited[it.id] = true
		c, ok := n.commits[it.id]
		if !ok {
			continue
		}
		if n.boundary[it.id] {
			if it.d < best {
				best = it.d
			}
			continue
		}
		for _, p := range c.Parents {
			queue = append(queue, item{p, it.d + 1})
		}
	}
	return best
}

func (n *naive) deepenDepth(depth int) {
	if depth <= n.minHeldDepth() {
		return
	}
	best := map[string]int{}
	type item struct {
		id string
		d  int
	}
	var queue []item
	for _, target := range n.refs {
		queue = append(queue, item{target, 1})
	}
	var view []string
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.d > depth {
			continue
		}
		if b, ok := best[it.id]; ok && b <= it.d {
			continue
		}
		best[it.id] = it.d
		view = append(view, it.id)
		for _, p := range n.lookup(it.id).Parents {
			queue = append(queue, item{p, it.d + 1})
		}
	}
	for _, id := range view {
		n.fetch(id)
	}
	n.recomputeBoundary()
}

func (n *naive) deepenTime(since int64) {
	seen := map[string]bool{}
	var queue []string
	for _, target := range n.refs {
		queue = append(queue, target)
	}
	var view []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		c := n.lookup(id)
		if c.Time < since {
			continue // stop this path; the commit itself is excluded
		}
		view = append(view, id)
		queue = append(queue, c.Parents...)
	}
	for _, id := range view {
		n.fetch(id)
	}
	n.recomputeBoundary()
}

func (n *naive) unshallow() {
	seen := map[string]bool{}
	queue := make([]string, 0, len(n.commits))
	for id := range n.commits {
		queue = append(queue, id)
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if seen[id] {
			continue
		}
		seen[id] = true
		n.fetch(id)
		queue = append(queue, n.lookup(id).Parents...)
	}
	n.recomputeBoundary()
}

func (n *naive) reachable() (map[string]bool, map[string]bool) {
	rc := map[string]bool{}
	ro := map[string]bool{}
	var stack []string
	for _, target := range n.refs {
		stack = append(stack, target)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if rc[id] {
			continue
		}
		c, ok := n.commits[id]
		if !ok {
			continue
		}
		rc[id] = true
		for _, oid := range c.Contents {
			ro[oid] = true
		}
		if n.boundary[id] {
			continue
		}
		stack = append(stack, c.Parents...)
	}
	return rc, ro
}

func (n *naive) gc() (int, int64) {
	rc, _ := n.reachable()
	keep := map[string]bool{}
	for id := range rc {
		keep[id] = true
	}
	for id := range n.boundary {
		keep[id] = true
	}
	keepO := map[string]bool{}
	for id := range keep {
		for _, oid := range n.commits[id].Contents {
			keepO[oid] = true
		}
	}
	count := 0
	var bytes int64
	for id := range n.commits {
		if !keep[id] {
			delete(n.commits, id)
			count++
		}
	}
	for id, o := range n.contents {
		if !keepO[id] {
			delete(n.contents, id)
			count++
			bytes += o.Size
		}
	}
	return count, bytes
}

// randomRemote builds a random DAG: ci's parents are chosen from higher
// indices (acyclic), times are random in [0,maxTime), contents come from a
// shared pool so objects are referenced by multiple commits.
func randomRemote(rng *rand.Rand, nCommits, nContents int, maxTime int64) (*MemRemote, []string, []string) {
	remote := NewMemRemote()
	contentIDs := make([]string, 0, nContents)
	for i := 0; i < nContents; i++ {
		id := fmt.Sprintf("o%d", i)
		remote.AddContent(ContentObject{ID: id, Size: int64(1 + rng.Intn(100))})
		contentIDs = append(contentIDs, id)
	}
	commitIDs := make([]string, 0, nCommits)
	for i := 0; i < nCommits; i++ {
		id := fmt.Sprintf("c%d", i)
		var parents []string
		for k := 0; k < rng.Intn(3); k++ {
			if i+1 < nCommits {
				p := fmt.Sprintf("c%d", i+1+rng.Intn(nCommits-i-1))
				if !contains(parents, p) {
					parents = append(parents, p)
				}
			}
		}
		var contents []string
		for k := 0; k < rng.Intn(4); k++ {
			oid := contentIDs[rng.Intn(len(contentIDs))]
			if !contains(contents, oid) {
				contents = append(contents, oid)
			}
		}
		remote.AddCommit(Commit{ID: id, Parents: parents, Time: rng.Int63n(maxTime), Contents: contents})
		commitIDs = append(commitIDs, id)
	}
	return remote, commitIDs, contentIDs
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// initialView computes a depth-limited view of the remote from root, plus
// the boundary that view implies.
func initialView(remote *MemRemote, root string, depth int) ([]Commit, []ContentObject, []string) {
	set := map[string]Commit{}
	type item struct {
		id string
		d  int
	}
	queue := []item{{root, 1}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.d > depth {
			continue
		}
		if _, ok := set[it.id]; ok {
			continue
		}
		c, err := remote.FetchCommit(it.id)
		if err != nil {
			panic(err)
		}
		set[it.id] = c
		for _, p := range c.Parents {
			queue = append(queue, item{p, it.d + 1})
		}
	}
	var commits []Commit
	var contents []ContentObject
	seenO := map[string]bool{}
	for _, c := range set {
		commits = append(commits, c)
		for _, oid := range c.Contents {
			if seenO[oid] {
				continue
			}
			seenO[oid] = true
			o, err := remote.FetchContent(oid)
			if err != nil {
				panic(err)
			}
			contents = append(contents, o)
		}
	}
	var boundary []string
	for id := range computeBoundary(set) {
		boundary = append(boundary, id)
	}
	sort.Strings(boundary)
	return commits, contents, boundary
}

// compareState asserts full state equivalence between the Repo and the
// naive model: boundary, held objects, and the reachability of every
// object known to the remote.
func compareState(t *testing.T, repo *Repo, n *naive, commitIDs, contentIDs []string, ctx string) {
	t.Helper()
	wantBoundary := make([]string, 0, len(n.boundary))
	for id := range n.boundary {
		wantBoundary = append(wantBoundary, id)
	}
	sort.Strings(wantBoundary)
	if got := repo.Boundary(); !reflect.DeepEqual(got, wantBoundary) {
		t.Fatalf("%s: boundary repo=%v model=%v", ctx, got, wantBoundary)
	}
	for _, id := range commitIDs {
		_, inModel := n.commits[id]
		if got := repo.HasCommit(id); got != inModel {
			t.Fatalf("%s: HasCommit(%s) repo=%v model=%v", ctx, id, got, inModel)
		}
	}
	for _, id := range contentIDs {
		_, inModel := n.contents[id]
		if got := repo.HasContent(id); got != inModel {
			t.Fatalf("%s: HasContent(%s) repo=%v model=%v", ctx, id, got, inModel)
		}
	}
	rc, ro := n.reachable()
	for _, id := range commitIDs {
		if got := repo.IsReachable(id); got != rc[id] {
			t.Fatalf("%s: IsReachable(%s) repo=%v model=%v", ctx, id, got, rc[id])
		}
	}
	for _, id := range contentIDs {
		if got := repo.IsReachable(id); got != ro[id] {
			t.Fatalf("%s: IsReachable(%s) repo=%v model=%v", ctx, id, got, ro[id])
		}
	}
}

// TestRandomModelComparison drives the Repo and the naive model through
// identical random operation sequences on random remote graphs and
// compares the full state after every step.
func TestRandomModelComparison(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			remote, commitIDs, contentIDs := randomRemote(rng, 40, 25, 50)
			commits, contents, boundary := initialView(remote, "c0", 1+rng.Intn(3))
			refs := map[string]string{"main": "c0"}
			repo, err := NewRepo(remote, commits, contents, boundary, refs)
			if err != nil {
				t.Fatalf("NewRepo: %v", err)
			}
			model := newNaive(remote)
			for _, c := range commits {
				model.commits[c.ID] = c
			}
			for _, o := range contents {
				model.contents[o.ID] = o
			}
			for _, id := range boundary {
				model.boundary[id] = true
			}
			for k, v := range refs {
				model.refs[k] = v
			}
			t.Logf("input: 40-commit random DAG, 25 shared contents, initial view depth from c0, boundary=%v", boundary)

			refNames := []string{"main", "side", "x1", "x2"}
			for step := 0; step < 300; step++ {
				var desc string
				switch rng.Intn(10) {
				case 0, 1, 2:
					d := 1 + rng.Intn(8)
					desc = fmt.Sprintf("DeepenDepth(%d)", d)
					if err := repo.DeepenDepth(d); err != nil {
						t.Fatalf("step %d %s: %v", step, desc, err)
					}
					model.deepenDepth(d)
				case 3, 4:
					ts := rng.Int63n(51)
					desc = fmt.Sprintf("DeepenTime(%d)", ts)
					if err := repo.DeepenTime(ts); err != nil {
						t.Fatalf("step %d %s: %v", step, desc, err)
					}
					model.deepenTime(ts)
				case 5:
					desc = "Unshallow()"
					if err := repo.Unshallow(); err != nil {
						t.Fatalf("step %d %s: %v", step, desc, err)
					}
					model.unshallow()
				case 6:
					desc = "GC()"
					n1, b1, err := repo.GC()
					if err != nil {
						t.Fatalf("step %d %s: %v", step, desc, err)
					}
					n2, b2 := model.gc()
					if n1 != n2 || b1 != b2 {
						t.Fatalf("step %d GC: repo=(%d,%d) model=(%d,%d)", step, n1, b1, n2, b2)
					}
					desc = fmt.Sprintf("GC()=(%d,%d)", n1, b1)
				case 7, 8:
					if len(model.commits) == 0 {
						continue
					}
					local := make([]string, 0, len(model.commits))
					for id := range model.commits {
						local = append(local, id)
					}
					sort.Strings(local)
					name := refNames[rng.Intn(len(refNames))]
					target := local[rng.Intn(len(local))]
					desc = fmt.Sprintf("SetRef(%s,%s)", name, target)
					if err := repo.SetRef(name, target); err != nil {
						t.Fatalf("step %d %s: %v", step, desc, err)
					}
					model.refs[name] = target
				case 9:
					if len(model.refs) == 0 {
						continue
					}
					names := make([]string, 0, len(model.refs))
					for name := range model.refs {
						names = append(names, name)
					}
					sort.Strings(names)
					name := names[rng.Intn(len(names))]
					desc = fmt.Sprintf("DeleteRef(%s)", name)
					if err := repo.DeleteRef(name); err != nil {
						t.Fatalf("step %d %s: %v", step, desc, err)
					}
					delete(model.refs, name)
				}
				ctx := fmt.Sprintf("step %d %s", step, desc)
				compareState(t, repo, model, commitIDs, contentIDs, ctx)
				if step%50 == 0 {
					t.Logf("step %d op=%s -> boundary=%v localCommits=%d; basis: full state equals naive model", step, desc, repo.Boundary(), len(model.commits))
				}
			}
			compareState(t, repo, model, commitIDs, contentIDs, "final")
			t.Logf("output: 300 random ops, final boundary=%v, state identical to naive model at every step; basis: compareState after each op", repo.Boundary())
		})
	}
}

// TestConcurrentDeepenAndGC hammers the Repo with concurrent deepen,
// unshallow, GC, ref writes and reachability reads (run with -race). All
// operations are linearizable, so afterwards the state must satisfy every
// invariant and the reachability index must match a fresh traversal.
func TestConcurrentDeepenAndGC(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	remote, commitIDs, _ := randomRemote(rng, 300, 150, 100)
	commits, contents, boundary := initialView(remote, "c0", 2)
	repo, err := NewRepo(remote, commits, contents, boundary, map[string]string{"main": "c0"})
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	t.Logf("input: 300-commit random DAG; 3 deepen + 2 GC + 2 ref + 2 reader goroutines x 25 iterations")

	var wg sync.WaitGroup
	errs := make(chan error, 256)
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 25; i++ {
				var err error
				switch r.Intn(3) {
				case 0:
					err = repo.DeepenDepth(1 + r.Intn(10))
				case 1:
					err = repo.DeepenTime(r.Int63n(101))
				default:
					err = repo.Unshallow()
				}
				if err != nil {
					errs <- fmt.Errorf("deepen: %w", err)
				}
			}
		}(int64(w*7 + 1))
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, _, err := repo.GC(); err != nil {
					errs <- fmt.Errorf("gc: %w", err)
				}
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 25; i++ {
				name := fmt.Sprintf("ref%d", r.Intn(4))
				target := commitIDs[r.Intn(len(commitIDs))]
				// Target may not be local; both outcomes are legal.
				_ = repo.SetRef(name, target)
				if r.Intn(3) == 0 {
					_ = repo.DeleteRef(name)
				}
			}
		}(int64(w*13 + 100))
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 50; i++ {
				_ = repo.IsReachable(commitIDs[r.Intn(len(commitIDs))])
			}
		}(int64(w*17 + 200))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent op failed: %v", err)
	}

	// Serial-equivalence checks on the final state.
	if err := repo.checkRefsLocked(); err != nil {
		t.Fatalf("final state has dangling refs: %v", err)
	}
	if err := repo.checkStateLocked(); err != nil {
		t.Fatalf("final state invalid: %v", err)
	}
	roots := make([]string, 0, len(repo.refs))
	for _, target := range repo.refs {
		roots = append(roots, target)
	}
	rc, ro := reachableFromRefs(repo.commits, repo.boundary, roots)
	if !reflect.DeepEqual(rc, repo.reachC) || !reflect.DeepEqual(ro, repo.reachO) {
		t.Fatalf("reachability index diverged from a fresh traversal")
	}
	n1, b1, err := repo.GC()
	if err != nil {
		t.Fatalf("GC after concurrency: %v", err)
	}
	n2, b2, err := repo.GC()
	if err != nil {
		t.Fatalf("GC#2 after concurrency: %v", err)
	}
	t.Logf("output: final state valid, reachability index consistent, GC=(%d,%d) then (%d,%d); basis: all ops linearizable on one mutex, invariants hold for some serial order", n1, b1, n2, b2)
	if n2 != 0 || b2 != 0 {
		t.Fatalf("GC not idempotent after concurrency: (%d,%d)", n2, b2)
	}
}
