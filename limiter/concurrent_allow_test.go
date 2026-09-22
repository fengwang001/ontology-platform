package limiter

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/policy"
)

// 钉住的语义：并发安全与不超卖——并发 Allow 同一租户与不同租户时，
// go test -race 干净，且放行总量不超过该时刻可用令牌数（令牌守恒：
// 已放行 + 剩余 == 初始容量）。并发期间混入 Inspect 查询以验证读写互斥。
// 之前未被覆盖的原因：TestConcurrentNoOversell 只覆盖了"两个租户各 8 个
// worker"的一种形态，没有覆盖"全部 worker 打同一个租户"的极端争用，
// 也没有并发查询混入。

// 全部 worker 抢同一个租户：放行总量不得超出租户容量，且令牌守恒。
func TestConcurrentSameTenantNoOversell(t *testing.T) {
	const (
		tenantCap = 300
		workers   = 32
		attempts  = 100
	)
	l, _ := newLimiter(t, 100000, 0, map[string]policy.Quota{
		"hot": policy.Must(tenantCap, 0),
	})
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				if l.Allow("hot", 1) == nil {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	got := allowed.Load()
	if got > tenantCap {
		t.Fatalf("same-tenant oversell: allowed %d > cap %d", got, tenantCap)
	}
	tb, _ := balances(t, l, "hot")
	if got+int64(tb) != tenantCap {
		t.Fatalf("token leak: allowed %d + remaining %v != %d", got, tb, tenantCap)
	}
}

// 不同租户并发 + 并发 Inspect：全局桶不超卖、各租户不超卖、令牌守恒，
// 并发查询不影响扣减的正确性（-race 下验证互斥）。
func TestConcurrentMultiTenantWithInspect(t *testing.T) {
	const (
		tenantCap = 800
		globalCap = 1000
		workers   = 16
		attempts  = 200
	)
	l, _ := newLimiter(t, globalCap, 0, map[string]policy.Quota{
		"a": policy.Must(tenantCap, 0),
		"b": policy.Must(tenantCap, 0),
	})
	var allowedA, allowedB atomic.Int64
	var wg sync.WaitGroup
	// 并发查询：与 Allow 竞争同一把锁，验证查询不破坏扣减。
	// 有界次数 + Gosched，避免自旋 goroutine 在 -race 下饿死 worker。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			l.Inspect("a")
			l.Inspect("b")
			runtime.Gosched()
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenant, counter := "a", &allowedA
			if w%2 == 1 {
				tenant, counter = "b", &allowedB
			}
			for i := 0; i < attempts; i++ {
				if l.Allow(tenant, 2) == nil {
					counter.Add(2)
				}
			}
		}(w)
	}
	wg.Wait()
	totalA, totalB := allowedA.Load(), allowedB.Load()
	if totalA > tenantCap || totalB > tenantCap {
		t.Fatalf("tenant oversell: a=%d b=%d cap=%d", totalA, totalB, tenantCap)
	}
	if totalA+totalB > globalCap {
		t.Fatalf("global oversell: %d > %d", totalA+totalB, globalCap)
	}
	tbA, gb := balances(t, l, "a")
	tbB, _ := balances(t, l, "b")
	if totalA+int64(tbA) != tenantCap || totalB+int64(tbB) != tenantCap {
		t.Fatalf("tenant token leak: a %d+%v, b %d+%v, cap %d",
			totalA, tbA, totalB, tbB, tenantCap)
	}
	if totalA+totalB+int64(gb) != globalCap {
		t.Fatalf("global token leak: consumed %d + remaining %v != %d",
			totalA+totalB, gb, globalCap)
	}
}
