package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义（任务一.8）：多 goroutine 并发 Allow 同一租户与不同租户，
// 同时另一个 goroutine 持续推进注入时钟（补充与消费并发发生）。要求：
// go test -race 干净；每个租户的放行总量不超过该时刻可用令牌数
// （容量 + 速率×已流逝时间）；最终「已消费 + 剩余」守恒，不丢不造令牌。
// 之前未覆盖：既有 TestConcurrentNoOversell 全程速率 0、时钟静止，
// 「并发扣减」与「时间补充」同时发生的路径从未被竞态检测覆盖。
func TestConcurrentAllowWhileClockAdvances(t *testing.T) {
	const (
		globalCap = 100000
		capA      = 100
		rateA     = 10
		capB      = 50
		workers   = 8
		attempts  = 500
		steps     = 20
		stepDur   = 100 * time.Millisecond
	)
	l, c := newLimiter(t, globalCap, 0, map[string]policy.Quota{
		"a": policy.Must(capA, rateA),
		"b": policy.Must(capB, 0),
	})
	const budgetA = capA + rateA*2

	var allowedA, allowedB atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenant, counter := "a", &allowedA
			if w%2 == 1 {
				tenant, counter = "b", &allowedB
			}
			for i := 0; i < attempts; i++ {
				if l.Allow(tenant, 1) == nil {
					counter.Add(1)
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < steps; i++ {
			c.Advance(stepDur)
		}
	}()
	wg.Wait()

	gotA, gotB := allowedA.Load(), allowedB.Load()
	if gotA > budgetA {
		t.Fatalf("tenant a oversold: %d > budget %d", gotA, budgetA)
	}
	if gotB > capB {
		t.Fatalf("tenant b oversold: %d > %d", gotB, capB)
	}

	// a 桶带速率：若某次入账前余额接近满，超出容量的补充会被合法截断，
	// 因此「消费 + 剩余」只能钉住不超过预算（不超发、不倒欠）。
	tba, gba := balances(t, l, "a")
	if sum := float64(gotA) + tba; sum > budgetA || tba < 0 {
		t.Fatalf("tenant a out of budget: consumed %d + remaining %v = %v, budget %d", gotA, tba, sum, budgetA)
	}
	tbb, _ := balances(t, l, "b")
	if float64(gotB)+tbb != capB {
		t.Fatalf("tenant b leak: consumed %d + remaining %v != %d", gotB, tbb, capB)
	}
	if float64(gotA+gotB)+gba != globalCap {
		t.Fatalf("global leak: consumed %d + remaining %v != %d", gotA+gotB, gba, globalCap)
	}
}
