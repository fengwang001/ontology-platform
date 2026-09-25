package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/reorder"
	"ontology/seq"
)

func sorted(es []seq.Event) bool {
	return sort.SliceIsSorted(es, func(i, j int) bool { return es[i].Seq < es[j].Seq })
}

// TestNaiveReplayRandom 多档参数×随机种子做 Feed/Tick 交错后再 Tick 清空，View 须等于「被接受事件升序、跳过丢失」的朴素重放（Seq/Value 逐条相同）。
func TestNaiveReplayRandom(t *testing.T) {
	cases := []struct{ mb, to, seed int }{{1, 0, 1}, {2, 1, 2}, {3, 2, 3}, {8, 3, 4}, {16, 1, 5}}
	for _, c := range cases {
		b, _ := api.New(c.mb, c.to)
		r := rand.New(rand.NewSource(int64(c.seed)))
		acc := map[int64]seq.Event{}
		for _, p := range r.Perm(300) { // 1..300 的随机排列模拟乱序到达
			if r.Intn(3) == 0 {
				b.Tick()
			}
			s := int64(p) + 1
			ev := seq.Event{Seq: s, Value: int(s)*3 + 1}
			if _, e := b.Feed(ev); e == nil {
				acc[s] = ev
			}
			if v := b.View(); !sorted(v) {
				t.Fatalf("mb=%d 非严格递增: %v", c.mb, v)
			}
		}
		for range [2000]struct{}{} {
			b.Tick()
		} // 足够多次 Tick 清空所有缺口
		lost := map[int64]bool{}
		for _, s := range b.Lost() {
			lost[s] = true
		}
		ks := make([]int64, 0, len(acc)) // 朴素重放参照：接受事件升序、跳过丢失
		for s := range acc {
			if !lost[s] {
				ks = append(ks, s)
			}
		}
		sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
		want := make([]seq.Event, 0, len(ks))
		for _, s := range ks {
			want = append(want, acc[s])
		}
		if got := b.View(); !reflect.DeepEqual(want, got) {
			t.Fatalf("mb=%d to=%d 朴素重放不一致\ngot=%v", c.mb, c.to, got)
		}
	}
}

// TestEmittedStrictlyIncreasing 乱序/超时/溢出交错下，每次操作后 View 都严格递增。
func TestEmittedStrictlyIncreasing(t *testing.T) {
	for _, mb := range []int{1, 2, 3, 7} {
		b, _ := api.New(mb, 2)
		r := rand.New(rand.NewSource(int64(mb) + 99))
		for k := 0; k < 400; k++ {
			if r.Intn(4) == 0 {
				b.Tick()
			} else {
				b.Feed(seq.Event{Seq: int64(1 + r.Intn(60)), Value: k})
			}
			if v := b.View(); !sorted(v) {
				t.Fatalf("mb=%d k=%d: %v", mb, k, v)
			}
		}
	}
}

// TestSentinelsAndRejections 四类哨兵可判定且互不相同；被拒后状态不变、实例仍可用；
// 参数非法返回 ErrBadParams 且不产生对象；SelfCheck 通过。
func TestSentinelsAndRejections(t *testing.T) {
	b, _ := api.New(1, 0)
	_, ei := b.Feed(seq.Event{Seq: 0})
	b.Feed(seq.Event{Seq: 1})
	_, ee := b.Feed(seq.Event{Seq: 1})
	b.Feed(seq.Event{Seq: 3})
	preV, preL := b.View(), b.Lost()
	_, eo := b.Feed(seq.Event{Seq: 4}) // 缓冲已占满
	if !errors.Is(ei, seq.ErrInvalid) || !errors.Is(ee, seq.ErrExpired) || !errors.Is(eo, reorder.ErrOverflow) {
		t.Fatalf("三类错误判定失败: %v %v %v", ei, ee, eo)
	}
	if !reflect.DeepEqual(preV, b.View()) || !reflect.DeepEqual(preL, b.Lost()) {
		t.Fatal("溢出被拒后状态发生变化")
	}
	if _, e := b.Feed(seq.Event{Seq: 2}); e != nil { // 命中并级联发 3，仍可用
		t.Fatalf("被拒后实例不可用: %v", e)
	}
	for _, c := range [][2]int{{0, 1}, {1, -1}, {-1, 0}, {-3, -9}} {
		if x, e := api.New(c[0], c[1]); !errors.Is(e, reorder.ErrBadParams) || x != nil {
			t.Fatalf("New(%v)=%v,%v", c, x, e)
		}
	}
	if ei == ee || ei == eo || ee == eo || ei == reorder.ErrBadParams ||
		ee == reorder.ErrBadParams || eo == reorder.ErrBadParams {
		t.Fatal("四类哨兵错误不互不相同")
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentViewConsistent 喂满并 Tick 清空后 N 个 goroutine 并发读同一实例，View 逐字段相同并并发 Lost/SelfCheck；无 sleep、channel 同步，-race 下核验。
func TestConcurrentViewConsistent(t *testing.T) {
	const N = 64
	b, _ := api.New(N, 1)
	for s := int64(2); s <= N+1; s++ { // 缺口在 1，缓冲恰好喂满 N 条
		b.Feed(seq.Event{Seq: s, Value: int(s)})
	}
	if _, e := b.Feed(seq.Event{Seq: N + 2}); !errors.Is(e, reorder.ErrOverflow) {
		t.Fatalf("缓冲应已满: %v", e)
	}
	b.Tick() // 超时丢 1、next=2，级联清空全部 N 条
	base := b.View()
	if len(base) != N || !reflect.DeepEqual(b.Lost(), []int64{1}) {
		t.Fatalf("清空形态错 len=%d lost=%v", len(base), b.Lost())
	}
	res := make([][]seq.Event, N)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			res[g] = b.View()
			_ = b.Lost()
			_ = b.SelfCheck()
		}(g)
	}
	close(start)
	wg.Wait()
	for g, v := range res {
		if !reflect.DeepEqual(base, v) {
			t.Fatalf("goroutine %d 的 View 与基准不一致", g)
		}
	}
}
