package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/lcache"
	"ontology/src"
)

var failed bool

func ok(name string, cond bool) {
	s := "OK   "
	if !cond {
		s = "FAIL "
		failed = true
	}
	fmt.Println(s + name)
}
func traceDemo() {
	a := api.New(4)
	tr := ""
	st := func() {
		_, ver, x, has := a.Snapshot("k")
		e := "无"
		if has && x {
			e = "v"
		} else if has {
			e = "负"
		}
		tr += fmt.Sprintf("f%d/%s@%d;", a.Fence("k"), e, ver)
	}
	var r1, r2, r3 src.Token
	var ok6, ok7, ok12, x13, x14 bool
	var g13, g14 string
	steps := []func(){
		func() { _ = a.Update("k", "a") }, func() { _ = a.Deliver(9) }, func() { r1, _ = a.BeginRead("k") },
		func() { _ = a.Update("k", "b") }, func() { r2, _ = a.BeginRead("k") }, func() { ok6, _ = a.FinishRead(r2) },
		func() { ok7, _ = a.FinishRead(r1) }, func() { _ = a.Deliver(9) }, func() { r3, _ = a.BeginRead("k") },
		func() { _ = a.Delete("k") }, func() { _ = a.Deliver(9) }, func() { ok12, _ = a.FinishRead(r3) },
		func() { g13, x13, _ = a.Get("k") }, func() { g14, x14, _ = a.Get("k") },
	}
	for _, s := range steps {
		s()
		st()
	}
	want := "f0/无@0;f1/无@0;f1/无@0;f1/无@0;f1/无@0;f1/v@2;f1/v@2;f2/v@2;f2/v@2;f2/v@2;f3/无@0;f3/无@0;f3/负@3;f3/负@3;"
	ok("十四步 fence/条目轨迹", tr == want)
	ok("步6接受、步7与步12回填被拒", ok6 && !ok7 && !ok12 && a.SourceReads() == 4)
	hit := func() bool { h, m := a.Stats(); return h == 1 && m == 1 }()
	ok("步13等值接受、步14命中负缓存", g13 == "" && !x13 && g14 == "" && !x14 && hit)
}
func errorsDemo() {
	a := api.New(1)
	_ = a.Update("k", "v")
	_ = a.Deliver(1)
	tk, _ := a.BeginRead("k")
	_, _ = a.FinishRead(tk)
	state := func() string {
		_, ver, x, has := a.Snapshot("k")
		return fmt.Sprint(a.SourceReads(), a.Pending(), a.Fence("k"), ver, x, has)
	}
	_ = a.Update("j", "1") // j 的事件留在队列里，用于触发 Deliver 超限
	cases := []struct{ got, want error }{
		{a.Update("", "x"), api.ErrEmptyKey}, {a.Delete("ghost"), api.ErrNotExist},
		{func() error { _, e := a.FinishRead(tk); return e }(), api.ErrBadToken},
		{func() error { _, e := a.FinishRead(src.Token{ID: 9}); return e }(), api.ErrBadToken},
		{a.Deliver(1), api.ErrTooManyKeys},
		{func() error { _, _, e := a.Get("j"); return e }(), api.ErrTooManyKeys},
	}
	good := true
	for _, c := range cases {
		good = good && errors.Is(c.got, c.want)
	}
	all := []error{api.ErrEmptyKey, api.ErrBadToken, api.ErrNotExist, api.ErrTooManyKeys}
	for i, x := range all {
		for _, y := range all[i+1:] {
			good = good && !errors.Is(x, y) && !errors.Is(y, x)
		}
	}
	ok("四类可判定错误且互不相同", good)
	good = true
	ops := []func() error{
		func() error { return a.Update("", "y") }, func() error { return a.Delete("ghost") },
		func() error { _, e := a.FinishRead(src.Token{ID: 7}); return e },
		func() error { _, _, e := a.Get(""); return e }, func() error { return a.Deliver(1) },
	}
	for _, op := range ops {
		before := state()
		good = good && op() != nil && state() == before
	}
	ok("被拒操作不留痕", good)
}
func concDemo() {
	a := api.New(16)
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			var pend []src.Token
			ops := []func(string){
				func(k string) { _ = a.Update(k, "v") }, func(k string) { _ = a.Delete(k) },
				func(k string) { _ = a.Deliver(r.Intn(4)) }, func(k string) { _, _, _ = a.Get(k) },
				func(k string) { t, _ := a.BeginRead(k); pend = append(pend, t) },
			}
			for i := 0; i < 300; i++ {
				ops[r.Intn(5)](string(rune('a' + r.Intn(6))))
				if len(pend) > 3 {
					_, _ = a.FinishRead(pend[0])
					pend = pend[1:]
				}
			}
			for _, t := range pend {
				_, _ = a.FinishRead(t)
			}
		}(w)
	}
	wg.Wait()
	for a.Pending() > 0 {
		_ = a.Deliver(a.Pending())
	}
	good := true
	for i := 0; i < 6; i++ {
		k := string(rune('a' + i))
		v1, e1, _ := a.Get(k)
		v2, e2 := a.SourceState(k)
		good = good && v1 == v2 && e1 == e2
	}
	ok("并发读写后无永久陈旧", good)
}
func main() {
	traceDemo()
	errorsDemo()
	ok("随机交错与无缓存参照一致", api.New(8).SelfCheck() == nil)
	ok("访问个数不随缓存规模增长", lcache.New(0).CheckAccessScaling())
	concDemo()
	if failed {
		os.Exit(1)
	}
}
