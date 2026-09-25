package api_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// snap 归一化全部租户快照，供逐字段比较。
func snap(a *api.API) string {
	var b strings.Builder
	for _, v := range a.View() {
		fmt.Fprintf(&b, "%s:%d:%d:%v|", v.ID, v.KeyCount, v.TotalBytes, slices.Sorted(maps.Keys(v.Items)))
	}
	return b.String()
}

// TestIsolationAndNaiveReference 钉住不变量 1/2：朴素 map 一致 + 同名 key 隔离（含(甲)回归）。
func TestIsolationAndNaiveReference(t *testing.T) {
	a := api.New("admin")
	a.Register("A", 2, 100)
	a.Register("B", 2, 100)
	ref := map[string]map[string]string{"A": {}, "B": {}}
	for _, p := range [][3]string{{"A", "x", "1"}, {"A", "y", "22"}, {"A", "z", "333"}, {"A", "x", "4444"}, {"B", "x", "B"}} {
		if e := a.Put(p[0], p[1], p[2]); e == nil {
			ref[p[0]][p[1]] = p[2]
		} else if e != api.ErrQuotaKeys {
			t.Fatal(e)
		}
	}
	for id, m := range ref {
		for k, w := range m {
			if g, e := a.Get(id, k); e != nil || g != w {
				t.Fatalf("Get(%s,%s)=%q want %q", id, k, g, w)
			}
		}
	}
	g1, _ := a.Get("A", "x")
	g2, _ := a.Get("B", "x")
	if g1 != "4444" || g2 != "B" { // (甲) 扁平表会让 A.x 错成 "B"
		t.Fatalf("isolation broken: A.x=%q B.x=%q", g1, g2)
	}
}

// TestUsageMatchesNaiveRecount 钉住不变量 3：Usage 恒等于朴素重算且不越配额。
func TestUsageMatchesNaiveRecount(t *testing.T) {
	a := api.New("admin")
	a.Register("Q", 3, 20)
	m := map[string]string{}
	for _, o := range []struct {
		k, v string
		del  bool
		want error
	}{
		{"p", "1", false, nil}, {"q", "22", false, nil}, {"r", "333", false, nil},
		{"q", "", true, nil}, {"p", "1111", false, nil},
		{"s", strings.Repeat("s", 17), false, api.ErrQuotaBytes}, // 4+17=21>20
	} {
		e := a.Put("Q", o.k, o.v)
		if o.del {
			e = a.Del("Q", o.k)
		}
		if e != o.want {
			t.Fatalf("%+v: %v want %v", o, e, o.want)
		}
		if e == nil {
			if o.del {
				delete(m, o.k)
			} else {
				m[o.k] = o.v
			}
		}
		n := 0
		for _, v := range m {
			n += len(v)
		}
		if kc, tb, _ := a.Usage("Q"); kc != len(m) || tb != n || kc > 3 || tb > 20 {
			t.Fatalf("Usage=(%d,%d) naive=(%d,%d)", kc, tb, len(m), n)
		}
	}
}

// TestRejectionsLeaveNoTrace 钉住不变量 4：四类错误可判定互不相同、拒绝不留痕、之后可用。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	a := api.New("sekret")
	a.Register("X", 1, 4)
	a.Put("X", "x", "ab")
	mr := func(want error, f func() error) {
		s := snap(a)
		if f() != want || snap(a) != s {
			t.Fatalf("wrong error or left a trace, want %v", want)
		}
	}
	mr(api.ErrNoTenant, func() error { return a.Put("ghost", "k", "v") })
	mr(api.ErrNoTenant, func() error { _, e := a.Get("ghost", "k"); return e })
	mr(api.ErrNoTenant, func() error { return a.Del("ghost", "k") })
	mr(api.ErrEmptyKey, func() error { return a.Put("X", "", "v") })
	mr(api.ErrEmptyKey, func() error { return a.Del("X", "") })
	mr(api.ErrQuotaKeys, func() error { return a.Put("X", "n", "v") })
	mr(api.ErrQuotaBytes, func() error { return a.Put("X", "x", "abcde") })
	mr(api.ErrUnauthorized, func() error { return a.Purge("bad", "X") })
	mr(api.ErrDuplicateTenant, func() error { return a.Register("X", 1, 4) })
	d := []error{api.ErrNoTenant, api.ErrEmptyKey, api.ErrQuotaKeys, api.ErrUnauthorized}
	if d[0] == d[1] || d[0] == d[2] || d[0] == d[3] || d[1] == d[2] || d[1] == d[3] || d[2] == d[3] {
		t.Fatal("the four sentinel errors are not distinct")
	}
	if a.Put("X", "x", "abcd") != nil || a.Del("X", "x") != nil || a.Put("X", "z", "q") != nil {
		t.Fatal("tenant unusable after rejections")
	}
	if e := api.New("sekret2").SelfCheck(); e != nil {
		t.Fatalf("SelfCheck: %v", e)
	}
}

// TestConcurrentReaders 钉住第六节：N goroutine 并发只读 View 逐字段相同，无 sleep。
func TestConcurrentReaders(t *testing.T) {
	a := api.New("admin")
	for i := range 4 {
		id := fmt.Sprintf("t%d", i)
		a.Register(id, 20, 200)
		for j := range 20 {
			a.Put(id, fmt.Sprintf("k%d", j), "v")
		}
	}
	want := snap(a)
	var bad int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				v, e := a.Get("t0", "k0")
				if snap(a) != want || e != nil || v != "v" {
					atomic.StoreInt32(&bad, 1)
				}
			}
		}()
	}
	wg.Wait()
	if bad != 0 {
		t.Fatal("concurrent readers saw inconsistent state")
	}
}
