package limiter

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/policy"
)

// 钉住的语义：任务一·8 —— 多 goroutine 并发 Allow 同一个热点租户时，
// 放行总量不超过该时刻可用令牌数（容量），且"已放行 + 剩余 = 容量"
// 无泄漏、无超卖；go test -race 下干净。
// 之前未被覆盖的原因：既有 TestConcurrentNoOversell 把 worker 均摊到两个
// 租户，单租户热点（所有 goroutine 抢同一个桶）路径未覆盖。
func TestConcurrentSameTenantNoOversell(t *testing.T) {
	const (
		tenantCap = 300
		workers   = 16
		attempts  = 100
	)
	l, _ := newLimiter(t, 100000, 0, map[string]policy.Quota{
		"a": policy.Must(tenantCap, 0),
	})
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				if l.Allow("a", 1) == nil {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	total := allowed.Load()
	if total > tenantCap {
		t.Fatalf("single-tenant oversell: %d > %d", total, tenantCap)
	}
	tb, _, _, _ := l.Inspect("a")
	if total+int64(tb) != tenantCap {
		t.Fatalf("token leak: allowed %d + remaining %v != %d", total, tb, tenantCap)
	}
}

// 钉住的语义：任务一·8 —— 多租户并发 Allow 与并发 Inspect 混合时，
// 每租户放行不超租户容量、总放行不超全局容量，且全局账守恒；
// 并发查询（含未注册租户）不干扰扣减；go test -race 下干净。
// 之前未被覆盖的原因：既有并发测试没有并发读者，Inspect 与 Allow
// 抢同一把锁的路径未在 -race 下验证。
func TestConcurrentMultiTenantWithInspect(t *testing.T) {
	const (
		tenantCap = 200
		globalCap = 450
		workers   = 12
		attempts  = 100
	)
	tenants := []string{"a", "b", "c"}
	l, _ := newLimiter(t, globalCap, 0, map[string]policy.Quota{
		"a": policy.Must(tenantCap, 0),
		"b": policy.Must(tenantCap, 0),
		"c": policy.Must(tenantCap, 0),
	})
	var allowed [3]atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			idx := w % len(tenants)
			for i := 0; i < attempts; i++ {
				if l.Allow(tenants[idx], 1) == nil {
					allowed[idx].Add(1)
				}
			}
		}(w)
	}
	// 并发读者：已注册与未注册租户混查。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < attempts; i++ {
				l.Inspect(tenants[r%len(tenants)])
				l.Inspect("ghost")
			}
		}(r)
	}
	wg.Wait()
	var total int64
	for i := range allowed {
		if got := allowed[i].Load(); got > tenantCap {
			t.Fatalf("tenant %s oversell: %d > %d", tenants[i], got, tenantCap)
		}
		total += allowed[i].Load()
	}
	if total > globalCap {
		t.Fatalf("global oversell: %d > %d", total, globalCap)
	}
	_, gb, _, _ := l.Inspect("a")
	if total+int64(gb) != globalCap {
		t.Fatalf("global token leak: consumed %d + remaining %v != %d", total, gb, globalCap)
	}
}
