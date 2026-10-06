package quota

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAdmission 并发准入：大量 goroutine 同时创建 Pod，
// 成功数必须恰好等于硬上限，用量不得超过硬上限，且最终状态一致。
func TestConcurrentAdmission(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	const podCap = 50
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{ResourcePods: podCap, RequestsFor("cpu"): 1 << 40},
	}))

	const workers = 200
	var succeeded atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := c.CreatePod("ns", fmt.Sprintf("p%d", i), PodSpec{
				Requests: map[string]int64{"cpu": 100},
			})
			if err == nil {
				succeeded.Add(1)
			} else if !IsKind(err, KindQuotaExceeded) {
				t.Errorf("意外错误: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := succeeded.Load(); got != podCap {
		t.Fatalf("并发准入成功数 = %d，期望 %d", got, podCap)
	}
	mustUsage(t, c, "ns", "q", map[ResourceName]int64{
		ResourcePods: podCap, RequestsFor("cpu"): podCap * 100,
	})
	mustConsistent(t, c)
}

// TestConcurrentMixed 并发混合操作（创建 / 调整 / 删除 / 配额变更 / 自检），
// 结果必须等价于某个串行顺序：最终一致性自检必须通过。
func TestConcurrentMixed(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{ResourcePods: 1000, RequestsFor("cpu"): 100000},
	}))

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				name := fmt.Sprintf("w%d-p%d", w, i)
				spec := PodSpec{Requests: map[string]int64{"cpu": int64(i + 1)}}
				if err := c.CreatePod("ns", name, spec); err != nil {
					continue
				}
				_ = c.UpdatePod("ns", name, PodSpec{
					Requests: map[string]int64{"cpu": int64(i + 2)},
				})
				if i%3 == 0 {
					_ = c.DeletePod("ns", name)
				}
			}
		}(w)
	}
	// 并发执行配额调整与自检
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = c.UpdateQuota("ns", "q", QuotaSpec{
					Hard: map[ResourceName]int64{
						ResourcePods:       1000,
						RequestsFor("cpu"): int64(100000 + i),
					},
				})
				if err := c.CheckConsistency(); err != nil {
					t.Errorf("并发期间一致性自检失败: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	mustConsistent(t, c)
}

// TestAdmissionIsO1 证明一次 Pod 准入的开销不随命名空间内
// 已有 Pod 数量增长：Pod 的创建 / 调整 / 删除完全不扫描 Pod 集合。
func TestAdmissionIsO1(t *testing.T) {
	c := NewController()
	newNS(t, c, "ns")
	mustOK(t, c.CreateQuota("ns", "q", QuotaSpec{
		Hard: map[ResourceName]int64{ResourcePods: 1 << 40, RequestsFor("cpu"): 1 << 40},
	}))
	const n = 2000
	for i := 0; i < n; i++ {
		mustOK(t, c.CreatePod("ns", fmt.Sprintf("p%d", i), PodSpec{
			Requests: map[string]int64{"cpu": 1},
		}))
	}
	mustConsistent(t, c)

	before := c.PodsScanned()
	for i := 0; i < n; i++ {
		mustOK(t, c.UpdatePod("ns", fmt.Sprintf("p%d", i), PodSpec{
			Requests: map[string]int64{"cpu": 2},
		}))
	}
	for i := 0; i < n; i++ {
		mustOK(t, c.DeletePod("ns", fmt.Sprintf("p%d", i)))
	}
	for i := 0; i < n; i++ {
		mustOK(t, c.CreatePod("ns", fmt.Sprintf("q%d", i), PodSpec{
			Requests: map[string]int64{"cpu": 1},
		}))
	}
	if got := c.PodsScanned() - before; got != 0 {
		t.Fatalf("Pod 准入 / 调整 / 删除扫描了 %d 个既有 Pod，期望 0", got)
	}
	mustConsistent(t, c)
}

// BenchmarkAdmission 在不同既有 Pod 数量下测量单次准入开销，
// 配合 TestAdmissionIsO1 证明开销与 Pod 数量无关。
// 运行：go test -bench=Admission -benchmem ./quota
func BenchmarkAdmission(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprintf("existingPods=%d", n), func(b *testing.B) {
			c := NewController()
			if err := c.CreateNamespace("ns"); err != nil {
				b.Fatal(err)
			}
			if err := c.CreateQuota("ns", "q", QuotaSpec{
				Hard: map[ResourceName]int64{ResourcePods: 1 << 40, RequestsFor("cpu"): 1 << 40},
			}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if err := c.CreatePod("ns", fmt.Sprintf("p%d", i), PodSpec{
					Requests: map[string]int64{"cpu": 1},
				}); err != nil {
					b.Fatal(err)
				}
			}
			spec := PodSpec{Requests: map[string]int64{"cpu": 1}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				name := fmt.Sprintf("bench-%d", i)
				if err := c.CreatePod("ns", name, spec); err != nil {
					b.Fatal(err)
				}
				if err := c.DeletePod("ns", name); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
