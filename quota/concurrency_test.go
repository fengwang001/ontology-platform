package quota

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAdmission 并发创建：恰好硬上限个 Pod 被接纳，用量不超过硬上限。
func TestConcurrentAdmission(t *testing.T) {
	const hard = 64
	const goroutines = 256
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		ResourcePods: hard, "requests.cpu": hard,
	}}); err != nil {
		t.Fatal(err)
	}
	var admitted, exceeded, other atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := c.CreatePod("ns", Pod{
				Name: fmt.Sprintf("p%d", i),
				Spec: PodSpec{Requests: map[string]int64{"cpu": 1}},
			})
			switch {
			case err == nil:
				admitted.Add(1)
			case IsKind(err, ErrQuotaExceeded):
				exceeded.Add(1)
			default:
				other.Add(1)
				t.Errorf("非预期错误: %v", err)
			}
		}(i)
	}
	wg.Wait()
	t.Logf("接纳=%d 超限拒绝=%d 其他=%d; 依据 并发结果须等价于某个串行顺序", admitted.Load(), exceeded.Load(), other.Load())
	if admitted.Load() != hard || exceeded.Load() != goroutines-hard {
		t.Fatalf("接纳 %d / 拒绝 %d, 期望 %d / %d", admitted.Load(), exceeded.Load(), hard, goroutines-hard)
	}
	u := usageOf(t, c, "ns", "q")
	if u[ResourcePods] != hard || u["requests.cpu"] != hard {
		t.Fatalf("并发后用量超过硬上限或不符: %v", u)
	}
	mustSelfCheck(t, c, "ns")
}

// TestConcurrentMixedOps 并发混合操作：最终状态必须自洽（自检通过、用量不超上限）。
func TestConcurrentMixedOps(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		ResourcePods: 32, "requests.cpu": 32,
	}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("p%d", i%32)
			switch i % 3 {
			case 0:
				_ = c.CreatePod("ns", Pod{Name: name, Spec: PodSpec{
					Requests: map[string]int64{"cpu": 1},
				}})
			case 1:
				_ = c.UpdatePod("ns", Pod{Name: name, Spec: PodSpec{
					Requests: map[string]int64{"cpu": int64(i%2 + 1)},
				}})
			case 2:
				_ = c.DeletePod("ns", name)
			}
		}(i)
	}
	wg.Wait()
	mustSelfCheck(t, c, "ns")
	u := usageOf(t, c, "ns", "q")
	t.Logf("混合并发后用量=%v; 依据 自检通过且用量不超硬上限", u)
	if u[ResourcePods] > 32 || u["requests.cpu"] > 32 {
		t.Fatalf("用量超过硬上限: %v", u)
	}
}
