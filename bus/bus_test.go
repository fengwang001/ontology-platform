package bus_test

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/bus"
	"sync"
	"testing"
)

func drain(b *bus.Bus, si int) (out []int64) {
	for v, ok := b.Consume(si); ok; v, ok = b.Consume(si) {
		out = append(out, v)
	}
	return
}
func recErr(fn func()) (e error) {
	defer func() { e, _ = recover().(error) }()
	fn()
	return
}
func replay(b *bus.Bus, ops []int64) {
	for _, x := range ops {
		if x >= 0 {
			b.Publish(x)
		} else {
			b.Consume(int(-x - 1))
		}
	}
}
func thrash(b *bus.Bus) {
	for range make([]struct{}, 300) {
		b.SelfCheck()
		b.DropCount(0)
		b.QueueLen(0)
	}
}
func TestSixStepScenario(t *testing.T) {
	// TestSixStepScenario 钉住第三节推导：六步每个前缀的两队列、丢弃计数与 Consume 返回。
	ops := []int64{1, 2, 3, -1, 4, -2}
	wq := []string{"[1]|[1]", "[1 2]|[1 2]", "[1 2]|[1 2]", "[2]|[1 2]", "[2 4]|[1 2]", "[2 4]|[2]"}
	wd := []string{"0|0", "0|0", "1|1", "1|1", "1|2", "1|2"}
	for k := 1; k <= 6; k++ {
		b, _ := bus.New(2, 2)
		cv, ck := int64(0), false
		for _, x := range ops[:k] {
			if x >= 0 {
				b.Publish(x)
				ck = false
			} else {
				cv, ck = b.Consume(int(-x - 1))
			}
		}
		gq, gd := fmt.Sprintf("%v|%v", drain(b, 0), drain(b, 1)), fmt.Sprintf("%d|%d", b.DropCount(0), b.DropCount(1))
		if gq != wq[k-1] || gd != wd[k-1] || ck != (k == 4 || k == 6) || (ck && cv != 1) {
			t.Fatalf("prefix %d: %s %s (%d,%v)", k, gq, gd, cv, ck)
		}
	}
}
func TestNaiveReference(t *testing.T) {
	// TestNaiveReference 不变量 1：终态由「未满入队、满则丢新」朴素规则手算成表。
	cases := []struct {
		n, c int
		ops  []int64
		q    []string
		d    []int
	}{
		{3, 3, []int64{1, 2, -1, 3, -2, 4, -1}, []string{"[3 4]", "[2 3 4]", "[1 2 3]"}, []int{0, 0, 1}},
		{2, 2, []int64{1, 2, 3, -1, -1, -2, -2, 4}, []string{"[4]", "[4]"}, []int{1, 1}},
		{3, 1, []int64{1, -1, 2, -1, 3, -2}, []string{"[3]", "[]", "[1]"}, []int{0, 2, 2}},
	}
	for _, tc := range cases {
		b, _ := bus.New(tc.n, tc.c)
		replay(b, tc.ops)
		for i := 0; i < tc.n; i++ {
			if fmt.Sprint(drain(b, i)) != tc.q[i] || b.DropCount(i) != tc.d[i] {
				t.Fatalf("case %+v sub %d diverged", tc, i)
			}
		}
	}
}
func TestSlowConsumerIsolation(t *testing.T) {
	// TestSlowConsumerIsolation 不变量 2：s1 满只丢自己，s0 腾位后照常收新事件。
	b, _ := bus.New(2, 1)
	replay(b, []int64{1, 2, -1, 3})
	if fmt.Sprint(drain(b, 0)) != "[3]" || b.DropCount(0) != 1 ||
		fmt.Sprint(drain(b, 1)) != "[1]" || b.DropCount(1) != 2 {
		t.Fatal("slow consumer isolation violated")
	}
}
func TestSentinelErrors(t *testing.T) {
	// TestSentinelErrors 不变量 4：三类错误互不相同、可判定（bus 与 api 两层）。
	for _, x := range []struct {
		n, c int
		e    error
	}{
		{0, 1, bus.ErrInvalidN}, {-3, 1, bus.ErrInvalidN}, {1, 0, bus.ErrInvalidC}, {2, -9, bus.ErrInvalidC},
	} {
		if _, err := api.New(x.n, x.c); !errors.Is(err, x.e) {
			t.Fatalf("New(%d,%d)=%v", x.n, x.c, err)
		}
	}
	if bus.ErrInvalidN == bus.ErrInvalidC || bus.ErrInvalidN == bus.ErrSubscriberIndex || bus.ErrInvalidC == bus.ErrSubscriberIndex {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	b, _ := bus.New(1, 1)
	for _, si := range []int{-1, 1} {
		if !errors.Is(recErr(func() { b.Consume(si) }), bus.ErrSubscriberIndex) {
			t.Fatalf("si=%d was not rejected", si)
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	// TestRejectedOpsLeaveNoTrace 不变量 4：越界被拒后状态原样，仍可正常使用。
	b, _ := bus.New(2, 2)
	replay(b, []int64{1, 2, 3})
	_ = recErr(func() { b.Consume(-1) })
	_ = recErr(func() { b.QueueLen(9) })
	if b.QueueLen(0) != 2 || b.DropCount(0) != 1 || b.DropCount(1) != 1 {
		t.Fatal("rejected op changed state")
	}
	if v, ok := b.Consume(0); !ok || v != 1 {
		t.Fatal("bus unusable after rejection")
	}
}
func TestConcurrentConsume(t *testing.T) {
	// TestConcurrentConsume：8 个 goroutine 各消费不同订阅者（无 sleep），与并发读者竞速；-race 须干净。
	b, _ := bus.New(8, 4)
	replay(b, []int64{0, 1, 2, 3, 4, 5, 6, 7, 8})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); thrash(b) }()
	res := make([][]int64, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			for v, ok := b.Consume(si); ok; v, ok = b.Consume(si) {
				res[si] = append(res[si], v)
			}
		}(i)
	}
	wg.Wait()
	for i, r := range res {
		if fmt.Sprint(r) != "[0 1 2 3]" || b.DropCount(i) != 5 {
			t.Fatalf("subscriber %d: %v drops=%d", i, r, b.DropCount(i))
		}
	}
}
