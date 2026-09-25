package main

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"unsafe"

	"ontology/bound"
	"ontology/hashfam"
	"ontology/sketch"
	"ontology/stream"
	"ontology/topk"
)

var failed bool

func check(name string, ok bool) {
	failed = failed || !ok
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func main() {
	check("hashfam: deterministic from params",
		hashfam.New(97, 5).Index(0, "alpha") == hashfam.New(97, 5).Index(0, "alpha"))
	sk, _ := sketch.New(64, 4)
	truth := map[string]uint64{}
	for i := 0; i < 300; i++ {
		k := "key" + strconv.Itoa(i%37)
		sk.Add(k, 1)
		truth[k]++
	}
	ge := true
	for k, c := range truth {
		est, _ := sk.Estimate(k)
		ge = ge && est >= c
	}
	check("sketch: estimate never underestimates", ge)
	s1, s2 := feed(64, 4, 0, 100), feed(64, 4, 0, 100)
	check("sketch: independent builds identical", reflect.DeepEqual(flat(s1), flat(s2)))
	a, b, ab := feed(32, 3, 0, 50), feed(32, 3, 50, 100), feed(32, 3, 0, 100)
	check("sketch: merge isomorphic to joint feed",
		a.Merge(b) == nil && reflect.DeepEqual(flat(a), flat(ab)))
	c1, c2 := feed(32, 3, 0, 7), feed(33, 3, 0, 9)
	f1, f2 := flat(c1), flat(c2)
	err := c1.Merge(c2)
	check("sketch: bad merge rejected, both intact", err == sketch.ErrIncompatible &&
		reflect.DeepEqual(flat(c1), f1) && reflect.DeepEqual(flat(c2), f2))
	fam := hashfam.New(2, 1)
	x, y := pair(fam, 0, -1) // x and y collide in the only row
	cell := uint64(2)        // Add(x,1)+Add(y,1) share one cell; Estimate(x)=2, true is 1
	cell -= cell             // Sub(x, Estimate(x)): the shared cell drops to 0
	check("design: Sub(estimate) underestimates y(true=1)",
		fam.Index(0, x) == fam.Index(0, y) && cell < 1)
	f3 := hashfam.New(8, 2)
	p, q := pair(f3, 0, 1) // collide in row 0 only
	joint := conservative(f3, p, q)
	split := conservative(f3, p)
	for i, v := range conservative(f3, q) {
		split[i] += v
	}
	check("design: conservative update breaks merge", !reflect.DeepEqual(joint, split))
	t1, _ := sketch.New(1000, 5)
	t2, _ := sketch.New(100000, 5)
	t1.Add("probe", 1)
	t2.Add("probe", 1)
	t1.Estimate("probe")
	t2.Estimate("probe")
	check("sketch: accesses == d for w=1e3 and w=1e5", accesses(t1) == 5 && accesses(t2) == 5)
	bp, berr := bound.FromEpsilonDelta(0.01, 0.01)
	check("bound: (eps,delta)->(w,d) conservative", berr == nil &&
		bp.W == 272 && bp.D == 5 && bp.Epsilon() <= 0.01 && bp.Delta() <= 0.01)
	tsk, _ := sketch.New(64, 4)
	dt, _ := topk.NewDetector(tsk, bound.Params{W: 64, D: 4}, 0.05)
	for i := 0; i < 100; i++ {
		dt.Add("hot", 1)
		dt.Add("c"+strconv.Itoa(i%50), 1)
	}
	check("topk: no true heavy hitter missed", slices.Contains(dt.HeavyHitters(), "hot"))
	st, _ := stream.New(stream.Config{Width: 16, Depth: 3, Phi: .5, MaxTotal: 11})
	st.Add("a", 10)
	o, _ := stream.New(stream.Config{Width: 32, Depth: 3, Phi: .5})
	_, e1 := stream.New(stream.Config{Width: 0, Depth: 3, Phi: .5})
	_, e2 := stream.New(stream.Config{Epsilon: 0, Delta: .5, Phi: .5})
	before := st.Cells()
	errs := []error{e1, e2, st.Add("", 1), st.Add("b", 2), st.Merge(o)}
	seen := map[error]bool{}
	for _, e := range errs {
		seen[e] = true
	}
	check("stream: five distinct decidable errors", len(seen) == 5 && !seen[nil])
	noTrace := reflect.DeepEqual(st.Cells(), before)
	check("stream: rejected ops leave no trace, still usable",
		noTrace && st.Add("c", 1) == nil && st.SelfCheck() == nil) // limit not yet hit
	want, _ := st.Estimate("a")
	got := make([]uint64, 8)
	var wg sync.WaitGroup
	for g := range got {
		wg.Add(1)
		go func(g int) { defer wg.Done(); got[g], _ = st.Estimate("a") }(g)
	}
	wg.Wait()
	check("stream: concurrent queries identical", slices.Equal(got, slices.Repeat([]uint64{want}, 8)))
	if failed {
		os.Exit(1)
	}
}

func feed(w, d, lo, hi int) *sketch.Sketch {
	s, _ := sketch.New(w, d)
	for i := lo; i < hi; i++ {
		s.Add("k"+strconv.Itoa(i), 1)
	}
	return s
}

func pair(f *hashfam.Family, eq, diff int) (string, string) {
	for i := 1; ; i++ {
		k := "k" + strconv.Itoa(i)
		if f.Index(eq, k) == f.Index(eq, "k0") && (diff < 0 || f.Index(diff, k) != f.Index(diff, "k0")) {
			return "k0", k
		}
	}
}

func flat(s *sketch.Sketch) []uint64 {
	_, d := s.Dims()
	var out []uint64
	for r := 0; r < d; r++ {
		out = append(out, s.Row(r)...)
	}
	return out
}
func conservative(f *hashfam.Family, seq ...string) []uint64 {
	c := make([]uint64, 16)
	for _, k := range seq {
		i0, i1 := int(f.Index(0, k)), 8+int(f.Index(1, k))
		m := min(c[i0], c[i1])
		c[i0], c[i1] = max(c[i0], m+1), max(c[i1], m+1)
	}
	return c
}

func accesses(s *sketch.Sketch) int64 {
	f := reflect.ValueOf(s).Elem().FieldByName("accessed")
	return *(*int64)(unsafe.Pointer(f.UnsafeAddr()))
}
