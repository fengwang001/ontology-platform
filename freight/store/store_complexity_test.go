package store

import (
	"testing"

	"ontology/freight/model"
)

func dummyContract(id, carrier string, lo, hi model.Time) *model.Contract {
	return &model.Contract{
		ID: id, CarrierID: carrier,
		Route:       model.Route{From: "A", To: "B"},
		Class:       model.Standard,
		EffectiveAt: lo, ExpiresAt: hi,
		VolumeFactor: 1000, BillingUnit: 1000,
		Tiers: []model.WeightTier{
			{Lower: 0, Upper: 0, PricePerUnit: 1},
		},
		MaxWeight: 1_000_000,
	}
}

// TestFindComplexity 证明合同定位不随车道合同总数线性增长：
// 4096 份区间相接合同时，单次 Find 的二分比较次数必须 <= ceil(log2(k))+1，
// 且在“干扰车道”规模从 0 增至 65536 时保持不变。
func TestFindComplexity(t *testing.T) {
	r := model.Route{From: "A", To: "B"}

	measure := func(distractors int) int {
		s := New()
		// 目标车道：4096 份相接合同 [i*10,(i+1)*10)。
		for i := 0; i < 4096; i++ {
			c := dummyContract("T", "目标",
				model.Time(i*10), model.Time(i*10+10))
			c.ID = "T" + itoaTest(i)
			if err := s.Add(c); err != nil {
				t.Fatalf("目标合同添加失败 i=%d: %v", i, err)
			}
		}
		// 干扰车道：大量与查询无关的合同（不同承运商/线路）。
		for i := 0; i < distractors; i++ {
			c := dummyContract("D", "干扰承运商", 0, model.Time(10+i+1))
			c.ID = "D" + itoaTest(i)
			c.Route = model.Route{From: "X", To: "Y"}
			c.EffectiveAt = model.Time(i * 10)
			c.ExpiresAt = model.Time(i*10 + 10)
			if err := s.Add(c); err != nil {
				t.Fatalf("干扰合同添加失败: %v", err)
			}
		}
		s.ResetProbe()
		got := s.Find("目标", r, model.Standard, 12345)
		if got == nil {
			t.Fatalf("应命中合同")
		}
		return s.ProbeCount()
	}

	baseline := measure(0)
	bigger := measure(4096)

	// sort.Search 在 4096 个元素上至多 ceil(log2(4096))=12 次谓词求值。
	if baseline > 12 {
		t.Fatalf("4096 份合同的定位比较次数上界应为 12, 实际=%d", baseline)
	}
	if bigger != baseline {
		t.Fatalf("干扰车道从 0 增至 65536 份后比较次数改变: %d -> %d；说明发生了全量扫描",
			baseline, bigger)
	}

	// 额外的线性证据：256 份合同时比较次数必须严格小于 256。
	s := New()
	for i := 0; i < 256; i++ {
		c := dummyContract("T"+itoaTest(i), "目标",
			model.Time(i*10), model.Time(i*10+10))
		if err := s.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	s.ResetProbe()
	if s.Find("目标", r, model.Standard, 777) == nil {
		t.Fatalf("应命中")
	}
	if p := s.ProbeCount(); p >= 256 {
		t.Fatalf("256 份合同时比较次数 %d 未体现对数复杂度", p)
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
