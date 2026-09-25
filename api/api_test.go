package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/reorder"
	"ontology/seq"
)

func drain(b *api.Buffer, maxTicks int) {
	for i := 0; i <= maxTicks; i++ {
		if _, _, bf := b.State(); len(bf) == 0 {
			return
		}
		b.Tick()
	}
}

// TestNaiveReplay 不变量1：多档规模、随机到达顺序下与朴素重放逐条一致。
func TestNaiveReplay(t *testing.T) {
	cases := []struct{ maxB, timeout, n int }{{1, 0, 60}, {3, 2, 300}, {8, 3, 800}, {16, 5, 1500}}
	for seed, c := range cases {
		b, _ := api.New(c.maxB, c.timeout)
		r := rand.New(rand.NewSource(int64(seed)))
		var acc []seq.Event
		for _, p := range r.Perm(c.n) {
			ev := seq.Event{Seq: int64(p + 1), Value: p}
			if _, err := b.Feed(ev); err == nil {
				acc = append(acc, ev)
			}
			if r.Intn(3) == 0 {
				b.Tick()
			}
		}
		drain(b, c.n*(c.timeout+1))
		sort.Slice(acc, func(i, j int) bool { return acc[i].Seq < acc[j].Seq })
		lost := map[int64]bool{}
		for _, s := range b.Lost() {
			lost[s] = true
		}
		var want []seq.Event
		for _, ev := range acc {
			if !lost[ev.Seq] {
				want = append(want, ev)
			}
		}
		if got := b.View(); !reflect.DeepEqual(got, want) {
			t.Fatalf("配置%+v 与朴素重放不一致", c)
		}
	}
}

// TestStrictlyIncreasing 不变量2：发出序列相邻 Seq 严格递增。
func TestStrictlyIncreasing(t *testing.T) {
	b, _ := api.New(3, 2)
	r := rand.New(rand.NewSource(9))
	for _, p := range r.Perm(200) {
		b.Feed(seq.Event{Seq: int64(p + 1)})
		if p%7 == 0 {
			b.Tick()
		}
	}
	drain(b, 600)
	v := b.View()
	for i := 1; i < len(v); i++ {
		if v[i].Seq <= v[i-1].Seq {
			t.Fatalf("第%d条起非严格递增: %v", i, v[i-1:])
		}
	}
}

// TestFailureAtomic 不变量4：四类错误可判定、互不相同、被拒后状态不变且仍可用。
func TestFailureAtomic(t *testing.T) {
	b, _ := api.New(2, 1)
	for _, s := range []int64{1, 3, 4} {
		b.Feed(seq.Event{Seq: s, Value: int(s)})
	}
	snap := func() string {
		now, next, bf := b.State()
		return fmt.Sprintf("%d|%d|%v|%v|%v", now, next, bf, b.View(), b.Lost())
	}
	before := snap()
	rejSeqs := []int64{0, 1, 5} // 非法 / 过期 / 溢出
	rejErrs := []error{seq.ErrInvalidSeq, seq.ErrStale, reorder.ErrOverflow}
	for i, s := range rejSeqs {
		if _, err := b.Feed(seq.Event{Seq: s}); !errors.Is(err, rejErrs[i]) {
			t.Fatalf("Feed(%d) 应报 %v, 得 %v", s, rejErrs[i], err)
		}
		if snap() != before {
			t.Fatalf("Feed(%d) 被拒后状态改变", s)
		}
	}
	for _, p := range [][2]int{{0, 1}, {1, -1}} {
		if _, e := api.New(p[0], p[1]); !errors.Is(e, api.ErrBadParam) {
			t.Fatalf("参数%v 未报参数非法: %v", p, e)
		}
	}
	if errors.Is(seq.ErrInvalidSeq, seq.ErrStale) || errors.Is(seq.ErrInvalidSeq, reorder.ErrOverflow) ||
		errors.Is(seq.ErrInvalidSeq, api.ErrBadParam) || errors.Is(seq.ErrStale, reorder.ErrOverflow) ||
		errors.Is(seq.ErrStale, api.ErrBadParam) || errors.Is(reorder.ErrOverflow, api.ErrBadParam) {
		t.Fatal("四类哨兵错误不互异")
	}
	if out, err := b.Feed(seq.Event{Seq: 2, Value: 2}); err != nil || len(out) != 3 {
		t.Fatalf("拒绝后实例不可用: %v %v", out, err)
	}
}

// TestConcurrentView N 个 goroutine 并发读同一实例的 View/Lost/SelfCheck，结果逐字段相同。
func TestConcurrentView(t *testing.T) {
	b, _ := api.New(4, 2)
	r := rand.New(rand.NewSource(5))
	for _, p := range r.Perm(120) {
		b.Feed(seq.Event{Seq: int64(p + 1), Value: p})
		if p%3 == 0 {
			b.Tick()
		}
	}
	drain(b, 360)
	want := b.View()
	got := make([][]seq.Event, 8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			got[g], _ = b.View(), b.Lost()
			if err := b.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := range got {
		if !reflect.DeepEqual(got[g], want) {
			t.Fatalf("goroutine %d 的 View 不一致", g)
		}
	}
}
