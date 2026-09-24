// Command demo verifies the batch topological refresh of a view dependency DAG.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"

	"ontology/api"
)

var fails int

func ok(c bool, m string) {
	p := map[bool]string{true: "OK ", false: "FAIL "}
	fmt.Println(p[c] + m)
	if !c {
		fails = 1
	}
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func addViews(x *api.API, ds [][]string) {
	for _, d := range ds {
		must(x.AddView(d[0], d[1:]...))
	}
}

// full independently recomputes in declaration order (topological), never
// reading package state: pins the full-recompute invariant.
func full(order []string, dm map[string][]string, bv map[string]int64) map[string]int64 {
	got := map[string]int64{}
	for _, v := range order {
		var s int64
		for _, d := range dm[v] {
			if _, iv := dm[d]; iv {
				s += got[d]
			} else {
				s += bv[d]
			}
		}
		got[v] = s
	}
	return got
}

func main() {
	// 1: section 3 eight writes, one Refresh; dirty order/log/values.
	a := api.New(0)
	d3 := [][]string{{"v1", "b1"}, {"v2", "b2"}, {"v3", "v1", "v2"}, {"v4", "v3", "b3"}}
	addViews(a, d3)
	bs := []string{"b1", "b3", "b2", "b1", "b2", "b3", "b1", "b2"}
	for i, x := range []int64{2, 7, 4, 5, 1, 2, 3, 6} {
		must(a.SetBase(bs[i], x))
	}
	log, _ := a.Refresh()
	want := []api.Change{{Name: "v1", Old: 0, New: 3}, {Name: "v2", Old: 0, New: 6}, {Name: "v3", Old: 0, New: 9}, {Name: "v4", Old: 0, New: 11}}
	ok(len(log) == 4 && reflect.DeepEqual(log, want) && a.View()["v3"] == 9 && a.View()["v4"] == 11, "八步后全脏、顺序[v1 v2 v3 v4]、逐条日志、v3=9 v4=11")
	// 2: 20 random batches on a diamond vs this file's independent recompute.
	q := api.New(0)
	dq := [][]string{{"x", "a", "b"}, {"y", "x", "b", "c"}, {"z", "x", "y"}}
	addViews(q, dq)
	mdq := map[string][]string{"x": {"a", "b"}, "y": {"x", "b", "c"}, "z": {"x", "y"}}
	rng, bv, match := rand.New(rand.NewSource(1)), map[string]int64{}, true
	for i := 0; i < 20; i++ {
		for _, b := range []string{"a", "b", "c"} {
			v := int64(rng.Intn(21) - 10)
			bv[b] = v
			must(q.SetBase(b, v))
		}
		q.Refresh()
		match = match && reflect.DeepEqual(q.View(), full([]string{"x", "y", "z"}, mdq, bv))
	}
	ok(match, "20 个随机批后 View() 与独立全量重算逐名一致")
	// 3: log prefixes self-consistent; each view appears exactly once.
	m3 := map[string][]string{"v1": {"b1"}, "v2": {"b2"}, "v3": {"v1", "v2"}, "v4": {"v3", "b3"}}
	end := map[string]int64{"b1": 3, "b2": 6, "b3": 2}
	cur, seen, pc := map[string]int64{}, map[string]int{}, true
	for _, c := range log {
		var s int64
		for _, d := range m3[c.Name] {
			if _, iv := m3[d]; iv {
				s += cur[d]
			} else {
				s += end[d]
			}
		}
		pc = pc && cur[c.Name] == c.Old && s == c.New
		cur[c.Name], seen[c.Name] = c.New, seen[c.Name]+1
	}
	pc = pc && seen["v1"] == 1 && seen["v2"] == 1 && seen["v3"] == 1 && seen["v4"] == 1
	ok(pc, "变更日志每前缀自洽且每视图恰好一次")
	// 4: four pairwise-distinct decidable error classes, separate instances.
	mkf := func() *api.API { e := api.New(2); must(e.AddView("v1", "b1")); must(e.AddView("v2", "b2")); return e }
	mkr := func() *api.API { e := api.New(3); must(e.AddView("v1", "b1")); return e }
	f, u, cy, t := mkf(), mkf(), mkr(), mkf()
	cs := []struct{ err, want error }{
		{f.AddView("", "b1"), api.ErrEmptyName}, {u.SetBase("ghost", 1), api.ErrUnknownName},
		{cy.AddView("v3", "v3"), api.ErrCycle}, {t.AddView("v9", "b1"), api.ErrTooManyViews},
	}
	eo := true
	ws := map[error]bool{}
	for _, x := range cs {
		eo = eo && errors.Is(x.err, x.want) && !ws[x.want]
		ws[x.want] = true
	}
	ok(eo, "四类错误可判定且互不相同")
	// 5: rejected ops leave no trace; instance stays usable.
	e := mkf()
	before := e.View()
	_ = e.AddView("", "x")
	_ = e.SetBase("ghost", 1)
	noTrace := reflect.DeepEqual(e.View(), before)
	must(e.SetBase("b1", 5))
	e.Refresh()
	ok(noTrace && e.View()["v1"] == 5, "被拒后状态不变且实例仍可正常使用")
	// 6: chain m-1 + diamond merge, all dirty; log length (== recomputations) is m.
	m, vn := 10000, func(i int) string { return fmt.Sprintf("v%05d", i) }
	big := api.New(0)
	must(big.AddView(vn(1), "b"))
	for i := 2; i < m; i++ {
		must(big.AddView(vn(i), vn(i-1)))
	}
	must(big.AddView(vn(m), vn(1), vn(m-1)))
	must(big.SetBase("b", 1))
	bl, _ := big.Refresh()
	ok(len(bl) == m && big.View()[vn(m)] == 2, fmt.Sprintf("m=%d 重算次数=脏视图数=%d（共享子树只刷一次）", m, len(bl)))
	// 7: concurrent readers of one refreshed instance see identical snapshots.
	base := a.View()
	res := make(chan bool, 32)
	for g := 0; g < 32; g++ {
		go func() { res <- reflect.DeepEqual(a.View(), base) }()
	}
	same := true
	for g := 0; g < 32; g++ {
		same = same && <-res
	}
	ok(same, "32 goroutine 并发只读结果逐字段相同")
	ok(a.SelfCheck() == nil, "内置 SelfCheck 通过")
	if fails > 0 {
		os.Exit(1)
	}
}
