package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/rand"
	"ontology/api"
	"ontology/interval"
	"ontology/scd"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
)

var failed bool

func check(n string, ok bool) {
	p := "OK"
	if !ok {
		p, failed = "FAIL", true
	}
	fmt.Println(p, n)
}
func newDB(n int) *api.DB { d, _ := api.New(n); return d }
func ev(eff int64, v string) api.Event {
	return api.Event{Key: "K", Eff: eff, Op: interval.Upsert, Val: v}
}
func evd(eff int64) api.Event { return api.Event{Key: "K", Eff: eff, Op: interval.Delete} }
func wf(rs []interval.Row) bool {
	for i := range rs {
		if rs[i].From >= rs[i].To || (i > 0 && rs[i-1].To > rs[i].From) {
			return false
		}
	}
	return true
}
func mkEvents(n int, key string) []api.Event { // Eff 两两不同、Upsert/Delete 混合
	es := make([]api.Event, n)
	for i := range es {
		op := interval.Upsert
		if i%3 == 0 {
			op = interval.Delete
		}
		es[i] = api.Event{Key: key, Eff: int64(2*i + 1), Op: op, Val: fmt.Sprint(i)}
	}
	return es
}
func recompute(evs []api.Event) []interval.Row { // 独立批量重算：同 Eff 后到覆盖、排序生成
	m := map[int64]interval.Point{}
	for _, e := range evs {
		m[e.Eff] = interval.Point{Eff: e.Eff, Del: e.Op == interval.Delete, Val: e.Val}
	}
	return interval.Build(slices.SortedFunc(maps.Values(m),
		func(a, b interval.Point) int { return cmp.Compare(a.Eff, b.Eff) }))
}
func concurrencyOK() bool {
	const N, M = 8, 60
	want := recompute(mkEvents(M, "K"))
	d := newDB(1000)
	var bad int32
	var ww, wr sync.WaitGroup
	for r := 0; r < 3; r++ { // 与写者并发的固定轮次读者（不用 sleep），每份历史须良构
		wr.Add(1)
		go func(seed int64) {
			defer wr.Done()
			rng := rand.New(rand.NewSource(seed))
			for n := 0; n < 1000; n++ {
				k := fmt.Sprintf("k%d", rng.Int63n(N))
				if !wf(d.History(k)) {
					atomic.AddInt32(&bad, 1)
				}
				d.AsOf(k, rng.Int63n(2*M))
			}
		}(int64(r) + 1)
	}
	for g := 0; g < N; g++ { // N 个写者：不同键、随机到达顺序、逐条应用以高频交错
		ww.Add(1)
		go func(g int) {
			defer ww.Done()
			es := mkEvents(M, fmt.Sprintf("k%d", g))
			rand.New(rand.NewSource(int64(g)+99)).Shuffle(M, func(i, j int) { es[i], es[j] = es[j], es[i] })
			for _, e := range es {
				if d.Apply([]api.Event{e}) != nil {
					atomic.AddInt32(&bad, 1)
				}
			}
		}(g)
	}
	ww.Wait()
	wr.Wait()
	for g := 0; g < N; g++ {
		if !reflect.DeepEqual(d.History(fmt.Sprintf("k%d", g)), want) {
			return false
		}
	}
	return bad == 0
}
func main() {
	pts := []interval.Point{{Eff: 5, Val: "f"}, {Eff: 10, Val: "a"}, {Eff: 20, Del: true}, {Eff: 30, Val: "d"}, {Eff: 40, Val: "g"}, {Eff: 50, Del: true}}
	fin := []interval.Row{{From: 5, To: 10, Val: "f"}, {From: 10, To: 20, Val: "a"}, {From: 30, To: 40, Val: "d"}, {From: 40, To: 50, Val: "g"}}
	_, _, sp := interval.SplitAt(interval.Row{From: 10, To: 30, Val: "a"}, 20)
	_, cl := interval.Close(interval.Row{From: 10, To: interval.Inf, Val: "a"}, 30)
	check("interval: build/split/close/eff-validity", reflect.DeepEqual(interval.Build(pts), fin) && sp && cl && interval.ValidEff(0) && !interval.ValidEff(interval.Inf))
	steps := []api.Event{ev(10, "a"), ev(30, "b"), evd(50), ev(20, "c"), ev(30, "d"), ev(5, "f"), evd(20), ev(40, "g")}
	d, seen, eightOK := newDB(1000), []api.Event{}, true
	for _, e := range steps { // 八步逐步历史：第4步拆分、第5/7步替换；逐步等于重算且良构无重叠
		seen = append(seen, e)
		eightOK = eightOK && d.Apply([]api.Event{e}) == nil && reflect.DeepEqual(d.History("K"), recompute(seen)) && wf(d.History("K"))
	}
	check("eight-step histories: split@4 replace@5/7, well-formed no overlap", eightOK)
	base := mkEvents(40, "K")
	rng, orderOK := rand.New(rand.NewSource(7)), true
	for round := 0; round < 8; round++ { // 随机到达顺序 == 批量重算
		es := append([]api.Event(nil), base...)
		rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		tmp := newDB(1000)
		orderOK = orderOK && tmp.Apply(es) == nil && reflect.DeepEqual(tmp.History("K"), recompute(base))
	}
	check("random arrival order equals sequential and batch recompute", orderOK)
	check("api.SelfCheck passes all four invariants", d.SelfCheck() == nil)
	_, eMax := api.New(0)
	d2, d3 := newDB(10), newDB(1)
	_ = d3.Apply([]api.Event{ev(1, "a")})
	is := func(err, w error) bool { return errors.Is(err, w) }
	sentinelOK := is(eMax, api.ErrInvalidMaxPoints) &&
		is(d2.Apply([]api.Event{{Key: "", Eff: 1, Op: interval.Upsert, Val: "x"}}), api.ErrEmptyKey) &&
		is(d2.Apply([]api.Event{ev(-1, "x")}), api.ErrEffOutOfRange) &&
		is(d2.Apply([]api.Event{evd(math.MaxInt64)}), api.ErrEffOutOfRange) &&
		is(d3.Apply([]api.Event{ev(2, "b")}), api.ErrTooManyPoints)
	distinct := api.ErrInvalidMaxPoints != api.ErrEmptyKey && api.ErrEmptyKey != api.ErrEffOutOfRange &&
		api.ErrEffOutOfRange != api.ErrTooManyPoints && api.ErrInvalidMaxPoints != api.ErrTooManyPoints
	check("four distinct sentinel errors; same-eff replace not capped", sentinelOK && distinct && d3.Apply([]api.Event{ev(1, "z")}) == nil)
	d4 := newDB(10)
	_ = d4.Apply([]api.Event{ev(10, "a")})
	h0 := d4.History("K")
	eBatch := d4.Apply([]api.Event{{Key: "Z", Eff: 1, Op: interval.Upsert, Val: "q"}, {Key: "", Eff: 2, Op: interval.Upsert, Val: "x"}})
	untouched := reflect.DeepEqual(d4.History("K"), h0)
	eAfter := d4.Apply([]api.Event{ev(20, "b")})
	check("rejected batch leaves no trace and db stays usable", eBatch != nil && d4.History("Z") == nil && untouched && eAfter == nil)
	check("locating comparisons bounded by 2*ceil(log2 m)+4", scd.NewTable(1).LocatingIsLogarithmic())
	check("concurrent writers/readers: histories match sequential", concurrencyOK())
	if failed {
		os.Exit(1)
	}
}
