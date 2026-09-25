package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"ontology/api"
	"ontology/dwr"
	"ontology/rec"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK  " + name)
}

// unexp 读取 Engine 的非导出字段（演示计数器有界，不经由任何导出接口）。
func unexp(e *dwr.Engine, name string) reflect.Value {
	f := reflect.ValueOf(e).Elem().FieldByName(name)
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func checkedOf(e *dwr.Engine) int64 { return unexp(e, "checked").Int() }

func dirtyHas(e *dwr.Engine, k string) bool {
	return unexp(e, "dirty").MapIndex(reflect.ValueOf(k)).IsValid()
}

// sevenWrites 依次执行第三节的七个写，返回每步后该键是否分歧。
func sevenWrites(e *dwr.Engine) []bool {
	steps := []struct {
		key string
		fn  func() error
	}{
		{"a", func() error { return e.Put("a", "a1", 1) }},
		{"b", func() error { return e.Put("b", "b1", 2) }},
		{"c", func() error { return e.Put("c", "c1", 3) }},
		{"a", func() error { return e.PutOne(0, "a", "a2", 4) }},
		{"c", func() error { return e.DelOne(1, "c", 5) }},
		{"b", func() error { return e.DelOne(0, "b", 6) }},
		{"d", func() error { return e.PutOne(0, "d", "d1", 7) }},
	}
	div := make([]bool, len(steps))
	for i, s := range steps {
		if s.fn() != nil {
			failed = true
		}
		div[i] = dirtyHas(e, s.key)
	}
	return div
}

func main() {
	r, t := rec.Live("v", 3), rec.Tomb(5)
	ok("rec: live/tomb/winner/sentinels", r.IsLive() && t.IsTomb() && rec.Winner(r, t) == t &&
		rec.CheckKey("") == rec.ErrBadKey && rec.CheckVal("") == rec.ErrBadVal &&
		rec.CheckVer(2, 2) == rec.ErrBadVer && rec.CheckSide(2) == rec.ErrBadSide)

	e := dwr.New()
	div := sevenWrites(e)
	want := []bool{false, false, false, true, true, true, true}
	ok("dwr: per-step divergence 1..7", fmt.Sprint(div) == fmt.Sprint(want))

	e.Reconcile()
	v := e.View()
	ok("dwr: reconciled view a=a2,d=d1", len(v) == 2 && v["a"] == "a2" && v["d"] == "d1")

	e.Reconcile()
	ok("dwr: reconcile idempotent", checkedOf(e) == 0 && len(e.View()) == 2)

	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		en := dwr.New()
		for i := 0; i < m; i++ {
			if en.Put(fmt.Sprintf("k%d", i), "v", int64(i+1)) != nil {
				bounded = false
			}
		}
		if en.PutOne(0, "k0", "v2", int64(m+1)) != nil {
			bounded = false
		}
		en.Reconcile()
		bounded = bounded && checkedOf(en) <= 2
	}
	ok("dwr: checked keys bounded for m=100..10000", bounded)

	a := api.New()
	ok("api: SelfCheck four invariants", a.SelfCheck() == nil)

	e1, e2, e3 := a.Put("", "v", 1), a.Put("k", "", 1), func() error {
		_ = a.Put("k", "v", 1)
		return a.Put("k", "v", 1)
	}()
	distinct := e1 == rec.ErrBadKey && e2 == rec.ErrBadVal && e3 == rec.ErrBadVer
	before := a.View()
	ok("api: 3 distinct sentinel errors", distinct)
	ok("api: rejected writes leave no trace", len(before) == 1 && before["k"] == "v" &&
		a.Put("k2", "v", 2) == nil)

	a2 := api.New()
	for i := 0; i < 50; i++ {
		_ = a2.Put(fmt.Sprintf("k%d", i), "v", int64(i+1))
	}
	a2.Reconcile()
	wantView := a2.View()
	var wg sync.WaitGroup
	var same atomic.Bool
	same.Store(true)
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				v := a2.View()
				a2.Reconcile()
				_ = a2.SelfCheck()
				if !reflect.DeepEqual(v, wantView) {
					same.Store(false)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	ok("api: concurrent read-only views identical", same.Load())

	if failed {
		os.Exit(1)
	}
}
