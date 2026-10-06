package inline

import (
	"math/rand"
	"testing"
)

// nSite is the naive model's site instance: it keeps no "pending" structure at
// all. At every step it scans the whole frontier, chooses the highest heat /
// earliest path, and applies the rules literally straight from the spec.
type nSite struct {
	owner, callee string
	heat          float64
	path          []int
	chain         []string
}

type naiveResult struct {
	finalSize int
	fates     map[string]Fate // key "owner:path" -> fate; reason checked separately
	reasons   map[string]Reason
	deepest   []string
}

func naiveDecide(funcs map[string]Func, base map[string]float64, root string, cfg Config) naiveResult {
	fn := funcs[root]
	current := fn.Size
	var pending []nSite
	for i, s := range fn.Sites {
		pending = append(pending, nSite{
			owner: root, callee: s.Callee, heat: s.Heat,
			path: []int{i}, chain: []string{root},
		})
	}

	fates := map[string]Fate{}
	reasons := map[string]Reason{}
	deepest := []string{root}

	for len(pending) > 0 {
		idx := 0
		for i := 1; i < len(pending); i++ {
			a, b := pending[i], pending[idx]
			if a.heat > b.heat || (a.heat == b.heat && pathLess(a.path, b.path)) {
				idx = i
			}
		}
		site := pending[idx]
		pending = append(pending[:idx], pending[idx+1:]...)

		key := site.owner + fmtPath(site.path)
		callee, defined := funcs[site.callee]
		var reject Reason = -1
		switch {
		case !defined:
			reject = ReasonUndefinedCallee
		case callee.Flags == FlagNoInline:
			reject = ReasonNoInline
		case site.chain[len(site.chain)-1] == site.callee:
			reject = ReasonDirectRecursion
		case callee.Uninlinable:
			reject = ReasonUninlinableStructure
		case countOn(site.chain, site.callee)+1 > cfg.MaxChainOccurrences:
			reject = ReasonChainExceeded
		case callee.Flags != FlagAlwaysInline:
			next := current + callee.Size - cfg.CallOverhead
			capSize := fn.Size + fn.Size*cfg.GrowthMultiple/cfg.GrowthMultipleDen
			if next > cfg.GlobalSizeLimit || next > capSize {
				reject = ReasonBudget
			}
		}
		if reject != -1 {
			fates[key] = FateRejected
			reasons[key] = reject
			continue
		}
		fates[key] = FateInlined
		current += callee.Size - cfg.CallOverhead

		newChain := append(append([]string(nil), site.chain...), site.callee)
		if len(newChain) > len(deepest) {
			deepest = append([]string(nil), newChain...)
		}
		ratio := site.heat / base[site.callee]
		for j, child := range callee.Sites {
			cp := append(append([]int(nil), site.path...), j)
			pending = append(pending, nSite{
				owner: site.callee, callee: child.Callee,
				heat: child.Heat * ratio, path: cp,
				chain: append([]string(nil), newChain...),
			})
		}
	}
	return naiveResult{finalSize: current, fates: fates, reasons: reasons, deepest: deepest}
}

func countOn(chain []string, name string) int {
	n := 0
	for _, f := range chain {
		if f == name {
			n++
		}
	}
	return n
}

func fmtPath(p []int) string {
	s := ""
	for _, x := range p {
		s += "/" + itoa(x)
	}
	return s
}

// TestNaiveDifferential builds hundreds of random call graphs (including
// recursion, missing callees, flags, structure marks and tight budgets) and
// compares the engine against the independent literal-spec model.
func TestNaiveDifferential(t *testing.T) {
	cfg := testCfg()
	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		r := NewRegistry()

		n := 1 + rng.Intn(7)
		names := make([]string, n)
		for i := range names {
			names[i] = "f" + itoa(i)
		}
		funcs := map[string]Func{}
		for i, name := range names {
			f := Func{Name: name, Size: 1 + rng.Intn(8)}
			switch rng.Intn(10) {
			case 0:
				f.Flags = FlagNoInline
			case 1:
				f.Flags = FlagAlwaysInline
			}
			if rng.Intn(8) == 0 {
				f.Uninlinable = true
			}
			// dyadic heats only, so float64 products stay exact and ordering
			// is unambiguous across implementations.
			for k := 0; k < rng.Intn(4); k++ {
				heat := float64(int(rng.Intn(4)))
				if rng.Intn(8) == 0 && n > 0 {
					f.Sites = append(f.Sites, Site{Callee: names[rng.Intn(n)], Heat: heat})
				} else if rng.Intn(6) == 0 {
					f.Sites = append(f.Sites, Site{Callee: "ghost" + itoa(k), Heat: heat})
				} else {
					f.Sites = append(f.Sites, Site{Callee: names[rng.Intn(n)], Heat: heat})
				}
			}
			funcs[name] = f
			if err := r.Add(f); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			_ = i
		}

		// Vary budgets to exercise boundary/overrun regions.
		c := cfg
		c.GlobalSizeLimit = 5 + rng.Intn(60)
		if rng.Intn(2) == 0 {
			c.GrowthMultiple, c.GrowthMultipleDen = 1, 1
		} else {
			c.GrowthMultiple, c.GrowthMultipleDen = 2, 1
		}
		c.MaxChainOccurrences = 1 + rng.Intn(3)

		snap := r.beginTask(c)
		for _, root := range names {
			rep := analyse(snap, root, nil)
			want := naiveDecide(snap.funcs, snap.base, root, c)

			if rep.FinalSize != want.finalSize {
				t.Fatalf("seed=%d root=%s final engine=%d naive=%d",
					seed, root, rep.FinalSize, want.finalSize)
			}
			if len(rep.Sites) != len(want.fates) {
				t.Fatalf("seed=%d root=%s site count %d vs %d",
					seed, root, len(rep.Sites), len(want.fates))
			}
			for _, s := range rep.Sites {
				key := s.Owner + fmtPath(s.Path)
				wf, ok := want.fates[key]
				if !ok || wf != s.Fate {
					t.Fatalf("seed=%d root=%s %s: engine fate=%v naive=%v",
						seed, root, key, s.Fate, wf)
				}
				if s.Fate == FateRejected && want.reasons[key] != s.Reason {
					t.Fatalf("seed=%d root=%s %s: engine reason=%s naive=%s",
						seed, root, key, s.Reason, want.reasons[key])
				}
			}
		}
		r.endTask(snap)
	}
}

// Determinism budget: the same large N produces identical outputs regardless
// of how many unrelated functions the program contains.
func TestBudgetCheckIndependentOfProgramSize(t *testing.T) {
	cfg := testCfg()
	cfg.GlobalSizeLimit = 100

	build := func(extra int) *Report {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 10, Sites: []Site{{Callee: "c", Heat: 1}}})
		mustAdd(t, r, Func{Name: "c", Size: 12}) // delta 11 -> 21 > 20: budget
		for i := 0; i < extra; i++ {
			mustAdd(t, r, Func{Name: "noise" + itoa(i), Size: 9000})
		}
		rep, err := r.Decide("main", cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	small, large := build(0), build(500)
	if small.Render() != large.Render() {
		t.Fatalf("decision changed with unrelated functions:\n%s\nvs\n%s",
			small.Render(), large.Render())
	}
}

// Recursion cost touches only the current path: adding 500 off-path functions
// cannot change a chain-limit rejection.
func TestRecursionCheckIndependentOfProgramSize(t *testing.T) {
	build := func(extra int) *Report {
		r := NewRegistry()
		mustAdd(t, r, Func{Name: "main", Size: 1, Sites: []Site{{Callee: "a"}}})
		mustAdd(t, r, Func{Name: "a", Size: 1, Sites: []Site{{Callee: "b"}}})
		mustAdd(t, r, Func{Name: "b", Size: 1, Sites: []Site{{Callee: "a"}}})
		for i := 0; i < extra; i++ {
			mustAdd(t, r, Func{Name: "off" + itoa(i), Size: 1})
		}
		rep, err := r.Decide("main", testCfg(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	a, b := build(0), build(500)
	if a.Render() != b.Render() {
		t.Fatal("chain decision changed with off-path functions")
	}
}
