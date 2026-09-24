package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/cmt"
	"ontology/ofs"
)

// 不变量 1/2/3：任意交错序列后 Committed 等于朴素参照（隐含至少一次与极大性）。
func TestNaiveConsistency(t *testing.T) {
	for _, n := range []int{1, 7, 64} {
		for seed := int64(0); seed < 20; seed++ {
			rng, c := rand.New(rand.NewSource(seed)), api.New(1<<20)
			start := int64(rng.Intn(500))
			c.Assign(0, start)
			acked := map[int64]bool{}
			for i := 0; i < n; i++ {
				c.Deliver(0, start+int64(i))
			}
			for _, i := range rng.Perm(n) {
				off := start + int64(i)
				if rng.Intn(3) == 0 {
					c.Ack(0, off) // 重复 Ack，幂等
				}
				c.Ack(0, off)
				acked[off] = true
				want := start
				for acked[want] {
					want++
				}
				if got, _ := c.Committed(0); got != want {
					t.Fatalf("n=%d seed=%d: C=%d want %d", n, seed, got, want)
				}
			}
		}
	}
}

// 不变量 4：四类错误可判定、互不相同，被拒后状态不变、仍可继续用。
func TestFailuresLeaveNoTrace(t *testing.T) {
	mk := func(max int) *api.Committer {
		c := api.New(max)
		c.Assign(0, 10)
		c.Deliver(0, 10)
		return c
	}
	cases := []struct {
		c    *api.Committer
		op   func(*api.Committer) error
		want error
	}{
		{mk(4), func(c *api.Committer) error { return c.Ack(9, 0) }, cmt.ErrUnassigned},
		{mk(4), func(c *api.Committer) error { return c.Deliver(0, 12) }, ofs.ErrGap},
		{mk(4), func(c *api.Committer) error { return c.Ack(0, 11) }, ofs.ErrOutOfRange},
		{mk(1), func(c *api.Committer) error { return c.Deliver(0, 11) }, cmt.ErrTooManyInFlight},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		before := tc.c.Commit()
		if err := tc.op(tc.c); !errors.Is(err, tc.want) || seen[tc.want] {
			t.Fatalf("err=%v want %v（四类须互不相同）", err, tc.want)
		}
		seen[tc.want] = true
		if after := tc.c.Commit(); after[0] != before[0] || len(after) != len(before) {
			t.Fatalf("被拒操作改变了状态: %v -> %v", before, after)
		}
		tc.c.Ack(0, 10) // 被拒后仍可用
		if got, _ := tc.c.Committed(0); got != 11 {
			t.Fatalf("C=%d want 11", got)
		}
	}
	if err := api.New(8).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 第三节场景：七步 Ack 的 Committed 序列与重启后的重复投递集合。
func TestRestartRedelivery(t *testing.T) {
	c := api.New(16)
	c.Assign(0, 100)
	for off := int64(100); off <= 107; off++ {
		c.Deliver(0, off)
	}
	wantC := []int64{100, 101, 102, 102, 102, 104, 104}
	for i, off := range []int64{103, 100, 101, 105, 101, 102, 107} {
		c.Ack(0, off)
		if got, _ := c.Committed(0); got != wantC[i] {
			t.Fatalf("step %d: C=%d want %d", i, got, wantC[i])
		}
	}
	c.Restart()
	for off := int64(104); off <= 107; off++ { // 重启后从 C=104 重投 {104,105,106,107}
		if err := c.Deliver(0, off); err != nil {
			t.Fatalf("重投 %d: %v", off, err)
		}
	}
	if err := c.Deliver(0, 103); !errors.Is(err, ofs.ErrGap) { // 103 < C，不会再投递
		t.Fatalf("err=%v want ErrGap", err)
	}
}

// 并发：N 个 goroutine 各 Ack 一条，最终 Committed=起点+N，期间读单调不减。
func TestConcurrentAck(t *testing.T) {
	for _, n := range []int{8, 256} {
		c := api.New(n)
		c.Assign(0, 0)
		for i := int64(0); i < int64(n); i++ {
			c.Deliver(0, i)
		}
		var doneCnt atomic.Int64
		var monoBad atomic.Bool
		start := make(chan struct{})
		go func() { // 单调性观察：任何时刻读到的值不得小于此前读到的值
			prev := int64(0)
			for doneCnt.Load() < int64(n) {
				v, _ := c.Committed(0)
				if v < prev {
					monoBad.Store(true)
				}
				prev = v
			}
		}()
		var wg sync.WaitGroup
		for i := int64(0); i < int64(n); i++ {
			wg.Add(1)
			go func(off int64) {
				defer wg.Done()
				<-start
				if err := c.Ack(0, off); err != nil {
					t.Error(err)
				}
				doneCnt.Add(1)
			}(i)
		}
		close(start)
		wg.Wait()
		if monoBad.Load() {
			t.Fatalf("n=%d: Committed 读非单调", n)
		}
		if got, _ := c.Committed(0); got != int64(n) {
			t.Fatalf("n=%d: C=%d want %d", n, got, n)
		}
	}
}
