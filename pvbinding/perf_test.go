package pvbinding

import (
	"fmt"
	"testing"
)

// TestImmediateSelection_OnlyScansOwnStorageClass 用可验证的计数证明：
// 立即绑定的选卷检查数只随声明所属存储类的桶大小变化，与无关存储类卷数无关。
func TestImmediateSelection_OnlyScansOwnStorageClass(t *testing.T) {
	log := newLog(t)

	measure := func(t *testing.T, irrelevant, relevant int) int {
		t.Helper()
		c := New()
		for i := 0; i < irrelevant; i++ {
			assertNoErr(t, c, c.AddVolume(fmt.Sprintf("noise-%d", i),
				volSpec(1, "noise-sc", am(ReadWriteOnce))), "noise")
		}
		for i := 0; i < relevant; i++ {
			assertNoErr(t, c, c.AddVolume(fmt.Sprintf("keep-%05d", i),
				volSpec(int64(100+i), "target-sc", am(ReadWriteOnce))), "keep")
		}
		assertInvariants(t, c)
		before := c.Stats().ExaminedTotal
		// 该声明所需容量超过全部 target-sc 卷 => 必然扫描整个目标桶，
		// 且最终保持待绑定，这给出最坏情况下的检查数上界。
		err := c.AddClaim("probe", claimSpec(1_000_000, "target-sc",
			am(ReadWriteOnce)))
		assertNoErr(t, c, err, "probe")
		assertPending(t, c, "probe")
		got := c.Stats().ExaminedTotal - before
		return got
	}

	e1 := measure(t, 100, 50)
	e2 := measure(t, 20_000, 50)
	log.step(fmt.Sprintf("无关卷=%d 目标桶=%d => 检查 %d；无关卷=%d 目标桶=%d => 检查 %d",
		100, 50, e1, 20_000, 50, e2), nil,
		"无关存储类卷数扩大 200 倍，检查次数应保持不变且等于目标桶大小")
	if e1 != 50 || e2 != 50 {
		t.Fatalf("examined = %d,%d, want 50,50", e1, e2)
	}

	e3 := measure(t, 20_000, 200)
	log.step(fmt.Sprintf("无关卷=%d 目标桶=%d => 检查 %d", 20_000, 200, e3), nil,
		"检查数随目标存储类桶大小增长，而不是随总卷数增长")
	if e3 != 200 {
		t.Fatalf("examined = %d, want 200", e3)
	}
}

func BenchmarkImmediateSelection(b *testing.B) {
	for _, n := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("irrelevant=%d", n), func(b *testing.B) {
			c := New()
			for i := 0; i < n; i++ {
				if err := c.AddVolume(fmt.Sprintf("noise-%d", i),
					volSpec(1, "noise-sc", am(ReadWriteOnce))); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < 100; i++ {
				vs := volSpec(int64(100+i), "target-sc", am(ReadWriteOnce))
				vs.Reclaim = ReclaimDelete
				if err := c.AddVolume(fmt.Sprintf("keep-%d", i), vs); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				name := fmt.Sprintf("probe-%d", i)
				if err := c.AddClaim(name, claimSpec(int64(100+(i%100)),
					"target-sc", am(ReadWriteOnce))); err != nil {
					b.Fatal(err)
				}
				if err := c.DeleteClaim(name); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
