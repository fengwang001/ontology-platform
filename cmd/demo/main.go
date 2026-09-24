// Command demo prints one OK/FAIL line per scenario (<=10); exit 0 iff all pass.
package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/api"
	"ontology/fk"
	"ontology/sch"
)

var failed bool

func ok(cond bool, tag string) {
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL"}[cond] + tag)
	if !cond {
		failed = true
	}
}

// op encodes one step: kind 0=P.ins 1=P.del 2=C.ins 3=C.del.
type op struct {
	kind int
	a, b string
	want error
}

func do(d *api.DB, o op) error {
	switch o.kind {
	case 0:
		return d.PIns(o.a)
	case 1:
		return d.PDel(o.a)
	case 2:
		return d.CIns(o.a, o.b)
	default:
		return d.CDel(o.a)
	}
}

func canon(d *api.DB) string {
	c := d.ViewC()
	ks := make([]string, 0, len(c))
	for k, v := range c {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	return strings.Join(d.ViewP(), ",") + "|" + strings.Join(ks, ",")
}

func main() {
	E, R, NP, NC := fk.ErrOrphanChild, fk.ErrParentReferenced, fk.ErrParentNotFound, fk.ErrChildNotFound
	// 1. Thirteen steps: verdict and both views for every row.
	steps := []op{
		{2, "k1", "p1", E}, {0, "p1", "", nil}, {2, "k1", "p1", nil},
		{2, "k2", "p2", E}, {1, "p1", "", R}, {2, "k3", "p1", nil},
		{3, "k1", "", nil}, {3, "k2", "", NC}, {3, "k3", "", nil},
		{1, "p1", "", nil}, {2, "k3", "p1", E}, {0, "p1", "", nil}, {2, "k3", "p1", nil},
	}
	views := []string{"|", "p1|", "p1|k1=p1", "p1|k1=p1", "p1|k1=p1", "p1|k1=p1,k3=p1",
		"p1|k3=p1", "p1|k3=p1", "p1|", "|", "|", "p1|", "p1|k3=p1"}
	d := api.New()
	good := true
	for i, s := range steps {
		good = good && errors.Is(do(d, s), s.want) && canon(d) == views[i]
	}
	ok(good && d.SelfCheck() == nil, "13 steps: verdicts + ViewP/ViewC per step")
	// 2. Parent late: child rejected, then accepted on re-delivery.
	d = api.New()
	late := errors.Is(d.CIns("k", "p1"), E)
	_ = d.PIns("p1")
	ok(late && d.CIns("k", "p1") == nil && d.ViewC()["k"] == "p1", "late-parent child rejected then re-delivered")
	// 3. Deleting a referenced parent is rejected and rows are kept.
	d = api.New()
	_ = d.PIns("p1")
	_ = d.CIns("k1", "p1")
	ok(errors.Is(d.PDel("p1"), R) && d.ViewC()["k1"] == "p1", "referenced P.del rejected, rows kept")
	// 4. Duplicate P.ins is idempotent.
	d = api.New()
	_ = d.PIns("p1")
	ok(d.PIns("p1") == nil && len(d.ViewP()) == 1, "duplicate P.ins idempotent")
	// 5. Four pairwise-distinct decidable sentinels.
	ok(len(map[error]struct{}{E: {}, R: {}, NP: {}, NC: {}}) == 4, "four distinct decidable sentinel errors")
	// 6. Rejections leave no trace; instance remains usable afterwards.
	d = api.New()
	_ = d.PIns("p1")
	_ = d.CIns("k1", "p1")
	before := canon(d)
	rejected := errors.Is(d.CIns("x", "ghost"), E) && errors.Is(d.PDel("p1"), R) &&
		errors.Is(d.PDel("ghost"), NP) && errors.Is(d.CDel("ghost"), NC)
	ok(rejected && canon(d) == before && d.CIns("k2", "p1") == nil && d.CDel("k2") == nil,
		"rejections leave no trace, still usable")
	// 7. Reference counts agree with the ViewC groups.
	st := sch.NewState()
	st.AddParent("p1")
	st.AddParent("p2")
	st.AddChild("k1", "p1")
	st.AddChild("k2", "p1")
	st.AddChild("k3", "p2")
	g := map[string]int{}
	for _, p := range st.Children() {
		g[p]++
	}
	ok(st.RefCountOf("p1") == g["p1"] && st.RefCountOf("p2") == g["p2"], "reference counts match ViewC groups")
	// 8. Large m: referenced/unreferenced delete verdicts are count-based and
	// identical at every scale (scan-counter const is pinned in sch's test).
	constant := true
	for _, m := range []int{100, 1000, 10000} {
		s := sch.NewState()
		for i := 0; i < m; i++ {
			p := fmt.Sprintf("p%d", i)
			s.AddParent(p)
			s.AddChild(fmt.Sprintf("c%d", i), p)
		}
		ex1, r1 := s.ProbeParentDelete("p0")
		s.AddParent("lonely")
		ex2, r2 := s.ProbeParentDelete("lonely")
		constant = constant && ex1 && r1 == 1 && ex2 && r2 == 0
	}
	ok(constant, "large-m delete probes count-based and scale-independent")
	// 9. Concurrent readers observe field-for-field identical views.
	d = api.New()
	for i := 0; i < 8; i++ {
		p := fmt.Sprintf("p%d", i)
		_ = d.PIns(p)
		_ = d.CIns(fmt.Sprintf("c%d", i), p)
	}
	const N = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	res := make([]string, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; res[g] = canon(d) }(g)
	}
	close(start)
	wg.Wait()
	same := true
	for g := 1; g < N; g++ {
		same = same && res[0] == res[g]
	}
	ok(same, "concurrent readers see identical views")
	if failed {
		panic("demo checks failed")
	}
}
