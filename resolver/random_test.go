package resolver

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveResolver is an independent, cache-free simulation of the
// specification used as the differential oracle.
type naiveResolver struct {
	d         int
	instances []instance
}

func (n *naiveResolver) add(trait string, head Type, ctx []Constraint) (int, error) {
	if err := validateInstance(trait, head, ctx); err != nil {
		return 0, err
	}
	if findDuplicate(n.instances, trait, head) {
		return 0, rejectf(RejectDuplicateInstance, "duplicate head %s for trait %q", head, trait)
	}
	if len(n.instances) >= maxInstances {
		return 0, rejectf(RejectTooManyInstances, "at most %d instances", maxInstances)
	}
	n.instances = append(n.instances, instance{id: len(n.instances) + 1, trait: trait, head: head, context: ctx})
	return len(n.instances), nil
}

func (n *naiveResolver) resolve(trait string, typ Type) (Result, error) {
	if !validName(trait) {
		return Result{}, rejectf(RejectInvalidParam, "bad trait name")
	}
	if !typ.valid() || !typ.IsGround() || typ.Depth() > maxTypeDepth {
		return Result{}, rejectf(RejectInvalidParam, "bad type")
	}
	tree, failure := n.solve(goalOf(trait, typ), 1, nil)
	if failure != nil {
		return Result{Category: failure.Category, Failure: failure}, nil
	}
	return Result{Category: Success, Tree: tree}, nil
}

func (n *naiveResolver) solve(g goal, level int, path []goalKey) (*Tree, *Failure) {
	for _, anc := range path {
		if anc == g.key {
			return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: Cycle}
		}
	}
	if level > n.d {
		return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: DepthExceeded}
	}
	var cands []candidate
	for _, inst := range n.instances {
		if inst.trait != g.trait {
			continue
		}
		subst := map[int]Type{}
		if matchPattern(inst.head, g.typ, subst) {
			cands = append(cands, candidate{inst: inst, subst: subst})
		}
	}
	if len(cands) == 0 {
		return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: NoInstance}
	}
	sel := -1
	for i := range cands {
		most := true
		for j := range cands {
			if i != j && !moreSpec(cands[i].inst, cands[j].inst) {
				most = false
				break
			}
		}
		if most {
			sel = i
			break
		}
	}
	if sel < 0 {
		return nil, &Failure{Trait: g.trait, TypeText: g.typ.String(), Category: Ambiguous}
	}
	c := cands[sel]
	childPath := append(append([]goalKey{}, path...), g.key)
	var children []*Tree
	maxChild := 0
	for _, con := range c.inst.context {
		child := goalOf(con.Trait, applySubst(con.Type, c.subst))
		childTree, failure := n.solve(child, level+1, childPath)
		if failure != nil {
			return nil, failure
		}
		children = append(children, childTree)
		if childTree.Height > maxChild {
			maxChild = childTree.Height
		}
	}
	return &Tree{
		Trait:    g.trait,
		TypeText: g.typ.String(),
		Instance: c.inst.id,
		Height:   maxChild + 1,
		Children: children,
	}, nil
}

type conSpec struct {
	name  string
	arity int
}

var consPool = []conSpec{
	{"Int", 0},
	{"Char", 0},
	{"Bool", 0},
	{"Float", 0},
	{"List", 1},
	{"Maybe", 1},
	{"Pair", 2},
	{"Trio", 3},
}

var traitPool = []string{"T0", "T1", "T2"}

// randType builds a random type of bounded depth. If vars is non-empty,
// variables from it may appear (possibly repeatedly, giving non-linear
// heads).
func randType(rng *rand.Rand, vars []int, maxDepth int) Type {
	if maxDepth <= 1 || rng.Intn(2) == 0 {
		if len(vars) > 0 && rng.Intn(2) == 0 {
			return vr(vars[rng.Intn(len(vars))])
		}
		for {
			c := consPool[rng.Intn(len(consPool))]
			if c.arity == 0 {
				return con(c.name)
			}
		}
	}
	c := consPool[rng.Intn(len(consPool))]
	if c.arity == 0 {
		return con(c.name)
	}
	args := make([]Type, c.arity)
	for i := range args {
		args[i] = randType(rng, vars, maxDepth-1)
	}
	return con(c.name, args...)
}

func randInstance(rng *rand.Rand) (string, Type, []Constraint) {
	trait := traitPool[rng.Intn(len(traitPool))]
	if rng.Intn(50) == 0 {
		trait = "" // invalid on purpose
	}
	var head Type
	if rng.Intn(20) == 0 {
		head = vr(rng.Intn(4)) // bare variable head
	} else {
		head = randType(rng, []int{0, 1, 2, 3}, 1+rng.Intn(3))
	}
	headVars := map[int]bool{}
	collectVars(head, headVars)
	var vars []int
	for v := 0; v <= 3; v++ {
		if headVars[v] {
			vars = append(vars, v)
		}
	}
	nctx := rng.Intn(3)
	if rng.Intn(50) == 0 {
		nctx = 5 // invalid on purpose
	}
	ctx := make([]Constraint, 0, nctx)
	for i := 0; i < nctx; i++ {
		ct := traitPool[rng.Intn(len(traitPool))]
		ctVars := vars
		if rng.Intn(40) == 0 {
			ctVars = []int{7} // variable outside the head: invalid on purpose
		}
		ctx = append(ctx, Constraint{Trait: ct, Type: randType(rng, ctVars, 1+rng.Intn(2))})
	}
	return trait, head, ctx
}

func resultEqual(a, b Result) bool {
	if a.Category != b.Category {
		return false
	}
	if a.Category == Success {
		return treeEqual(a.Tree, b.Tree)
	}
	return *a.Failure == *b.Failure
}

func rejectReason(err error) (RejectReason, bool) {
	if err == nil {
		return 0, false
	}
	if re, ok := err.(*RejectError); ok {
		return re.Reason, true
	}
	return 0, false
}

func sameRejection(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ra, oka := rejectReason(a)
	rb, okb := rejectReason(b)
	return oka && okb && ra == rb
}

func snapshotCache(r *Resolver) map[goalKey]cacheEntry {
	snap := make(map[goalKey]cacheEntry, len(r.cache))
	for k, e := range r.cache {
		snap[k] = *e
	}
	return snap
}

func treeFromEntries(entries map[goalKey]cacheEntry, k goalKey) *Tree {
	e := entries[k]
	tree := &Tree{
		Trait:    k.trait,
		TypeText: e.typ.String(),
		Instance: e.instance,
		Height:   e.height,
	}
	for _, ck := range e.childKeys {
		tree.Children = append(tree.Children, treeFromEntries(entries, ck))
	}
	return tree
}

// verifyInvalidation checks the exact invalidation rule against the cache
// snapshot taken before n was added, and that every doomed entry indeed
// re-resolves to a different result.
func verifyInvalidation(t *testing.T, r *Resolver, before map[goalKey]cacheEntry, n instance) {
	t.Helper()
	doomed := map[goalKey]bool{}
	var reasons []string
	for k, e := range before {
		if n.trait != k.trait || !matchPattern(n.head, e.typ, map[int]Type{}) {
			continue
		}
		sel := r.instances[e.instance-1]
		if moreSpec(sel, n) {
			reasons = append(reasons, fmt.Sprintf("keep %s<%s>: instance %d more specialized than %d",
				k.trait, e.typ, sel.id, n.id))
			continue
		}
		reasons = append(reasons, fmt.Sprintf("doom %s<%s>: instance %d not more specialized than %d",
			k.trait, e.typ, sel.id, n.id))
		doomed[k] = true
	}
	for changed := true; changed; {
		changed = false
		for k, e := range before {
			if doomed[k] {
				continue
			}
			for _, ck := range e.childKeys {
				if doomed[ck] {
					doomed[k] = true
					changed = true
					reasons = append(reasons, fmt.Sprintf("doom %s<%s>: tree contains a doomed goal", k.trait, e.typ))
					break
				}
			}
		}
	}
	for k := range r.cache {
		if _, ok := before[k]; !ok {
			t.Fatalf("AddInstance created a cache entry %v", k)
		}
		if doomed[k] {
			t.Fatalf("doomed entry %v survived invalidation", k)
		}
	}
	for k := range before {
		if !doomed[k] {
			if _, kept := r.cache[k]; !kept {
				t.Fatalf("entry %v wrongly invalidated", k)
			}
		}
	}
	// Every doomed entry must re-resolve to a different result.
	nv := &naiveResolver{d: r.depthLimit, instances: append([]instance(nil), r.instances...)}
	for k := range doomed {
		oldTree := treeFromEntries(before, k)
		res, err := nv.resolve(k.trait, before[k].typ)
		if err != nil {
			t.Fatalf("naive resolve of doomed goal %v: %v", k, err)
		}
		if res.Category == Success && treeEqual(oldTree, res.Tree) {
			t.Fatalf("doomed entry %v re-resolves to the identical tree", k)
		}
	}
	if len(doomed) > 0 {
		keys := make([]string, 0, len(doomed))
		for k := range doomed {
			keys = append(keys, fmt.Sprintf("%s<%s>", k.trait, before[k].typ))
		}
		sort.Strings(keys)
		t.Logf("  invalidation by instance %d (%s %s): doomed=%v; basis: %v",
			n.id, n.trait, n.head, keys, reasons)
	}
}

// TestRandomDifferential replays 2000 random add/resolve sequences against
// both the cached resolver and the cache-free naive simulation, checking
// result equality, the exact invalidation rule, and cache consistency.
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		d := 1 + rng.Intn(8)
		real, err := NewResolver(d)
		if err != nil {
			t.Fatalf("seq %d: %v", seq, err)
		}
		naive := &naiveResolver{d: d}
		ops := 5 + rng.Intn(20)
		t.Logf("seq %d: D=%d, %d ops", seq, d, ops)
		for op := 0; op < ops; op++ {
			if rng.Intn(100) < 55 {
				trait, head, ctx := randInstance(rng)
				before := snapshotCache(real)
				idGot, errGot := real.AddInstance(trait, head, ctx)
				idWant, errWant := naive.add(trait, head, ctx)
				if !sameRejection(errGot, errWant) || idGot != idWant {
					t.Fatalf("seq %d op %d: AddInstance(%q, %s, %v): got (id=%d, %v), naive (id=%d, %v)",
						seq, op, trait, head, ctx, idGot, errGot, idWant, errWant)
				}
				t.Logf("seq %d op %d: AddInstance(%q, %s, %v) -> id=%d err=%v",
					seq, op, trait, head, ctx, idGot, errGot)
				if errGot == nil {
					verifyInvalidation(t, real, before, real.instances[len(real.instances)-1])
					if err := real.VerifyCacheConsistency(); err != nil {
						t.Fatalf("seq %d op %d: %v", seq, op, err)
					}
				} else if len(real.cache) != len(before) {
					t.Fatalf("seq %d op %d: rejected AddInstance changed the cache", seq, op)
				}
				continue
			}
			trait := traitPool[rng.Intn(len(traitPool))]
			typ := randType(rng, nil, 1+rng.Intn(5))
			got, errGot := real.Resolve(trait, typ)
			want, errWant := naive.resolve(trait, typ)
			if !sameRejection(errGot, errWant) {
				t.Fatalf("seq %d op %d: Resolve(%q, %s): rejection mismatch %v vs %v",
					seq, op, trait, typ, errGot, errWant)
			}
			if errGot != nil {
				continue
			}
			t.Logf("seq %d op %d: Resolve(%q, %s) -> %s (naive agrees: %v)",
				seq, op, trait, typ, describeResult(got), resultEqual(got, want))
			if !resultEqual(got, want) {
				t.Fatalf("seq %d op %d: Resolve(%q, %s): got %s, naive %s",
					seq, op, trait, typ, describeResult(got), describeResult(want))
			}
		}
	}
}

func describeResult(res Result) string {
	if res.Category == Success {
		return fmt.Sprintf("success(instance=%d, height=%d)", res.Tree.Instance, res.Tree.Height)
	}
	return res.Failure.String()
}

// TestDeterministicReplay runs the same fixed operation script on two
// resolvers and requires identical outputs, including instance numbers.
func TestDeterministicReplay(t *testing.T) {
	script := func(r *Resolver) []string {
		var out []string
		record := func(format string, args ...any) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		add := func(trait string, head Type, ctx []Constraint) {
			id, err := r.AddInstance(trait, head, ctx)
			record("add %s %s -> %d %v", trait, head, id, err)
		}
		resolve := func(trait string, typ Type) {
			res, err := r.Resolve(trait, typ)
			if err != nil {
				record("resolve %s %s -> %v", trait, typ, err)
				return
			}
			record("resolve %s %s -> %s", trait, typ, describeResult(res))
		}
		add("Show", con("Int"), nil)
		add("Show", con("List", vr(0)), []Constraint{{"Show", vr(0)}})
		add("Show", con("List", con("Char")), nil)
		resolve("Show", con("List", con("Char")))
		resolve("Show", con("List", con("Int")))
		resolve("Show", listN(con("Char"), 2))
		add("Show", listN(vr(0), 2), []Constraint{{"Show", vr(0)}})
		resolve("Show", listN(con("Char"), 2))
		add("Eq", con("Pair", con("Int"), vr(0)), nil)
		add("Eq", con("Pair", vr(0), con("Int")), nil)
		resolve("Eq", con("Pair", con("Int"), con("Int")))
		add("Eq", con("Pair", con("Int"), con("Int")), nil)
		resolve("Eq", con("Pair", con("Int"), con("Int")))
		add("Cyc", con("Pair", vr(0), vr(1)), []Constraint{{"Cyc", con("Pair", vr(1), vr(0))}})
		resolve("Cyc", con("Pair", con("Int"), con("Char")))
		add("Show", con("Int"), nil) // duplicate
		resolve("Show", con("Int"))
		return out
	}
	r1 := mustResolver(t, 8)
	r2 := mustResolver(t, 8)
	out1, out2 := script(r1), script(r2)
	if len(out1) != len(out2) {
		t.Fatalf("replay produced %d vs %d outputs", len(out1), len(out2))
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("replay divergence at step %d:\n%s\n%s", i, out1[i], out2[i])
		}
		t.Logf("step %d: %s", i, out1[i])
	}
}

// TestConcurrentUse hammers the resolver from many goroutines. Concurrent
// resolves against a fixed instance set must equal the serial results;
// concurrent adds of distinct instances must all succeed with unique
// numbers; the cache must stay consistent throughout.
func TestConcurrentUse(t *testing.T) {
	r := mustResolver(t, 16)
	mustAdd(t, r, "Show", con("Int"), nil)
	mustAdd(t, r, "Show", con("List", vr(0)), []Constraint{{"Show", vr(0)}})
	mustAdd(t, r, "Show", con("List", con("Char")), nil)
	mustAdd(t, r, "Eq", con("Pair", vr(0), vr(0)), nil)

	goals := []struct {
		trait string
		typ   Type
	}{
		{"Show", con("Int")},
		{"Show", con("List", con("Int"))},
		{"Show", listN(con("Char"), 3)},
		{"Show", listN(con("Int"), 5)},
		{"Eq", con("Pair", con("Int"), con("Int"))},
		{"Eq", con("Pair", con("Int"), con("Char"))},
		{"Show", con("Bool")},
	}
	expected := make([]Result, len(goals))
	for i, g := range goals {
		res, err := r.Resolve(g.trait, g.typ)
		if err != nil {
			t.Fatalf("serial resolve %v: %v", g, err)
		}
		expected[i] = res
	}

	var wg sync.WaitGroup
	errs := make(chan string, 1024)
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				idx := rng.Intn(len(goals))
				res, err := r.Resolve(goals[idx].trait, goals[idx].typ)
				if err != nil {
					errs <- fmt.Sprintf("resolve %v: %v", goals[idx], err)
					return
				}
				if !resultEqual(res, expected[idx]) {
					errs <- fmt.Sprintf("resolve %v: got %s, want %s",
						goals[idx], describeResult(res), describeResult(expected[idx]))
					return
				}
			}
		}(int64(worker))
	}
	wg.Wait()

	// Concurrent adds of distinct instances.
	const adders = 8
	const perAdder = 10
	ids := make(chan int, adders*perAdder)
	for worker := 0; worker < adders; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perAdder; i++ {
				trait := fmt.Sprintf("CT%d", worker)
				head := con(fmt.Sprintf("N%d", i))
				id, err := r.AddInstance(trait, head, nil)
				if err != nil {
					errs <- fmt.Sprintf("add %s %s: %v", trait, head, err)
					return
				}
				ids <- id
			}
		}(worker)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}

	seen := map[int]bool{}
	count := 0
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate instance id %d", id)
		}
		seen[id] = true
		count++
	}
	if count != adders*perAdder {
		t.Fatalf("expected %d instance ids, got %d", adders*perAdder, count)
	}
	if len(r.instances) != 4+adders*perAdder {
		t.Fatalf("expected %d instances, got %d", 4+adders*perAdder, len(r.instances))
	}
	if err := r.VerifyCacheConsistency(); err != nil {
		t.Fatalf("consistency after concurrent use: %v", err)
	}
}
