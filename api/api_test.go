package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func ok(t *testing.T, cond bool, msg string, a ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(msg, a...)
	}
}

// runScenario 对分区 0 做随机 Deliver/Ack 交错直到 n 条全 Ack，每步核验不变量 1/2/3。
func runScenario(t *testing.T, seed int64, mif, n int) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	c := api.New(mif)
	c.Assign(0, 7)
	ack := map[int64]bool{}
	var hi int64 = 7
	for d, a := 0, 0; d < n || a < n; {
		C := c.Commit()[0]
		canD := d < n && (mif <= 0 || int64(d) < C-7+int64(mif))
		switch {
		case canD && (a == d || r.Intn(2) == 0):
			off := 7 + int64(d)
			ok(t, c.Deliver(0, off) == nil, "deliver %d", off)
			hi, d = hi+1, d+1
		case a < d:
			off := 7 + r.Int63n(int64(d))
			if !ack[off] {
				ok(t, c.Ack(0, off) == nil, "ack %d", off)
				ack[off], a = true, a+1
			}
		}
		nv := int64(7) // 朴素扫描：遇首个未 Ack 位点即停
		for ack[nv] {
			nv++
		}
		got := c.Commit()[0]
		ok(t, got == nv, "seed %d C=%d naive=%d", seed, got, nv)
		ok(t, got >= hi || !ack[got], "seed %d C=%d 非极大", seed, got)
	}
}

func scenarios(t *testing.T, mif int, seeds ...int64) {
	for _, s := range seeds {
		runScenario(t, s, mif, 200)
	}
}

// TestAtLeastOnce 钉不变量 1。
func TestAtLeastOnce(t *testing.T) { scenarios(t, 0, 1, 2, 3) }

// TestMaximal 钉不变量 2。
func TestMaximal(t *testing.T) { scenarios(t, 50, 11, 12) }

// TestNaiveReference 钉不变量 3，多档在途上限。
func TestNaiveReference(t *testing.T) {
	seeds, mifs := []int64{21, 22, 23, 24}, []int{0, 1, 8, 64}
	for i, s := range seeds {
		t.Run(fmt.Sprintf("seed=%d/mif=%d", s, mifs[i]), func(t *testing.T) { runScenario(t, s, mifs[i], 200) })
	}
}

// TestRejectedLeavesNoTrace 钉不变量 4：哨兵互异、拒绝不改态、拒后可用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	sen := []error{api.ErrPartitionNotAssigned, api.ErrDeliverGap, api.ErrAckOutOfRange, api.ErrTooManyInFlight}
	ok(t, sen[0] != sen[1] && sen[0] != sen[2] && sen[0] != sen[3] && sen[1] != sen[2] && sen[1] != sen[3] && sen[2] != sen[3], "四类哨兵错误不互异")
	c := api.New(1)
	c.Assign(0, 10)
	type rc struct {
		e error
		f func() error
	}
	cases := []rc{
		{api.ErrPartitionNotAssigned, func() error { return c.Ack(9, 0) }},
		{api.ErrDeliverGap, func() error { return c.Deliver(0, 11) }},
		{nil, func() error { return c.Deliver(0, 10) }},
		{api.ErrTooManyInFlight, func() error { return c.Deliver(0, 11) }},
		{api.ErrAckOutOfRange, func() error { return c.Ack(0, 11) }},
		{nil, func() error { return c.Ack(0, 9) }}, // < 起点的 Ack 幂等成功
	}
	for i, tc := range cases {
		before := c.Commit()[0]
		ok(t, errors.Is(tc.f(), tc.e), "case %d 错误不匹配", i)
		ok(t, c.Commit()[0] == before, "case %d 拒绝后改了状态", i)
	}
	ok(t, c.Ack(0, 10) == nil, "释放槽位失败")
	ok(t, c.Deliver(0, 11) == nil, "被拒后分区不可用")
}

func monotonicReader(t *testing.T, c *api.Committer, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	prev := int64(-1)
	for {
		select {
		case <-stop:
			return
		default:
			got := c.Commit()[0]
			if got < prev {
				t.Errorf("Committed 倒退 %d->%d", prev, got)
			}
			prev = got
		}
	}
}

// TestConcurrentAck 钉并发：终态 C=起点+N，期间并发读到的 C 单调不减；多档 N 无 sleep。
func TestConcurrentAck(t *testing.T) {
	for _, N := range []int{1, 2, 100, 1000} {
		t.Run(fmt.Sprintf("N=%d", N), func(t *testing.T) {
			c := api.New(0)
			c.Assign(0, 100)
			for off := 100; off < 100+N; off++ {
				ok(t, c.Deliver(0, int64(off)) == nil, "deliver %d", off)
			}
			order := rand.New(rand.NewSource(int64(N))).Perm(N)
			stop := make(chan struct{})
			var rwg, awg sync.WaitGroup
			rwg.Add(2)
			go monotonicReader(t, c, stop, &rwg)
			go monotonicReader(t, c, stop, &rwg)
			for _, idx := range order {
				awg.Add(1)
				go func(off int64) { defer awg.Done(); _ = c.Ack(0, off) }(100 + int64(idx))
			}
			awg.Wait()
			close(stop)
			rwg.Wait()
			ok(t, c.Commit()[0] == int64(100+N), "C=%d want %d", c.Commit()[0], 100+N)
		})
	}
}

// TestSelfCheck：内置自检通过且不改接收者自身状态。
func TestSelfCheck(t *testing.T) {
	c := api.New(0)
	before := len(c.Commit())
	ok(t, c.SelfCheck() == nil, "SelfCheck 失败: %v", c.SelfCheck())
	ok(t, len(c.Commit()) == before, "SelfCheck 改了接收者状态")
}
