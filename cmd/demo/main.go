package main

import (
	"errors"
	"fmt"
	"strings"

	"ontology/agg"
	"ontology/api"
	"ontology/reb"
)

func ok(name string, good bool) {
	if good {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}
func eq(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func build(v *api.View, src []string) {
	if v.Start() != nil {
		panic("busy")
	}
	for range src {
		_ = v.Step()
	}
	if _, e := v.Commit(); e != nil {
		panic(e)
	}
}
func walk() (string, bool) {
	src := []string{"a", "b", "a", "c", "b", "a"}
	m, _ := reb.New(src, 2)
	R := func(i int) string {
		s := m.Snapshot()
		return fmt.Sprintf("%d:p%d sh=%v cp=%v com=%v g=%d", i, s.Processed, s.Shadow, s.CPShadow, s.Committed, s.Gen)
	}
	acts := []func() error{m.Start, m.Step, m.Step, m.Crash, nil, m.Start, m.Step}
	rs := []string{R(1)}
	var v6 map[string]int
	for i, f := range acts { // 第6步 f==nil：Crash 后只读 View
		if f == nil {
			v6 = m.View()
		} else {
			_ = f()
		}
		rs = append(rs, R(i+2))
	}
	g9, e9 := m.Commit()
	rs = append(rs, R(9))
	good := len(v6) == 0 && e9 == nil && g9 == 1 && eq(m.View(), map[string]int{"a": 3, "b": 2, "c": 1})
	return strings.Join(rs, " | "), good
}
func errs() (bool, bool) {
	src := []string{"a", "b", "a"}
	_, eBad := api.New(src, 0)
	v, _ := api.New(src, 2)
	eNB := v.Step()
	_ = v.Start()
	eBusy := v.Start()
	_ = v.Step()
	_, eInc := v.Commit()
	d := errors.Is(eBad, api.ErrBadChunk) && errors.Is(eNB, api.ErrNotBuilding) &&
		errors.Is(eBusy, api.ErrBusy) && errors.Is(eInc, api.ErrIncomplete)
	if len(map[error]bool{api.ErrBadChunk: true, api.ErrBusy: true, api.ErrNotBuilding: true, api.ErrIncomplete: true}) != 4 {
		d = false
	}
	_ = v.Step()
	g, e := v.Commit() // 被拒后续跑成功
	return d, e == nil && g == 1 && eq(v.View(), agg.Naive(src))
}
func resume() bool {
	n := 10000
	src := make([]string, n)
	for i := range src {
		src[i] = string(rune('a' + i%5))
	}
	m, _ := reb.New(src, 10)
	_ = m.Start()
	for c := 0; c < 5; c++ {
		_ = m.Step()
		s := m.Snapshot()
		if m.Crash() != nil || m.Start() != nil || s.Processed != s.CPProcessed {
			return false
		}
	}
	for m.Snapshot().Processed < n {
		_ = m.Step()
	}
	_, e := m.Commit()
	return e == nil && eq(m.View(), agg.Naive(src))
}
func conc() bool {
	src := []string{"y", "z", "z"}
	v, _ := api.New(src, 1)
	build(v, src)
	w := agg.Naive(src)
	const nr = 8
	first := make(chan map[string]int, nr)
	stop := make(chan struct{})
	for i := 0; i < nr; i++ {
		go func() {
			first <- v.View() // 写入前首轮：N 个只读结果逐键相同
			for {
				select {
				case <-stop:
					return
				default:
					if !eq(v.View(), w) || v.Gen() < 1 {
						panic("read off a commit boundary")
					}
				}
			}
		}()
	}
	f0 := <-first
	for i := 1; i < nr; i++ {
		if !eq(<-first, f0) {
			return false
		}
	}
	for k := 0; k < 50; k++ { // 连续 Start+Step+Commit 重建
		build(v, src)
	}
	close(stop)
	return v.Gen() == 51 && eq(v.View(), w)
}
func main() {
	r, w := walk()
	fmt.Println("walk9: " + r)
	ok("step6 View old-only; final {a3b2c1} gen1", w)
	d, t := errs()
	ok("4 sentinel errors pairwise distinct", d)
	ok("rejected ops leave no trace, still builds", t)
	ok("crash resumes from checkpoint, not linear in n", resume())
	ok("concurrent reads land only on commit boundary", conc())
	vc, _ := api.New([]string{"a", "b"}, 1)
	ok("SelfCheck + agg.Add/Naive", vc.SelfCheck() == nil)
}
