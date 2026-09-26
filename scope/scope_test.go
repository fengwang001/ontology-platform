package scope

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"sync"
	"testing"

	"ontology/sym"
)

func must(t *testing.T, e error) {
	if e != nil {
		t.Fatal(e)
	}
}
func TestNaiveReference(t *testing.T) {
	for _, c := range [][2]int{{1, 500}, {2, 1000}, {42, 2000}} {
		s := New()
		rng := rand.New(rand.NewPCG(uint64(c[0]), uint64(c[0])))
		model := []map[string]sym.T{{}}
		ns := []string{"a", "b", "c", "x", "y", "z"}
		for step := 0; step < c[1]; step++ {
			switch rng.IntN(4) {
			case 0:
				if len(model) > 1 && rng.IntN(2) == 0 {
					must(t, s.Exit())
					model = model[:len(model)-1]
				} else {
					s.Enter()
					model = append(model, map[string]sym.T{})
				}
			case 1:
				n, ty := ns[rng.IntN(6)], kinds[rng.IntN(2)]
				_, dup := model[len(model)-1][n]
				e := s.Declare(n, ty)
				if dup != errors.Is(e, ErrDuplicate) || !dup && e != nil {
					t.Fatalf("step %d %s dup=%v e=%v", step, n, dup, e)
				}
				if !dup {
					model[len(model)-1][n] = ty
				}
			default:
				n := ns[rng.IntN(6)]
				got, e := s.Lookup(n)
				var want sym.T
				for i := len(model) - 1; i >= 0 && want == ""; i-- {
					want, _ = model[i][n]
				}
				if (want == "") != (e != nil) || want != got {
					t.Fatalf("step %d %s=%s want %s", step, n, got, want)
				}
			}
		}
	}
}
func TestShadowRestore(t *testing.T) {
	for _, d := range []int{1, 2, 5} {
		s := New()
		must(t, s.Declare("x", sym.Int))
		for i := 0; i < d; i++ {
			s.Enter()
		}
		must(t, s.Declare("x", sym.Bool))
		if v, _ := s.Lookup("x"); v != sym.Bool {
			t.Fatalf("d=%d inner %s", d, v)
		}
		for i := 0; i < d; i++ {
			must(t, s.Exit())
		}
		if v, _ := s.Lookup("x"); v != sym.Int {
			t.Fatalf("d=%d restored %s", d, v)
		}
	}
}
func TestDuplicateKeepsFirst(t *testing.T) {
	for _, c := range [][2]sym.T{{sym.Int, sym.Bool}, {sym.Bool, sym.Int}} {
		s := New()
		_ = s.Declare("z", c[0])
		if e := s.Declare("z", c[1]); !errors.Is(e, ErrDuplicate) {
			t.Fatalf("want dup got %v", e)
		}
		if v, _ := s.Lookup("z"); v != c[0] {
			t.Fatalf("kept %s want %s", v, c[0])
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := New()
	_ = s.Declare("x", sym.Int)
	s.Enter()
	_ = s.Declare("y", sym.Bool)
	before := s.String()
	mustReject := func(name string, run func() error, want error) {
		if e := run(); !errors.Is(e, want) {
			t.Fatalf("%s want %v got %v", name, want, e)
		}
		if s.String() != before {
			t.Fatalf("%s mutated state", name)
		}
	}
	mustReject("dup", func() error { return s.Declare("y", sym.Int) }, ErrDuplicate)
	mustReject("undef", func() error { _, e := s.Lookup("ghost"); return e }, ErrUndeclared)
	must(t, s.Exit())
	g := s.String()
	if e := s.Exit(); !errors.Is(e, ErrExitGlobal) || s.String() != g {
		t.Fatalf("exit global e=%v changed=%v", e, s.String() != g)
	}
	if ErrDuplicate == ErrUndeclared || ErrDuplicate == ErrExitGlobal || ErrUndeclared == ErrExitGlobal {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}
func TestLookupProbeBudget(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			_ = s.Declare("n"+strconv.Itoa(i), sym.Int)
		}
		_, e := s.Lookup("n" + strconv.Itoa(m-1))
		must(t, e)
		if s.probes.Load() > 1 {
			t.Fatalf("m=%d scanned >1 binding", m)
		}
	}
}
func TestConcurrentLookup(t *testing.T) {
	s := New()
	_ = s.Declare("a", sym.Int)
	_ = s.Declare("g", sym.Int)
	s.Enter()
	_ = s.Declare("a", sym.Bool)
	want := map[string]sym.T{"a": sym.Bool, "g": sym.Int}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n, w := range want {
				if v, e := s.Lookup(n); e != nil || v != w {
					t.Errorf("%s=%s,%v want %s", n, v, e, w)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}
