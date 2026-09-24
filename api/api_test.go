package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// naiveRef 是独立于 obs 的朴素批量重算，作为不变量 1 的外部标尺。
func naiveRef(es []int64) (mx, ooo, late int64) {
	first := true
	for _, v := range es {
		if first || v > mx {
			mx, first = v, false
		} else if v < mx {
			ooo++
			if d := mx - v; d > late {
				late = d
			}
		}
	}
	return
}

type snapshot struct {
	mx, ooo, late int64
	ex            bool
}

func snapOf(o *Observer) snapshot {
	return snapshot{o.MaxSeen(), o.OutOfOrder(), o.MaxLateness(), o.ExceedsSlack()}
}

// TestBatchEquivalence 钉不变量 1：固定表 + 多档规模随机排列，三项统计逐项等于朴素重算。
func TestBatchEquivalence(t *testing.T) {
	cases := [][]int64{
		{10, 10, 5, 12, 11, 13},
		{1, 2, 3, 4}, {7, 7, 7}, {9, 8, 7}, {3, 1, 4, 1, 5, 9, 2, 6},
	}
	check := func(tag int, es []int64) {
		o, err := New(10)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range es {
			if err := o.Feed(v); err != nil {
				t.Fatalf("case %d feed %d: %v", tag, v, err)
			}
		}
		m, c, l := naiveRef(es)
		if g := snapOf(o); g.mx != m || g.ooo != c || g.late != l {
			t.Errorf("case %d got %+v want mx=%d ooo=%d late=%d", tag, g, m, c, l)
		}
	}
	for i, es := range cases { // 表驱动固定序列
		check(i, es)
	}
	for _, n := range []int{5, 50, 500} { // 随机到达顺序：多档规模循环生成
		rng := rand.New(rand.NewSource(int64(n) * 7))
		for trial := 0; trial < 20; trial++ {
			perm := rng.Perm(n) // 1..n 的随机排列，序号恒合法
			es := make([]int64, n)
			for j, p := range perm {
				es[j] = int64(p) + 1
			}
			check(1000+n+trial, es)
		}
	}
	sc, _ := New(10) // 公开 SelfCheck 同样核验四不变量与 O(1)
	if err := sc.SelfCheck(); err != nil {
		t.Fatalf("public SelfCheck: %v", err)
	}
}

// TestNoDropKeepsCount 钉不变量 2、3：迟到量取历史最大(5 非最近 1)；乱序与超 slack 都计数不丢弃。
func TestNoDropKeepsCount(t *testing.T) {
	o, _ := New(2) // slack=2：第 3 步迟到 5 必置 ExceedsSlack，但仍计数
	for _, v := range []int64{10, 10, 5, 12, 11, 13} {
		if err := o.Feed(v); err != nil {
			t.Fatal(err)
		}
	}
	if g := snapOf(o); g != (snapshot{13, 2, 5, true}) {
		t.Fatalf("six-step got %+v want {13 2 5 true}", g)
	}
	if err := o.Feed(1); err != nil || o.OutOfOrder() != 3 || o.MaxLateness() != 12 {
		t.Fatalf("extreme-late event must count: ooo=%d late=%d err=%v", o.OutOfOrder(), o.MaxLateness(), err)
	}
}

// TestRejectedOpsLeaveNoTrace 钉不变量 4：三类哨兵互不相同、拒绝前后全状态不变、被拒后仍可用、冻结粘性。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	o, _ := New(10)
	if err := o.Feed(7); err != nil {
		t.Fatal(err)
	}
	before := snapOf(o)
	errInvalid := o.Feed(0)
	if !errors.Is(errInvalid, ErrInvalidSeq) || snapOf(o) != before {
		t.Fatalf("invalid seq err=%v before=%+v after=%+v", errInvalid, before, snapOf(o))
	}
	if err := o.Feed(8); err != nil || snapOf(o) != (snapshot{8, 0, 0, false}) {
		t.Fatalf("unusable after reject: err=%v snap=%+v", err, snapOf(o))
	}
	if err := o.Freeze(); err != nil {
		t.Fatal(err)
	}
	frozen := snapOf(o)
	errFrozen := o.Feed(9)
	if !errors.Is(errFrozen, ErrFrozen) || snapOf(o) != frozen {
		t.Fatalf("frozen feed err=%v snap=%+v", errFrozen, snapOf(o))
	}
	_, errNeg := New(-1)
	if !errors.Is(errNeg, ErrNegativeSlack) {
		t.Fatalf("New(-1) err=%v", errNeg)
	}
	if errors.Is(errInvalid, ErrFrozen) || errors.Is(errFrozen, ErrInvalidSeq) ||
		errors.Is(errInvalid, ErrNegativeSlack) || errors.Is(errFrozen, ErrNegativeSlack) {
		t.Fatalf("sentinels must be distinct: %v/%v/%v", errInvalid, errFrozen, errNeg)
	}
	if !errors.Is(o.Feed(0), ErrFrozen) || snapOf(o) != frozen { // 冻结优先且粘性
		t.Fatalf("frozen state not sticky: snap=%+v", snapOf(o))
	}
}

// TestConcurrentReadersAgree：N 个 goroutine 并发只读同一已喂满实例，四项逐项相同；WaitGroup/channel 同步，无 sleep。
func TestConcurrentReadersAgree(t *testing.T) {
	o, _ := New(10)
	for _, v := range []int64{10, 10, 5, 12, 11, 13} {
		if err := o.Feed(v); err != nil {
			t.Fatal(err)
		}
	}
	want := snapshot{13, 2, 5, false}
	const n = 32
	got := make(chan snapshot, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); got <- snapOf(o) }()
	}
	wg.Wait()
	close(got)
	for s := range got {
		if s != want {
			t.Errorf("reader got %+v want %+v", s, want)
		}
	}
}
