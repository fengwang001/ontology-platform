// 演示程序：不读参数、不联网，逐条打印 OK/FAIL，退出码非 0 表示失败。
package main

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/rank"
)

func op(k string, s int64, a bool) api.Op { return api.Op{Row: api.Row{Key: k, Score: s}, Add: a} }
func tenOps() []api.Op {
	return []api.Op{op("a", 50, true), op("b", 70, true), op("c", 50, true), op("d", 60, true), op("e", 50, true), op("b", 70, false), op("e", 50, false), op("f", 55, true), op("d", 60, false), op("g", 45, true)}
}
func brute(m map[string]int64, n int) []api.Row {
	r := make([]api.Row, 0, len(m))
	for k, s := range m {
		r = append(r, api.Row{Key: k, Score: s})
	}
	slices.SortFunc(r, func(a, b api.Row) int { return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(a.Key, b.Key)) })
	return r[:min(n, len(r))]
}
func main() {
	failed := false
	check := func(name string, ok bool) {
		fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[ok] + name)
		failed = failed || !ok
	}
	v, _ := api.New(3, 16)
	want := "+a|+b|+c|-c+d||-b+c||-c+f|-d+c||" // 十步逐步日志（无变更步为空段）
	sign := map[bool]byte{true: '+', false: '-'}
	var sb strings.Builder
	for _, o := range tenOps() {
		ch, _ := v.Apply([]api.Op{o})
		for _, c := range ch { // 每条紧凑签名 +key / -key，先 − 后 +
			sb.WriteByte(sign[c.Add])
			sb.WriteString(c.Row.Key)
		}
		sb.WriteByte('|')
	}
	check("ten-step changelog exact (step6/9 backfilled by c)", sb.String() == want)
	check("final top-N = [f55 a50 c50]", slices.Equal(v.View(), []api.Row{{Key: "f", Score: 55}, {Key: "a", Score: 50}, {Key: "c", Score: 50}}))
	check("random add/retract matches batch recompute", randomOK())
	check("changelog every prefix self-consistent", prefixOK())
	check("four distinct sentinel errors", sentinelOK())
	check("rejection leaves no trace; view still usable", noTraceOK())
	check("log-scale comparison bound at large m", rank.SelfTest() == nil)
	check("concurrent readers get identical views", concurrentOK())
	sc, _ := api.New(3, 16)
	check("api SelfCheck passes four invariants", sc.SelfCheck() == nil)
	os.Exit(map[bool]int{true: 1, false: 0}[failed])
}
func randomOK() bool {
	rv, _ := api.New(4, 64)
	model, r := map[string]int64{}, rand.New(rand.NewSource(7))
	for t := 0; t < 300; t++ {
		k := fmt.Sprintf("k%d", r.Intn(20))
		s, live := model[k]
		if !live && r.Intn(2) == 0 {
			s = int64(r.Intn(10))
			if _, e := rv.Apply([]api.Op{op(k, s, true)}); e == nil {
				model[k] = s
			}
		} else if live {
			if _, e := rv.Apply([]api.Op{op(k, s, false)}); e == nil {
				delete(model, k)
			}
		}
		if !slices.Equal(rv.View(), brute(model, 4)) {
			return false
		}
	}
	return true
}
func prefixOK() bool {
	pv, _ := api.New(3, 16)
	held := map[string]api.Row{}
	for _, o := range tenOps() {
		ch, _ := pv.Apply([]api.Op{o})
		for _, c := range ch {
			cur, ok := held[c.Row.Key]
			if c.Add == ok || !c.Add && (!ok || cur != c.Row) {
				return false
			}
			if c.Add {
				held[c.Row.Key] = c.Row
			} else {
				delete(held, c.Row.Key)
			}
		}
		if len(held) != min(3, pv.Live()) {
			return false
		}
	}
	return true
}
func sentinelOK() bool {
	e := func(n, m int, ops ...api.Op) error { w, _ := api.New(n, m); _, err := w.Apply(ops); return err }
	_, e0 := api.New(0, 4)
	return api.ErrInvalidParam != api.ErrKeyExists && api.ErrKeyExists != api.ErrRowMissing &&
		api.ErrRowMissing != api.ErrOverLimit && api.ErrKeyExists != api.ErrOverLimit &&
		errors.Is(e0, api.ErrInvalidParam) && errors.Is(e(2, 8, op("a", 1, true), op("a", 2, true)), api.ErrKeyExists) &&
		errors.Is(e(2, 8, op("z", 1, false)), api.ErrRowMissing) &&
		errors.Is(e(1, 1, op("a", 1, true), op("b", 2, true)), api.ErrOverLimit)
}
func noTraceOK() bool {
	nv, _ := api.New(2, 4)
	_, _ = nv.Apply([]api.Op{op("a", 1, true), op("b", 2, true), op("c", 3, true), op("d", 4, true)})
	live, view := nv.Live(), fmt.Sprint(nv.View())
	for _, b := range []api.Op{op("a", 9, true), op("a", 9, false), op("z", 1, false), op("e", 5, true)} {
		if _, e := nv.Apply([]api.Op{b}); e == nil || nv.Live() != live || fmt.Sprint(nv.View()) != view {
			return false
		}
	}
	_, e := nv.Apply([]api.Op{op("a", 1, false), op("e", 5, true)})
	return e == nil
}
func concurrentOK() bool {
	cv, _ := api.New(8, 256)
	var ops []api.Op
	for i := 0; i < 100; i++ {
		ops = append(ops, op(fmt.Sprintf("k%03d", i), int64((i*7)%53), true))
	}
	if _, e := cv.Apply(ops); e != nil {
		return false
	}
	const g = 16
	var wg sync.WaitGroup
	views := make([][]api.Row, g)
	start := make(chan struct{})
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(id int) { defer wg.Done(); <-start; views[id] = cv.View() }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < g; i++ {
		if fmt.Sprint(views[i]) != fmt.Sprint(views[0]) {
			return false
		}
	}
	return true
}
