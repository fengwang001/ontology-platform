// Command demo 逐条演示多租户命名空间与配额隔离的关键判定，退出码 0。
package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var fails int

func line(ok bool, name string) {
	p := "OK: "
	if !ok {
		p, fails = "FAIL: ", fails+1
	}
	fmt.Println(p + name)
}

func sig(a *api.API) string {
	var b strings.Builder
	for _, v := range a.View() {
		fmt.Fprintf(&b, "%s:%d:%d:%v|", v.ID, v.KeyCount, v.TotalBytes, slices.Sorted(maps.Keys(v.Items)))
	}
	return b.String()
}

func vw(a *api.API, id string) (api.TenantView, bool) {
	for _, v := range a.View() {
		if v.ID == id {
			return v, true
		}
	}
	return api.TenantView{}, false
}

func main() {
	a := api.New("sekret")

	// 第三节八步：逐步核对两租户配额与结果（第八步为两个 Get）。
	steps := []struct {
		f                  func() error
		err                error
		akc, atb, bkc, btb int
	}{
		{func() error { return a.Register("A", 2, 100) }, nil, 0, 0, 0, 0},
		{func() error { return a.Register("B", 2, 100) }, nil, 0, 0, 0, 0},
		{func() error { return a.Put("A", "x", "1") }, nil, 1, 1, 0, 0},
		{func() error { return a.Put("A", "y", "22") }, nil, 2, 3, 0, 0},
		{func() error { return a.Put("A", "z", "333") }, api.ErrQuotaKeys, 2, 3, 0, 0},
		{func() error { return a.Put("A", "x", "4444") }, nil, 2, 6, 0, 0},
		{func() error { return a.Put("B", "x", "B") }, nil, 2, 6, 1, 1},
	}
	ok8 := true
	for i, s := range steps {
		if s.f() != s.err {
			ok8 = false
		}
		va, _ := vw(a, "A")
		vb, has := vw(a, "B")
		if va.KeyCount != s.akc || va.TotalBytes != s.atb || has != (i >= 1) ||
			(has && (vb.KeyCount != s.bkc || vb.TotalBytes != s.btb)) {
			ok8 = false
		}
	}
	g1, e1 := a.Get("A", "x")
	g2, e2 := a.Get("B", "x")
	if e1 != nil || e2 != nil || g1 != "4444" || g2 != "B" {
		ok8 = false
	}
	line(ok8, "八步: 两租户配额/结果与推导表逐行一致")

	ref := map[string]map[string]string{"A": {"x": "4444", "y": "22"}, "B": {"x": "B"}}
	naive := true
	for id, m := range ref {
		v, _ := vw(a, id)
		if !maps.Equal(v.Items, m) {
			naive = false
		}
	}
	line(naive, "View 与每租户朴素 map 参照一致")

	a.Register("C", 10, 3)
	cases := []struct {
		f    func() error
		want error
	}{
		{func() error { return a.Put("ghost", "k", "v") }, api.ErrNoTenant},
		{func() error { return a.Put("A", "", "v") }, api.ErrEmptyKey},
		{func() error { return a.Put("C", "k", "1234") }, api.ErrQuotaBytes},
		{func() error { return a.SetQuota("bad", "C", 10, 9) }, api.ErrUnauthorized},
	}
	errOK, traceOK, seen := true, true, map[error]bool{}
	for _, c := range cases {
		before := sig(a)
		if c.f() != c.want || sig(a) != before {
			errOK, traceOK = false, false
		}
		if seen[c.want] {
			errOK = false
		}
		seen[c.want] = true
	}
	traceOK = a.Put("C", "k", "12") == nil && traceOK
	line(errOK, "四类错误(未注册/空key/配额/越权)可判定且互不相同")
	line(traceOK, "被拒操作不留痕且租户后续可用")

	constBig := true
	for _, m := range []int{100, 1000, 10000} {
		id := fmt.Sprintf("big%d", m)
		constBig = a.Register(id, m, 1<<30) == nil && constBig
		for i := range m {
			constBig = a.Put(id, fmt.Sprintf("k%d", i), "v") == nil && constBig
		}
		constBig = a.Put(id, "k0", "w") == nil && constBig
		kc, tb, e := a.Usage(id)
		constBig = e == nil && kc == m && tb == m && constBig
	}
	line(constBig, "大 m(100/1000/10000) 满配额覆盖已存在 key 成功且计数恒定")

	// 并发只读：16 goroutine 的 View/Get/Usage 逐字段相同，无 sleep。
	base := sig(a)
	var same int32 = 1
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				v, e := a.Get("A", "x")
				k, b, ue := a.Usage("B")
				if sig(a) != base || e != nil || v != "4444" || ue != nil || k != 1 || b != 1 {
					atomic.StoreInt32(&same, 0)
				}
			}
		}()
	}
	wg.Wait()
	line(same == 1 && api.New("sekret2").SelfCheck() == nil,
		"16 goroutine 并发只读逐字段一致; SelfCheck 通过")

	if fails != 0 {
		panic("demo failed")
	}
}
