// 演示程序：逐条打印 OK/FAIL，全部通过退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/agg"
	"ontology/api"
	"ontology/grp"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

// render 按值渲染存在集合（与指针地址无关）。
func render(gs []api.GroupState) string {
	s := ""
	for _, g := range gs {
		if g.Group == nil {
			s += fmt.Sprintf("NULL=%d;", g.Count)
		} else {
			s += fmt.Sprintf("%q=%d;", *g.Group, g.Count)
		}
	}
	return s
}

func main() {
	empty, a, b := "", "a", "b"
	kn, ke, ka, kb := grp.Of(nil), grp.Of(&empty), grp.Of(&a), grp.Of(&b)
	check("grp: NULL/空串/普通串三态互异且有序",
		kn != ke && ke != ka && kn != ka &&
			grp.Of(&a) == ka && // 同值即同键
			grp.Less(kn, ke) && grp.Less(ke, ka) && grp.Less(ka, kb))

	// agg：归零即删 + 负计数拒绝且不留痕
	m := agg.New()
	s := "x"
	kx := grp.Of(&s)
	ok := m.ApplyBatch(map[grp.Key]int64{kx: 5}) == nil &&
		m.ApplyBatch(map[grp.Key]int64{kx: -5}) == nil &&
		m.Count(kx) == 0 && len(m.Keys()) == 0 // 归零即删
	err := m.ApplyBatch(map[grp.Key]int64{kx: -1})
	check("agg: 归零即删、ErrNegative 拒绝且不留痕",
		ok && errors.Is(err, agg.ErrNegative) && len(m.Keys()) == 0)

	// agg：大 m 下单次定位检查个数不随 m 增长
	bounded := true
	for _, scale := range []int{100, 1000, 10000} {
		mm := agg.New()
		batch := map[grp.Key]int64{}
		for i := 0; i < scale; i++ {
			t := fmt.Sprintf("g%05d", i)
			batch[grp.Of(&t)] = 1
		}
		_ = mm.ApplyBatch(batch)
		hit := "g00000"
		_ = mm.ApplyBatch(map[grp.Key]int64{grp.Of(&hit): 1})
		bounded = bounded && mm.LastLocateChecksAtMost(4)
	}
	check("agg: 大 m 下定位检查个数有界(哈希定位)", bounded)

	// api：第三节八步序列，逐步核对四个组计数与存在集合
	sp := func(s string) *string { return &s }
	steps := []api.Event{
		{Group: nil, Delta: 2}, {Group: sp(""), Delta: 1},
		{Group: sp("a"), Delta: 3}, {Group: nil, Delta: 1},
		{Group: sp("b"), Delta: 5}, {Group: sp(""), Delta: -1},
		{Group: sp("a"), Delta: -2}, {Group: nil, Delta: -3},
	}
	want := [][4]int64{
		{2, 0, 0, 0}, {2, 1, 0, 0}, {2, 1, 3, 0}, {3, 1, 3, 0},
		{3, 1, 3, 5}, {3, 0, 3, 5}, {3, 0, 1, 5}, {0, 0, 1, 5},
	}
	v, _ := api.New(16)
	stepsOK := true
	for i, e := range steps {
		if v.Feed([]api.Event{e}) != nil {
			stepsOK = false
		}
		got := [4]int64{v.Count(nil), v.Count(sp("")), v.Count(sp("a")), v.Count(sp("b"))}
		nz := 0
		for _, c := range want[i] {
			if c != 0 {
				nz++
			}
		}
		stepsOK = stepsOK && got == want[i] && len(v.Groups()) == nz
	}
	check("api: 八步序列逐步计数与存在集合(NULL/空串独立、归零即删)", stepsOK)

	// api：三类可判定错误互不相同，被拒后状态不变
	snap0 := render(v.Groups())
	e1 := v.Feed([]api.Event{{Group: nil, Delta: -1}})
	e2 := v.Feed([]api.Event{{Group: sp("0123456789abcdefg"), Delta: 1}})
	_, e3 := api.New(0)
	check("api: ErrNegative/ErrTooLong/ErrBadParam 可判定且互异",
		errors.Is(e1, agg.ErrNegative) && errors.Is(e2, api.ErrTooLong) &&
			errors.Is(e3, api.ErrBadParam) &&
			!errors.Is(e1, api.ErrTooLong) && !errors.Is(e2, agg.ErrNegative))
	check("api: 被拒后状态不变、仍可正常使用",
		render(v.Groups()) == snap0 && v.Feed([]api.Event{{Group: sp("a"), Delta: 1}}) == nil)

	// api：SelfCheck + 并发只读视图逐字段相同
	w, _ := api.New(16)
	_ = w.Feed(steps)
	snap := fmt.Sprint(w.Count(nil), render(w.Groups()))
	var wg sync.WaitGroup
	same := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		go func() {
			defer wg.Done()
			same <- fmt.Sprint(w.Count(nil), render(w.Groups())) == snap && w.SelfCheck() == nil
		}()
	}
	wg.Wait()
	close(same)
	conc := true
	for ok := range same {
		conc = conc && ok
	}
	check("api: SelfCheck 通过、8 路并发只读结果一致", conc)

	if failed {
		os.Exit(1)
	}
}
