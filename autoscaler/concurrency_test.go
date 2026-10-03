package autoscaler

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAccess 并发调用 Evaluate 与 State：
// 结果必须等价于某个串行顺序，且不变量始终成立。
func TestConcurrentAccess(t *testing.T) {
	cfg := Config{
		Min:     2,
		Max:     100,
		Initial: 10,
		High:    70,
		Low:     30,
		Up:      []Tier{{0, 20}, {10, 50}},
		Down:    []Tier{{0, 10}, {10, 30}},
		MinStep: 1,
		Warmup:  2,
		CoolOut: 3,
		CoolIn:  3,
	}
	c := mustNew(t, cfg)

	var writers, readers sync.WaitGroup
	var now atomic.Int64
	var stop atomic.Bool

	check := func(s State) {
		if s.Cap < cfg.Min || s.Cap > cfg.Max {
			t.Errorf("cap=%d 越界 [%d,%d]", s.Cap, cfg.Min, cfg.Max)
		}
		if s.Effective > cfg.Max {
			t.Errorf("eff=%d > Mx=%d", s.Effective, cfg.Max)
		}
		if s.Cap+s.InflightTotal != s.Effective {
			t.Errorf("eff 不一致: %+v", s)
		}
		for _, b := range s.Batches {
			if b.ReadyAt <= s.MaxNow {
				t.Errorf("批次 %+v 就绪时刻 <= maxNow=%d", b, s.MaxNow)
			}
		}
	}

	// 写入方：now 取自共享递增计数器，交错导致的时钟回退拒绝是允许的。
	for g := 0; g < 8; g++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			r := rand.New(rand.NewSource(int64(id) + 1))
			for i := 0; i < 500; i++ {
				n := now.Add(r.Int63n(3))
				m := r.Int63n(120)
				res, err := c.Evaluate(n, m)
				if err == nil {
					if res.Cap < cfg.Min || res.Cap > cfg.Max {
						t.Errorf("结果 cap=%d 越界", res.Cap)
					}
					if res.Cap+res.Inflight > cfg.Max {
						t.Errorf("结果 eff=%d 超上限", res.Cap+res.Inflight)
					}
				}
			}
		}(g)
	}

	// 只读方：持续读取快照并校验不变量。
	for g := 0; g < 4; g++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				check(c.State())
			}
		}()
	}

	writers.Wait()
	stop.Store(true)
	readers.Wait()
}
