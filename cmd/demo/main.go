package main

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sort"
	"sync"

	"ontology/api"
	"ontology/reorder"
	"ontology/seq"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%v %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func seqs(evs []seq.Event) (s []int64) {
	for _, e := range evs {
		s = append(s, e.Seq)
	}
	return
}

func drain(b *api.Buffer, n int) {
	for i := 0; i <= n; i++ {
		if _, _, bf := b.State(); len(bf) == 0 {
			return
		}
		b.Tick()
	}
}

func main() {
	b, _ := api.New(3, 2) // 十一步逐步核验（NOTES.md 分步表）
	type step struct {
		feed, now, next int64
		buf, out, lost  []int64
		err             error
	}
	steps := []step{
		{1, 0, 2, nil, []int64{1}, nil, nil}, {3, 0, 2, []int64{3}, nil, nil, nil}, {5, 0, 2, []int64{3, 5}, nil, nil, nil},
		{0, 1, 2, []int64{3, 5}, nil, nil, nil}, {0, 2, 4, []int64{5}, []int64{3}, []int64{2}, nil},
		{2, 2, 4, []int64{5}, nil, []int64{2}, seq.ErrStale}, {4, 2, 6, nil, []int64{4, 5}, []int64{2}, nil},
		{8, 2, 6, []int64{8}, nil, []int64{2}, nil}, {9, 2, 6, []int64{8, 9}, nil, []int64{2}, nil},
		{10, 2, 6, []int64{8, 9, 10}, nil, []int64{2}, nil}, {11, 2, 6, []int64{8, 9, 10}, nil, []int64{2}, reorder.ErrOverflow},
	}
	okStep := make([]bool, len(steps))
	for i, st := range steps {
		o, err := b.Feed(seq.Event{Seq: st.feed}) // feed==0 时此次必被拒且不留痕
		if st.feed == 0 {
			o, err = b.Tick()
		}
		now, next, buf := b.State()
		okStep[i] = errors.Is(err, st.err) && now == st.now && next == st.next &&
			reflect.DeepEqual(seqs(buf), st.buf) && reflect.DeepEqual(seqs(o), st.out) &&
			reflect.DeepEqual(b.Lost(), st.lost)
	}
	check("十一步逐步 now/next/缓冲/发出/丢失/报错", !slices.Contains(okStep, false))
	check("第5步超时/第7步级联/第11步溢出判定", okStep[4] && okStep[6] && okStep[10])
	nb, _ := api.New(8, 3) // 朴素重放一致 + 严格递增 + 缓冲不超界
	r := rand.New(rand.NewSource(7))
	var acc []seq.Event
	bound := true
	for i, p := range r.Perm(300) {
		ev := seq.Event{Seq: int64(p + 1), Value: p}
		if _, err := nb.Feed(ev); err == nil {
			acc = append(acc, ev)
		}
		if _, _, bf := nb.State(); len(bf) > 8 {
			bound = false
		}
		if i%5 == 4 {
			nb.Tick()
		}
	}
	drain(nb, 1200)
	sort.Slice(acc, func(i, j int) bool { return acc[i].Seq < acc[j].Seq })
	want := acc[:0]
	for _, ev := range acc {
		if !slices.Contains(nb.Lost(), ev.Seq) {
			want = append(want, ev)
		}
	}
	got := nb.View()
	check("与朴素重放一致", reflect.DeepEqual(got, want))
	check("顺序严格递增", slices.IsSortedFunc(got, func(x, y seq.Event) int { return cmp.Compare(x.Seq, y.Seq) }))
	check("缓冲不超界", bound)
	fb, _ := api.New(2, 1) // 四类可判定错误 + 被拒后状态不变
	for _, s := range []int64{1, 3, 4} {
		fb.Feed(seq.Event{Seq: s})
	}
	snap := func() string {
		now, next, bf := fb.State()
		return fmt.Sprintf("%d|%d|%v|%v|%v", now, next, bf, fb.View(), fb.Lost())
	}
	before := snap()
	_, e1 := fb.Feed(seq.Event{Seq: 0})
	_, e2 := fb.Feed(seq.Event{Seq: 1})
	_, e3 := fb.Feed(seq.Event{Seq: 5})
	_, e4 := api.New(0, 1)
	errs := []error{seq.ErrInvalidSeq, seq.ErrStale, reorder.ErrOverflow, api.ErrBadParam}
	distinct := !errors.Is(errs[0], errs[1]) && !errors.Is(errs[0], errs[2]) && !errors.Is(errs[0], errs[3]) &&
		!errors.Is(errs[1], errs[2]) && !errors.Is(errs[1], errs[3]) && !errors.Is(errs[2], errs[3])
	check("四类可判定错误", errors.Is(e1, errs[0]) && errors.Is(e2, errs[1]) &&
		errors.Is(e3, errs[2]) && errors.Is(e4, errs[3]) && distinct)
	check("被拒后状态不变", snap() == before)
	hb, _ := api.New(10000, 1) // 大 m：堆定位最小 Seq（计数断言见 reorder 内部测试）
	for i := 1; i <= 10000; i++ {
		hb.Feed(seq.Event{Seq: int64(2 * i), Value: i})
	}
	ho, _ := hb.Tick()
	check("大m比较条数不随m增长(堆)", len(ho) == 1 && ho[0].Seq == 2)
	cb, _ := api.New(120, 2) // 并发读同一实例结果一致
	cr := rand.New(rand.NewSource(5))
	for _, p := range cr.Perm(120) {
		cb.Feed(seq.Event{Seq: int64(p + 1), Value: p})
	}
	drain(cb, 360)
	cgot := make([][]seq.Event, 8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			cgot[g], _ = cb.View(), cb.Lost()
		}(g)
	}
	close(start)
	wg.Wait()
	cwant := cb.View()
	same := cb.SelfCheck() == nil && !slices.ContainsFunc(cgot, func(g []seq.Event) bool { return !reflect.DeepEqual(g, cwant) })
	check("并发读结果一致/SelfCheck", same)
	if failed {
		os.Exit(1)
	}
}
