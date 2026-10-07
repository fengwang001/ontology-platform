package pivas_test

import (
	"fmt"
	"testing"

	"ontology/pivas"
)

// 证明一：受理开销不随历史已完成批次总数增长。
// 两档规模（1 千 / 5 万已完成批次）下，稳态受理耗时应基本持平：
// 已完成批次在操作被接受时即被清除（每个批次均摊 O(1)），
// 受理只扫描当前未开始批次。
func BenchmarkAdmissionCompletedHistory(b *testing.B) {
	for _, completed := range []int{1_000, 50_000} {
		b.Run(fmt.Sprintf("completed=%d", completed), func(b *testing.B) {
			c, err := pivas.NewCenter(5, 8)
			if err != nil {
				b.Fatal(err)
			}
			mustDrugB(c, 0)
			if err := c.RegisterBench(0, pivas.BenchConfig{
				ID: "B1", Capacity: 2, DurationByCount: []int64{0, 10, 20}, ClearanceSec: 5,
			}); err != nil {
				b.Fatal(err)
			}
			now := int64(0)
			seq := 0
			admit := func() {
				o := pivas.Order{
					ID:         fmt.Sprintf("ord%d", seq),
					DrugIDs:    []string{"A"},
					Solvent:    "NS",
					RequiredAt: pivas.MaxNow,
				}
				seq++
				if _, err := c.Admit(now, o); err != nil {
					b.Fatalf("Admit: %v", err)
				}
				now += 20 // 批次 [now, now+10) 随即完成
			}
			for i := 0; i < completed; i++ {
				admit()
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				admit()
			}
		})
	}
}

// 证明二：禁忌配对判定开销不随目录中禁忌配对总数增长。
// 两档规模（1 千 / 100 万配对）下，命中与未命中两种路径耗时应基本持平：
// 配对存于哈希集合，单次判定 O(1)。
func BenchmarkIncompatibilityCheck(b *testing.B) {
	for _, pairs := range []int{1_000, 1_000_000} {
		setup := func(b *testing.B) *pivas.Center {
			c, err := pivas.NewCenter(5, 8)
			if err != nil {
				b.Fatal(err)
			}
			mustDrugB(c, 0)
			if err := c.UpsertDrug(0, pivas.Drug{ID: "B", RoomStableSec: 1000000, ColdStableSec: 1000000, SolventClass: "NS"}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < pairs; i++ {
				if err := c.AddIncompatibility(0, fmt.Sprintf("fa%d", i), fmt.Sprintf("fb%d", i)); err != nil {
					b.Fatal(err)
				}
			}
			if err := c.AddIncompatibility(0, "A", "B"); err != nil {
				b.Fatal(err)
			}
			return c
		}
		b.Run(fmt.Sprintf("pairs=%d/hit", pairs), func(b *testing.B) {
			c := setup(b)
			o := pivas.Order{ID: "x", DrugIDs: []string{"A", "B"}, Solvent: "NS", RequiredAt: pivas.MaxNow}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				o.ID = fmt.Sprintf("ord%d", i)
				if _, err := c.Admit(0, o); err == nil {
					b.Fatal("期望禁忌配对拒绝")
				}
			}
		})
		b.Run(fmt.Sprintf("pairs=%d/miss", pairs), func(b *testing.B) {
			c := setup(b)
			// 不含禁忌配对；无洁净台，判定通过后报无可行安排
			o := pivas.Order{ID: "x", DrugIDs: []string{"A"}, Solvent: "NS", RequiredAt: pivas.MaxNow}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				o.ID = fmt.Sprintf("ord%d", i)
				if _, err := c.Admit(0, o); err == nil {
					b.Fatal("期望无可行安排")
				}
			}
		})
	}
}

func mustDrugB(c *pivas.Center, now int64) {
	_ = c.UpsertDrug(now, pivas.Drug{ID: "A", RoomStableSec: 1000000, ColdStableSec: 1000000, SolventClass: "NS"})
}
