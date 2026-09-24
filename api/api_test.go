package api_test

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand"
	"ontology/api"
	"ontology/interval"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func wf(rs []api.Row) bool { // 良构：from<to、按 from 升序、两两不重叠
	for i := range rs {
		if rs[i].From >= rs[i].To || (i > 0 && rs[i-1].To > rs[i].From) {
			return false
		}
	}
	return true
}
func mk(n int, k string) []api.Event { // Eff 两两不同、Upsert/Delete 混合
	es, ops := make([]api.Event, n), [3]interval.Op{interval.Delete, interval.Upsert, interval.Upsert}
	for i := range es {
		es[i] = api.Event{Key: k, Eff: int64(2*i + 1), Op: ops[i%3], Val: fmt.Sprint(i)}
	}
	return es
}
func rec(es []api.Event) []api.Row { // 权威参照：同 Eff 后到覆盖、排序后一次性生成
	m := map[int64]interval.Point{}
	for _, e := range es {
		m[e.Eff] = interval.Point{Eff: e.Eff, Del: e.Op == interval.Delete, Val: e.Val}
	}
	return interval.Build(slices.SortedFunc(maps.Values(m), func(a, b interval.Point) int { return cmp.Compare(a.Eff, b.Eff) }))
}
func u(e int64, v string) api.Event { return api.Event{Key: "K", Eff: e, Op: interval.Upsert, Val: v} }
func dl(e int64) api.Event          { return api.Event{Key: "K", Eff: e, Op: interval.Delete} }
func nd(n int) *api.DB              { d, _ := api.New(n); return d }
func TestInvariantRecompute(t *testing.T) {
	// 钉住不变量 1：八步逐步与批量重算逐行一致，抽核 AsOf 边界并跑 SelfCheck。
	steps := []api.Event{u(10, "a"), u(30, "b"), dl(50), u(20, "c"), u(30, "d"), u(5, "f"), dl(20), u(40, "g")}
	d, seen := nd(1000), []api.Event{}
	for i, e := range steps {
		seen = append(seen, e)
		if d.Apply([]api.Event{e}) != nil || !reflect.DeepEqual(d.History("K"), rec(seen)) {
			t.Fatalf("step %d: %v", i+1, d.History("K"))
		}
	}
	at, vv, ok := []int64{3, 25, 35, 45, 55}, []string{"", "", "d", "g", ""}, []bool{false, false, true, true, false}
	for i := range at {
		if v, o := d.AsOf("K", at[i]); v != vv[i] || o != ok[i] {
			t.Fatalf("AsOf(%d)=(%q,%v) want (%q,%v)", at[i], v, o, vv[i], ok[i])
		}
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
func TestApplyErrorSentinels(t *testing.T) {
	// 钉住第五节：四类错误可判定且互不相同，同 Eff 替换不占名额。
	d10, d1 := nd(10), nd(1)
	_ = d1.Apply([]api.Event{u(1, "a")})
	_, eNew := api.New(0)
	cases := []struct{ err, want error }{
		{eNew, api.ErrInvalidMaxPoints},
		{d10.Apply([]api.Event{{Key: "", Eff: 1, Op: interval.Upsert}}), api.ErrEmptyKey},
		{d10.Apply([]api.Event{u(-1, "x")}), api.ErrEffOutOfRange},
		{d10.Apply([]api.Event{dl(math.MaxInt64)}), api.ErrEffOutOfRange},
		{d1.Apply([]api.Event{u(2, "b")}), api.ErrTooManyPoints},
	}
	got := map[error]bool{}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("case %d: %v want %v", i, c.err, c.want)
		}
		got[c.want] = true
	}
	if len(got) != 4 || d1.Apply([]api.Event{u(1, "z")}) != nil {
		t.Fatalf("distinct=%v or same-eff replace wrongly rejected", got)
	}
}
func TestRejectionAtomic(t *testing.T) {
	// 钉住不变量 4：非法/超限整批整体失败不留痕，之后仍可正常使用。
	bad := [][]api.Event{
		{{Key: "", Eff: 1, Op: interval.Upsert, Val: "x"}},
		{u(-1, "x")}, {dl(math.MaxInt64)},
		{{Key: "Z", Eff: 1, Op: interval.Upsert, Val: "q"}, {Key: "", Eff: 2, Op: interval.Upsert}},
	}
	over := make([]api.Event, 11) // K 已有 1 点再加 11 个新 Eff：任何 cap 都必超限
	for i := range over {
		over[i] = u(int64(100+i), "x")
	}
	for ci, n := range []int{10, 1} {
		d := nd(n)
		_ = d.Apply([]api.Event{u(10, "a")})
		before := d.History("K")
		for i, b := range bad {
			if d.Apply(b) == nil || !reflect.DeepEqual(d.History("K"), before) || d.History("Z") != nil {
				t.Fatalf("cap=%d batch %d left trace", n, i)
			}
		}
		if d.Apply(over) == nil || !reflect.DeepEqual(d.History("K"), before) {
			t.Fatalf("cap=%d overflow batch left trace", n)
		}
		if ci == 0 && d.Apply([]api.Event{u(20, "b")}) != nil {
			t.Fatal("db not usable after rejection")
		}
	}
}
func TestConcurrentDifferentKeys(t *testing.T) {
	// 钉住第六节：N 个 goroutine 各写不同键（随机顺序、逐条应用），同时并发
	// 读他键；读始终良构、AsOf 不 panic，终态与顺序喂入逐行相同；不用 sleep。
	const N, M = 16, 80
	want, d := rec(mk(M, "K")), nd(10000)
	var fail int32
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			es := mk(M, fmt.Sprintf("k%d", g))
			rng := rand.New(rand.NewSource(int64(g) + 77))
			rng.Shuffle(M, func(i, j int) { es[i], es[j] = es[j], es[i] })
			for _, e := range es {
				if d.Apply([]api.Event{e}) != nil {
					atomic.AddInt32(&fail, 1)
				}
				k := fmt.Sprintf("k%d", rng.Int63n(N))
				if !wf(d.History(k)) {
					atomic.AddInt32(&fail, 1)
				}
				d.AsOf(k, int64(rng.Intn(2*M)))
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < N; g++ {
		if !reflect.DeepEqual(d.History(fmt.Sprintf("k%d", g)), want) {
			t.Fatalf("key k%d differs from sequential", g)
		}
	}
	if fail != 0 {
		t.Fatalf("ill-formed concurrent reads or failed writes: %d", fail)
	}
}
