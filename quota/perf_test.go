package quota

import (
	"fmt"
	"testing"
)

// TestAdmissionDoesNotScanPods 结构性证明：准入/调整/删除路径不扫描已有 Pod 集合，
// 因此一次准入的开销不随命名空间内 Pod 数量增长。
func TestAdmissionDoesNotScanPods(t *testing.T) {
	c := newControllerWithNS(t, "ns")
	if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
		ResourcePods: 1 << 40, "requests.cpu": 1 << 40,
	}}); err != nil {
		t.Fatal(err)
	}
	const prefill = 5000
	for i := 0; i < prefill; i++ {
		if err := c.CreatePod("ns", Pod{
			Name: fmt.Sprintf("p%d", i),
			Spec: PodSpec{Requests: map[string]int64{"cpu": 1}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := c.podScans
	// 在已有 5000 个 Pod 的命名空间上执行准入、调整、删除。
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("extra-%d", i)
		if err := c.CreatePod("ns", Pod{Name: name, Spec: PodSpec{
			Requests: map[string]int64{"cpu": 1},
		}}); err != nil {
			t.Fatal(err)
		}
		if err := c.UpdatePod("ns", Pod{Name: name, Spec: PodSpec{
			Requests: map[string]int64{"cpu": 2},
		}}); err != nil {
			t.Fatal(err)
		}
		if err := c.DeletePod("ns", name); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("5000 个已存在 Pod 下执行 300 次准入类操作, Pod 全量扫描次数 %d -> %d; 依据 增量记账不扫描 Pod 集合",
		before, c.podScans)
	if c.podScans != before {
		t.Fatalf("准入路径扫描了已有 Pod 集合: %d -> %d", before, c.podScans)
	}
	mustSelfCheck(t, c, "ns")
}

// BenchmarkAdmissionScaling 经验性证明：在不同 Pod 规模下测量单次准入耗时。
// 若开销与 Pod 数量无关，三档规模的 ns/op 应基本持平。
// 运行: go test ./quota -bench=AdmissionScaling -benchtime=100x
func BenchmarkAdmissionScaling(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("pods=%d", n), func(b *testing.B) {
			c := NewController()
			if err := c.CreateNamespace("ns"); err != nil {
				b.Fatal(err)
			}
			if err := c.AddQuota("ns", Quota{Name: "q", Hard: map[ResourceName]int64{
				ResourcePods: 1 << 40, "requests.cpu": 1 << 40,
			}}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if err := c.CreatePod("ns", Pod{
					Name: fmt.Sprintf("p%d", i),
					Spec: PodSpec{Requests: map[string]int64{"cpu": 1}},
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				name := fmt.Sprintf("bench-%d", i)
				if err := c.CreatePod("ns", Pod{Name: name, Spec: PodSpec{
					Requests: map[string]int64{"cpu": 1},
				}}); err != nil {
					b.Fatal(err)
				}
				if err := c.DeletePod("ns", name); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
