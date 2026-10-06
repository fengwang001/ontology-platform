package freight

import (
	"fmt"
	"testing"
)

// buildTieredContract 构造一份 ntiers 档阶梯的合同。
func buildTieredContract(id, carrier string, start, end int64, ntiers int) Contract {
	tiers := make([]WeightTier, ntiers)
	for i := 0; i < ntiers; i++ {
		tiers[i] = WeightTier{Lower: int64(i) * 10, Upper: int64(i+1) * 10, PricePerUnit: int64(i%7 + 1)}
	}
	tiers[ntiers-1].Upper = 1_000_000_000_000
	return Contract{
		ID: id, CarrierID: carrier,
		Lane: Lane{Origin: "A", Dest: "B"}, Level: Standard,
		Start: start, End: end,
		VolumeDivisor: 10, BillingUnit: 10, Tiers: tiers,
		FuelPermille: 100, MinCharge: 100,
		DimThreshold: 50, DimExcessFee: 10,
		WeightThreshold: 1 << 50, WeightExcessFee: 10,
		MaxWeight: 1 << 60, MaxDim: 1 << 60,
	}
}

// BenchmarkPriceVsTotalContracts 验证计价开销不随系统内合同总数增长：
// 合同分布在大量不同线路上，目标线路只有一份合同。
// 若实现为全量扫描，耗时将随规模线性增长；二分索引下应近似平坦。
func BenchmarkPriceVsTotalContracts(b *testing.B) {
	for _, n := range []int{100, 10000, 1000000} {
		b.Run(fmt.Sprintf("contracts=%d", n), func(b *testing.B) {
			s := NewSystem()
			for i := 0; i < n; i++ {
				c := buildTieredContract(fmt.Sprintf("bg-%d", i), "carrier-bg",
					0, 100, 8)
				c.Lane = Lane{Origin: "A", Dest: fmt.Sprintf("Z%d", i)}
				if err := s.AddContract(c); err != nil {
					b.Fatal(err)
				}
			}
			if err := s.AddContract(buildTieredContract("target", "carrier-t", 0, 100, 8)); err != nil {
				b.Fatal(err)
			}
			w := Waybill{
				ID: "bw", CarrierID: "carrier-t",
				Lane: Lane{Origin: "A", Dest: "B"}, Level: Standard,
				PickupTime: 50, ActualWeight: 55, Volume: 100,
				Dims: [3]int64{10, 10, 10},
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Price(w); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPriceVsContractsPerLane 验证同一线路下合同定位开销为 O(log n)。
func BenchmarkPriceVsContractsPerLane(b *testing.B) {
	for _, n := range []int{10, 1000, 100000} {
		b.Run(fmt.Sprintf("perLane=%d", n), func(b *testing.B) {
			s := NewSystem()
			for i := 0; i < n; i++ {
				start := int64(i) * 10
				if err := s.AddContract(buildTieredContract(fmt.Sprintf("c-%d", i), "carrier-t", start, start+10, 8)); err != nil {
					b.Fatal(err)
				}
			}
			w := Waybill{
				ID: "bw", CarrierID: "carrier-t",
				Lane: Lane{Origin: "A", Dest: "B"}, Level: Standard,
				PickupTime: int64(n)*10 - 5, ActualWeight: 55,
				Dims: [3]int64{10, 10, 10},
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Price(w); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPriceVsTiers 验证阶梯计价开销为 O(log 档数)（前缀和 + 二分）。
func BenchmarkPriceVsTiers(b *testing.B) {
	for _, n := range []int{8, 1024, 131072} {
		b.Run(fmt.Sprintf("tiers=%d", n), func(b *testing.B) {
			s := NewSystem()
			if err := s.AddContract(buildTieredContract("c", "carrier-t", 0, 100, n)); err != nil {
				b.Fatal(err)
			}
			w := Waybill{
				ID: "bw", CarrierID: "carrier-t",
				Lane: Lane{Origin: "A", Dest: "B"}, Level: Standard,
				PickupTime: 50, ActualWeight: int64(n)*10 - 5, // 落在最后一档
				Dims: [3]int64{10, 10, 10},
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Price(w); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
