// Command demo exercises the LSO / read_committed implementation.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"ontology/api"
	"ontology/rc"
	"ontology/txlog"
)

var allOK = true
var hws = []int{2, 4, 6, 7, 9, 10, 11}

func check(ok bool, f string, a ...any) {
	fmt.Printf("%s "+f+"\n", append([]any{map[bool]string{true: "OK ", false: "FAIL "}[ok]}, a...)...)
	if !ok {
		allOK = false
	}
}
func eq(x, y []string) bool                    { return strings.Join(x, "|") == strings.Join(y, "|") }
func da(a *api.API, p int, v string)           { a.AppendData(p, v) }
func f1(a *api.API)                            { da(a, 1, "a"); da(a, 2, "b"); da(a, 1, "c"); a.AppendCommit(1) }
func f2(a *api.API)                            { da(a, 3, "d"); da(a, 2, "e"); a.AppendAbort(2); da(a, 3, "f") }
func f3(a *api.API)                            { da(a, 1, "g"); a.AppendCommit(3); a.AppendAbort(1) }
func feed(a *api.API)                          { f1(a); f2(a); f3(a) }
func err2(_ int, e error) error                { return e }
func err3(_ []string, _ int, e error) error    { return e }
func val0(v []string, _ int, _ error) []string { return v }
func reject(a *api.API, b0 []string) bool {
	return errors.Is(err2(a.AppendData(0, "x")), txlog.ErrInvalidRecord) &&
		errors.Is(err2(a.AppendCommit(99)), txlog.ErrNoActiveTransaction) &&
		errors.Is(a.AdvanceHW(-1), txlog.ErrHWOutOfRange) &&
		errors.Is(err3(a.Fetch(12)), rc.ErrInvalidFrom) && a.HW() == 11 &&
		a.LSO() == 11 && eq(val0(a.Fetch(0)), b0)
}
func main() {
	a := api.New()
	feed(a)
	wantLSO := []int{0, 1, 1, 4, 4, 8, 11}
	wantOut := [][]string{{}, {"a"}, {}, {"c"}, {}, {"d", "f"}, {}}
	lsos, from, ok7 := make([]int, 7), 0, true
	for i, h := range hws {
		a.AdvanceHW(h)
		lsos[i] = a.LSO()
		v, nx, _ := a.Fetch(from)
		from, ok7 = nx, ok7 && lsos[i] == wantLSO[i] && eq(v, wantOut[i])
	}
	check(ok7, "七步 LSO=%v 输出=%v", lsos, wantOut)
	b, _, _ := a.Fetch(0)
	check(eq(b, []string{"a", "c", "d", "f"}), "同pid提交/中止分事务判定、标记不输出: %v", b)
	s, x := interleave()
	check(eq(s, x), "随机交错: 流式==批量(%d 条)", len(x))
	check(reject(a, b), "四类错误可判定互异/被拒不留痕仍可用")
	bigtest()
	check(concurrent(), "4 并发消费者一致、各自观察 LSO 单调")
	check(api.New().SelfCheck() == nil, "SelfCheck 核验四不变量")
	if !allOK {
		os.Exit(1)
	}
}
func bigtest() {
	b, m := api.New(), 10000
	for p := 1; p <= m; p++ {
		b.AppendData(p, "x")
	}
	b.AdvanceHW(m)
	b.AppendCommit(m)
	b.AdvanceHW(m + 1)
	l1 := b.LSO()
	b.AppendCommit(1)
	b.AdvanceHW(m + 2)
	check(l1 == 0 && b.LSO() == 1, "m=%d 未决按首位点有序 LSO %d→%d (检查 O(1) 见测试)", m, l1, b.LSO())
}
func interleave() (s, b []string) {
	a := api.New()
	r := rand.New(rand.NewSource(7))
	open := map[int]bool{}
	n, marks, hw, cur := 0, 0, 0, 0
	for k := 0; k < 2000; k++ {
		if r.Intn(3) == 0 {
			for p := range open {
				if r.Intn(2) == 0 {
					a.AppendCommit(p)
				} else {
					a.AppendAbort(p)
				}
				delete(open, p)
				marks++
				break
			}
		}
		if r.Intn(2) == 0 {
			p := 1 + r.Intn(6)
			if !open[p] {
				a.AppendData(p, "v"+strconv.Itoa(n))
				open[p], n = true, n+1
			}
		}
		hw += r.Intn(n + marks - hw + 1)
		a.AdvanceHW(hw)
		v, nx, _ := a.Fetch(cur)
		s, cur = append(s, v...), nx
	}
	for p := range open {
		a.AppendCommit(p)
		marks++
	}
	a.AdvanceHW(n + marks)
	v, _, _ := a.Fetch(cur)
	b, _, _ = a.Fetch(0)
	return append(s, v...), b
}
func consume(w *api.API, bad *atomic.Bool, done chan<- struct{}) {
	cur, prev, got := 0, 0, []string{}
	for cur < 11 {
		l := w.LSO()
		bad.CompareAndSwap(false, l < prev)
		prev = l
		v, nx, e := w.Fetch(cur)
		bad.CompareAndSwap(false, e != nil)
		got, cur = append(got, v...), nx
	}
	bad.CompareAndSwap(false, !eq(got, []string{"a", "c", "d", "f"}))
	done <- struct{}{}
}
func concurrent() bool {
	w := api.New()
	var bad atomic.Bool
	done := make(chan struct{}, 4)
	go func() {
		feed(w)
		for _, h := range hws {
			w.AdvanceHW(h)
		}
	}()
	for range 4 {
		go consume(w, &bad, done)
	}
	for range 4 {
		<-done
	}
	return !bad.Load()
}
