package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestFeedMatchesBatchRecompute 不变量 1：六步序列+多档规模+随机到达，四项统计逐项等于朴素批量重算。
func TestFeedMatchesBatchRecompute(t *testing.T) {
	type tc struct {
		name  string
		slack int64
		seqs  []int64
	}
	cases := []tc{
		{"six-steps", 10, []int64{10, 10, 5, 12, 11, 13}},
		{"strict-order", 10, []int64{1, 2, 3}},
		{"strict-reverse", 10, []int64{5, 4, 3, 2, 1}},
		{"zero-slack", 0, []int64{3, 1, 4, 1, 5}},
		{"duplicates", 2, []int64{7, 7, 7, 6}},
	}
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 2, 4, 8, 16, 32, 64} { // 多档规模
		for g := 0; g < 4; g++ { // 随机到达顺序
			s := make([]int64, n)
			for i, v := range rng.Perm(n) {
				s[i] = int64(v) + 1
			}
			cases = append(cases, tc{"rand", rng.Int63n(5), s})
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := New(c.slack)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range c.seqs {
				if err := o.Feed(s); err != nil {
					t.Fatal(err)
				}
			}
			mx, on, ml, ex := batch(c.seqs, c.slack)
			if o.MaxSeen() != mx || o.OutOfOrder() != on || o.MaxLateness() != ml || o.ExceedsSlack() != ex {
				t.Fatalf("seqs=%v: got (%d,%d,%d,%v) want (%d,%d,%d,%v)", c.seqs, o.MaxSeen(), o.OutOfOrder(), o.MaxLateness(), o.ExceedsSlack(), mx, on, ml, ex)
			}
		})
	}
}

// TestMaxLatenessHistorical 不变量 2：迟到量非单调时取历史最大（5 不被 1 覆盖）。
func TestMaxLatenessHistorical(t *testing.T) {
	o, _ := New(10)
	steps := []struct{ s, wantLate, wantOOO int64 }{
		{10, 0, 0}, {10, 0, 0}, {5, 5, 1}, {12, 5, 1}, {11, 5, 2}, {13, 5, 2},
	}
	for i, st := range steps {
		if err := o.Feed(st.s); err != nil {
			t.Fatal(err)
		}
		if o.MaxLateness() != st.wantLate || o.OutOfOrder() != st.wantOOO {
			t.Fatalf("step %d: got late=%d ooo=%d, want %d,%d", i+1, o.MaxLateness(), o.OutOfOrder(), st.wantLate, st.wantOOO)
		}
	}
}

// TestLateEventsNeverDropped 不变量 3：超 slack 的乱序事件仍计数，仅置标志位。
func TestLateEventsNeverDropped(t *testing.T) {
	o, _ := New(3)
	for _, s := range []int64{10, 5, 11, 1} {
		if err := o.Feed(s); err != nil {
			t.Fatal(err)
		}
	}
	if o.OutOfOrder() != 2 || o.MaxLateness() != 10 || !o.ExceedsSlack() || o.MaxSeen() != 11 {
		t.Fatalf("got (%d,%d,%v,%d), want (2,10,true,11)", o.OutOfOrder(), o.MaxLateness(), o.ExceedsSlack(), o.MaxSeen())
	}
}

// TestRejectedOpsLeaveNoTrace 不变量 4：三类拒绝互不相同、拒绝前后状态一致、冻结态保持、被拒后仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrInvalidSlack) {
		t.Fatalf("New(-1)=%v", err)
	}
	o, _ := New(10)
	o.Feed(10)
	o.Feed(5)
	snap := func() string {
		return fmt.Sprintf("%d/%d/%d/%v", o.MaxSeen(), o.OutOfOrder(), o.MaxLateness(), o.ExceedsSlack())
	}
	before := snap()
	for _, s := range []int64{0, -7} {
		if err := o.Feed(s); !errors.Is(err, ErrInvalidSeq) || snap() != before {
			t.Fatalf("Feed(%d) err=%v snap=%s want %s", s, err, snap(), before)
		}
	}
	if err := o.Feed(12); err != nil { // 拒绝后仍可正常使用
		t.Fatal(err)
	}
	before = snap()
	o.Freeze()
	for _, s := range []int64{13, 1} { // 冻结后写入被拒，不改状态，第二次证明仍冻结
		if err := o.Feed(s); !errors.Is(err, ErrFrozen) || snap() != before {
			t.Fatalf("Feed(%d) err=%v snap=%s want %s", s, err, snap(), before)
		}
	}
	if errors.Is(ErrInvalidSeq, ErrFrozen) || errors.Is(ErrFrozen, ErrInvalidSlack) || errors.Is(ErrInvalidSeq, ErrInvalidSlack) {
		t.Fatal("sentinel errors not distinct")
	}
}

func TestSelfCheck(t *testing.T) {
	o, _ := New(10)
	if err := o.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestConcurrentReaders N 个 goroutine 并发只读同一喂满实例，四项逐项相同；无 sleep，关 channel 对齐起跑。
func TestConcurrentReaders(t *testing.T) {
	o, _ := New(10)
	for _, s := range []int64{10, 10, 5, 12, 11, 13, 2, 14} {
		o.Feed(s)
	}
	const n = 64
	nums := make([][3]int64, n)
	exs := make([]bool, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			nums[i] = [3]int64{o.MaxSeen(), o.OutOfOrder(), o.MaxLateness()}
			exs[i] = o.ExceedsSlack()
		}(i)
	}
	close(start)
	wg.Wait()
	wn := [3]int64{14, 3, 11}
	for i := 0; i < n; i++ {
		if nums[i] != wn || !exs[i] {
			t.Fatalf("reader %d=%v,%v want %v,true", i, nums[i], exs[i], wn)
		}
	}
}
