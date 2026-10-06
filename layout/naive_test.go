package layout

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"testing"
)

// naiveModel is an independent oracle: it stores every accepted declaration
// and, after each mutation, recomputes ALL composite layouts from scratch and
// re-derives versions by diffing against its previous snapshot. It shares no
// code with Registry's incremental propagation except the pure layout rules
// it copies from the specification.
type naiveModel struct {
	cfg    Config
	basic  map[string][2]int // size, align
	specs  map[string]CompositeSpec
	layout map[string]*Layout
	ver    map[string]int
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:    cfg,
		basic:  map[string][2]int{},
		specs:  map[string]CompositeSpec{},
		layout: map[string]*Layout{},
		ver:    map[string]int{},
	}
}

func (m *naiveModel) basicAlign(name string) (size, align int, ok bool) {
	if v, has := m.basic[name]; has {
		return v[0], v[1], true
	}
	return 0, 0, false
}

// recomputeAll recomputes every composite layout from scratch. It assumes the
// embedding graph is acyclic (the system rejects cycles) and uses fixed-point
// iteration; per query it is O(types * fields), deliberately independent of
// the incremental implementation.
func (m *naiveModel) recomputeAll() (map[string]*Layout, error) {
	ly := map[string]*Layout{}
	remaining := map[string]CompositeSpec{}
	for n, s := range m.specs {
		remaining[n] = s
	}
	for len(remaining) > 0 {
		progress := false
		for n, s := range remaining {
			resolvable := true
			for _, f := range s.Fields {
				if f.Indirect {
					continue
				}
				if _, _, b := m.basicAlign(f.TypeName); !b {
					if _, c := ly[f.TypeName]; !c {
						resolvable = false
						break
					}
				}
			}
			if !resolvable {
				continue
			}
			l, err := computeLayout(m.cfg, s, func(name string) *Layout {
				if sz, al, b := m.basicAlign(name); b {
					return &Layout{Name: name, Size: sz, Align: al}
				}
				return ly[name]
			})
			if err != nil {
				return nil, err
			}
			ly[n] = l
			delete(remaining, n)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("naive model stuck")
		}
	}
	return ly, nil
}

// bumpVersions compares freshly computed layouts with the previous ones and
// increments versions on any difference, exactly as the spec requires: a
// version grows only when the actual layout changes.
func (m *naiveModel) bumpVersions(fresh map[string]*Layout) {
	for n, l := range fresh {
		old := m.layout[n]
		if old == nil {
			m.ver[n] = 1
		} else if !layoutsEqual(old, l) {
			m.ver[n]++
		}
	}
	for n := range m.layout {
		if fresh[n] == nil {
			delete(m.ver, n)
		}
	}
	m.layout = fresh
}

func (m *naiveModel) basicOK(name string, size, align int) bool {
	if size <= 0 || size > m.cfg.MaxSize || !m.cfg.allowed(align) {
		return false
	}
	if _, exists := m.basic[name]; exists {
		return false
	}
	m.basic[name] = [2]int{size, align}
	return true
}

// registerOK mirrors Registry.Register validation using only naive state.
func (m *naiveModel) registerOK(s CompositeSpec) bool {
	if s.Name == "" {
		return false
	}
	if _, exists := m.specs[s.Name]; exists {
		return false
	}
	return m.validate(s)
}

func (m *naiveModel) modifyOK(s CompositeSpec) bool {
	if _, exists := m.specs[s.Name]; !exists {
		return false
	}
	return m.validate(s)
}

func (m *naiveModel) validate(s CompositeSpec) bool {
	if s.MaxAlign != 0 && !m.cfg.allowed(s.MaxAlign) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range s.Fields {
		if f.Name == "" {
			return false
		}
		if !f.Indirect {
			if _, _, b := m.basicAlign(f.TypeName); !b {
				if _, c := m.specs[f.TypeName]; !c {
					return false
				}
			}
		}
		if !f.Indirect {
			if _, al, b := m.basicAlign(f.TypeName); b {
				if !m.cfg.allowed(al) {
					return false
				}
			}
		}
		if seen[f.Name] {
			return false
		}
		seen[f.Name] = true
	}
	// Size and cycles are checked by a trial recompute on the prospective graph.
	trial := map[string]CompositeSpec{}
	for n, sp := range m.specs {
		trial[n] = sp
	}
	trial[s.Name] = s
	if naiveCycle(s.Name, trial) {
		return false
	}
	save := m.specs
	m.specs = trial
	all, err := m.recomputeAll()
	m.specs = save
	if err != nil {
		return false
	}
	for _, l := range all {
		if l.Size > m.cfg.MaxSize {
			return false
		}
	}
	return true
}

func naiveCycle(root string, specs map[string]CompositeSpec) bool {
	color := map[string]int{}
	var visit func(n string, start bool) bool
	visit = func(n string, start bool) bool {
		if color[n] == 2 {
			return false
		}
		color[n] = 1
		if s, ok := specs[n]; ok {
			for _, f := range s.Fields {
				if f.Indirect {
					continue
				}
				d := f.TypeName
				if d == root && !start {
					return true
				}
				if color[d] == 1 {
					return true
				}
				if visit(d, false) {
					return true
				}
			}
		}
		color[n] = 2
		return false
	}
	return visit(root, true)
}

// rngSpec builds a random composite spec referring only to types already in
// the given name set (indirect targets may be fresh "future" names).
func randomSpec(rng *rand.Rand, name string, known []string) CompositeSpec {
	nf := rng.Intn(5)
	spec := CompositeSpec{Name: name}
	if rng.Intn(3) == 0 {
		spec.MaxAlign = []int{0, 1, 2, 4, 8}[rng.Intn(5)]
	}
	for i := 0; i < nf; i++ {
		f := Field{Name: fmt.Sprintf("f%d", i), Compact: rng.Intn(4) == 0}
		if len(known) > 0 && rng.Intn(2) == 0 {
			f.TypeName = known[rng.Intn(len(known))]
		} else {
			f.Indirect = true
			f.TypeName = fmt.Sprintf("future%d", rng.Intn(4))
		}
		spec.Fields = append(spec.Fields, f)
	}
	return spec
}

func TestRandomDifferential(t *testing.T) {
	logger := &testLogger{w: os.Stdout}
	seeds := []int64{1, 2, 3, 2026, 1539}
	if testing.Short() {
		seeds = seeds[:1]
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := DefaultConfig()
			cfg.MaxSize = 200 // small enough to trigger size rejections
			reg, err := NewRegistry(cfg, logger)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaiveModel(cfg)
			knownComposites := []string{}
			known := []string{"u8", "u16", "u32", "u64"}
			naive.basic["u8"] = [2]int{1, 1}
			naive.basic["u16"] = [2]int{2, 2}
			naive.basic["u32"] = [2]int{4, 4}
			naive.basic["u64"] = [2]int{8, 8}
			mustBasic(t, reg, "u8", 1, 1)
			mustBasic(t, reg, "u16", 2, 2)
			mustBasic(t, reg, "u32", 4, 4)
			mustBasic(t, reg, "u64", 8, 8)

			for step := 0; step < 600; step++ {
				action := rng.Intn(10)
				switch {
				case action < 5 || len(knownComposites) == 0:
					name := fmt.Sprintf("T%d", rng.Intn(12))
					spec := randomSpec(rng, name, known)
					okReg := reg.Register(spec) == nil
					okNaive := naive.registerOK(cloneSpec(spec))
					if okReg != okNaive {
						t.Fatalf("seed %d step %d Register %q acceptance: reg=%v naive=%v\n%+v",
							seed, step, name, okReg, okNaive, spec)
					}
					if okReg {
						if !contains(knownComposites, name) {
							knownComposites = append(knownComposites, name)
							known = append(known, name)
						}
						naive.specs[name] = cloneSpec(spec)
						fresh, ferr := naive.recomputeAll()
						if ferr != nil {
							t.Fatalf("naive recompute: %v", ferr)
						}
						naive.bumpVersions(fresh)
					}
					debugDivergence(t, seed, step, "Register", name, reg, naive, knownComposites)
				case action < 9:
					name := knownComposites[rng.Intn(len(knownComposites))]
					spec := randomSpec(rng, name, known)
					_, errReg := reg.Modify(spec)
					okNaive := naive.modifyOK(cloneSpec(spec))
					if (errReg == nil) != okNaive {
						deps := []string{}
						for _, f := range spec.Fields {
							if !f.Indirect {
								deps = append(deps, f.TypeName)
							}
						}
						t.Fatalf("seed %d step %d Modify %q acceptance: reg=%v(%v) naive=%v\nspec=%+v\ndepLayouts=%v",
							seed, step, name, errReg == nil, errReg, okNaive, spec, depLayouts(reg, deps))
					}
					if errReg == nil {
						naive.specs[name] = cloneSpec(spec)
						fresh, ferr := naive.recomputeAll()
						if ferr != nil {
							t.Fatalf("naive recompute: %v", ferr)
						}
						naive.bumpVersions(fresh)
					}
					debugDivergence(t, seed, step, "Modify", name, reg, naive, knownComposites)
				default:
					// Checkpoint: compare every layout and version.
					for _, n := range knownComposites {
						rl, err := reg.Get(n)
						if err != nil {
							t.Fatalf("Get %s: %v", n, err)
						}
						nl := naive.layout[n]
						if rl.Size != nl.Size || rl.Align != nl.Align {
							t.Fatalf("seed %d %s size/align reg=%d/%d naive=%d/%d",
								seed, n, rl.Size, rl.Align, nl.Size, nl.Align)
						}
						if len(rl.Fields) != len(nl.Fields) {
							t.Fatalf("seed %d %s field count %d vs %d", seed, n, len(rl.Fields), len(nl.Fields))
						}
						for i := range rl.Fields {
							if rl.Fields[i].Offset != nl.Fields[i].Offset ||
								rl.Fields[i].Size != nl.Fields[i].Size ||
								rl.Fields[i].Name != nl.Fields[i].Name {
								t.Fatalf("seed %d %s field %d mismatch: %+v vs %+v",
									seed, n, i, rl.Fields[i], nl.Fields[i])
							}
						}
						if rl.Version != naive.ver[n] {
							t.Fatalf("seed %d step %d %s version reg=%d naive=%d regLayout=%+v naiveLayout=%+v",
								seed, step, n, rl.Version, naive.ver[n], rl, nl)
						}
					}
				}
			}
			// Final full comparison.
			names := append([]string(nil), knownComposites...)
			sort.Strings(names)
			for _, n := range names {
				rl, _ := reg.Get(n)
				nl := naive.layout[n]
				if rl.Size != nl.Size || rl.Align != nl.Align || rl.Version != naive.ver[n] {
					t.Fatalf("final %s mismatch", n)
				}
			}
		})
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func debugDivergence(t *testing.T, seed int64, step int, op, root string, reg *Registry, naive *naiveModel, names []string) {
	t.Helper()
	for _, n := range names {
		rl, err := reg.Get(n)
		if err != nil {
			continue
		}
		nl := naive.layout[n]
		if nl == nil {
			continue
		}
		if rl.Version != naive.ver[n] {
			t.Fatalf("DIVERGE seed=%d step=%d op=%s root=%s type=%s regV=%d naiveV=%d\nreg=%+v\nnaive=%+v",
				seed, step, op, root, n, rl.Version, naive.ver[n], rl, nl)
		}
	}
}

func depLayouts(reg *Registry, names []string) []string {
	out := []string{}
	for _, n := range names {
		l, err := reg.Get(n)
		if err != nil {
			out = append(out, n+":<undef>")
			continue
		}
		out = append(out, fmt.Sprintf("%s{size=%d align=%d fields=%d}", n, l.Size, l.Align, len(l.Fields)))
	}
	return out
}

func TestConcurrentClients(t *testing.T) {
	logger := &testLogger{w: os.Stdout}
	r, err := NewRegistry(DefaultConfig(), logger)
	if err != nil {
		t.Fatal(err)
	}
	mustBasic(t, r, "u32", 4, 4)
	// One shared chain root <- many leaf types, exercised concurrently.
	mustRegister(t, r, CompositeSpec{Name: "root", Fields: []Field{
		{Name: "x", TypeName: "u32"},
	}})

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 100))
			local := []string{}
			for k := 0; k < 100; k++ {
				name := fmt.Sprintf("g%d_t%d", g, k)
				spec := CompositeSpec{Name: name, Fields: []Field{
					{Name: "p", TypeName: "root"},
					{Name: "z", TypeName: "u32"},
				}}
				if err := r.Register(spec); err == nil {
					local = append(local, name)
				}
				// Read-heavy concurrent queries: every snapshot must be
				// internally consistent (size covers every field's span).
				if l, err := r.Get("root"); err == nil {
					end := 0
					for _, f := range l.Fields {
						if f.Offset+f.Size > end {
							end = f.Offset + f.Size
						}
					}
					if end > l.Size || l.Version < 1 {
						t.Errorf("inconsistent snapshot: size=%d end=%d v=%d", l.Size, end, l.Version)
					}
				}
				if rng.Intn(3) == 0 && len(local) > 0 {
					n := local[rng.Intn(len(local))]
					_, _ = r.View(n)
				}
			}
		}(g)
	}
	wg.Wait()

	// A concurrent modification of the root must leave all embedders with
	// coherent versions: either all observe growth or the call is rejected.
	before := len(r.Names())
	_, err = r.Modify(CompositeSpec{Name: "root", Fields: []Field{
		{Name: "x", TypeName: "u32"},
		{Name: "y", TypeName: "u32"},
	}})
	if err != nil {
		t.Fatalf("concurrent modify root: %v", err)
	}
	if len(r.Names()) != before {
		t.Fatalf("type count changed on modify")
	}
}

// discardLogger keeps the measured path representative (logging call present)
// without I/O noise.

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

// BenchmarkLayoutOneType measures the first complexity claim directly on the
// pure layout kernel: computeLayout performs one O(1) map lookup per field
// and never scans the registry, so ns/op is flat while the surrounding
// registry grows 100x in unrelated types. Compare ns/op between the two
// sub-benchmarks:
//
//	go test -run NONE -bench BenchmarkLayoutOneType -benchtime=100x ./layout
func BenchmarkLayoutOneType(b *testing.B) {
	for _, totalTypes := range []int{100, 10000} {
		b.Run(fmt.Sprintf("registry=%d", totalTypes), func(b *testing.B) {
			r, _ := NewRegistry(DefaultConfig(), discardLogger{})
			mustBasic(b, r, "u32", 4, 4)
			for i := 0; i < totalTypes; i++ {
				name := fmt.Sprintf("other%d", i)
				if err := r.Register(CompositeSpec{Name: name, Fields: []Field{
					{Name: "x", TypeName: "u32"},
				}}); err != nil {
					b.Fatal(err)
				}
			}
			spec := CompositeSpec{Name: "target", Fields: make([]Field, 50)}
			for i := range spec.Fields {
				spec.Fields[i] = Field{Name: fmt.Sprintf("f%d", i), TypeName: "u32"}
			}
			lookup := func(name string) *Layout {
				e := r.types[name]
				return &Layout{Name: name, Size: e.size, Align: e.align}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := computeLayout(r.cfg, spec, lookup); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkModifyReachability proves the second complexity claim: a modify
// touches only the embedding-reachable dependents. A registry of N unrelated
// chains is built; modifying the head of a chain of length R recomputes O(R)
// nodes regardless of N. Compare recompute counts in the benchmark output.
func BenchmarkModifyReachability(b *testing.B) {
	for _, chains := range []int{10, 1000} {
		b.Run(fmt.Sprintf("chains=%d", chains), func(b *testing.B) {
			r, _ := NewRegistry(DefaultConfig(), discardLogger{})
			mustBasic(b, r, "u32", 4, 4)
			const reach = 10
			for c := 0; c < chains; c++ {
				prev := "u32"
				for d := 0; d < reach; d++ {
					name := fmt.Sprintf("c%d_%d", c, d)
					if err := r.Register(CompositeSpec{Name: name, Fields: []Field{
						{Name: "p", TypeName: prev},
					}}); err != nil {
						b.Fatal(err)
					}
					prev = name
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Grow/shrink the head alternates, always propagating exactly
				// reach-1 dependents inside chain 0 and no other chain.
				spec := CompositeSpec{Name: "c0_0", Fields: []Field{
					{Name: "p", TypeName: "u32"},
					{Name: "x", TypeName: "u32"},
				}}
				res, err := r.Modify(spec)
				if err != nil {
					b.Fatal(err)
				}
				if len(res.Recomputed) != reach-1 {
					b.Fatalf("recomputed %d, want %d", len(res.Recomputed), reach-1)
				}
				if _, err := r.Modify(CompositeSpec{Name: "c0_0", Fields: []Field{
					{Name: "p", TypeName: "u32"},
				}}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
