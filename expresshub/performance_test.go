package expresshub

import (
	"fmt"
	"testing"
)

func BenchmarkDuplicateAddDecision(b *testing.B) {
	for _, existing := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("existing-%d", existing), func(b *testing.B) {
			system, _ := New(Config{
				MaxItems:       existing + b.N + 1,
				MaxWeightGrams: 10_000_000,
				DwellLimitSec:  1_000_000_000,
			})
			for i := 0; i < existing; i++ {
				waybill := fmt.Sprintf("history-%06d", i)
				if _, err := system.AddParcel(AddParcelInput{
					Waybill:     waybill,
					Destination: "A",
					WeightGrams: 1,
					Category:    CategoryNormal,
					At:          0,
				}); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := system.AddParcel(AddParcelInput{
				Waybill:     "duplicate",
				Destination: "A",
				WeightGrams: 1,
				Category:    CategoryNormal,
				At:          0,
			}); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := system.AddParcel(AddParcelInput{
					Waybill:     "duplicate",
					Destination: "A",
					WeightGrams: 1,
					Category:    CategoryNormal,
					At:          0,
				})
				if err == nil {
					b.Fatal("duplicate add unexpectedly succeeded")
				}
			}
		})
	}
}

func BenchmarkAcceptedAddAfterHistoricalBags(b *testing.B) {
	for _, historicalBags := range []int{100, 1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("historical-bags-%d", historicalBags), func(b *testing.B) {
			system, _ := New(Config{
				MaxItems:       10,
				MaxWeightGrams: 10_000_000,
				DwellLimitSec:  1_000_000_000,
			})
			for i := 0; i < historicalBags; i++ {
				base := int64(i * 4)
				destination := fmt.Sprintf("history-%06d", i)
				waybill := fmt.Sprintf("history-w-%06d", i)
				added, err := system.AddParcel(AddParcelInput{
					Waybill:     waybill,
					Destination: destination,
					WeightGrams: 1,
					Category:    CategoryNormal,
					At:          base,
				})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := system.SealBag(SealBagInput{Destination: destination, At: base + 1}); err != nil {
					b.Fatal(err)
				}
				if _, err := system.DispatchBag(DispatchBagInput{
					BagID: added.BagID, VehicleID: "history", At: base + 2,
				}); err != nil {
					b.Fatal(err)
				}
				if _, err := system.VerifyBag(VerifyBagInput{
					BagID: added.BagID, Destination: destination,
					Scanned: []string{waybill}, At: base + 3,
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			currentTime := int64(historicalBags * 4)
			for i := 0; i < b.N; i++ {
				destination := fmt.Sprintf("current-%06d", i)
				_, err := system.AddParcel(AddParcelInput{
					Waybill:     fmt.Sprintf("current-w-%06d", i),
					Destination: destination,
					WeightGrams: 1,
					Category:    CategoryNormal,
					At:          currentTime,
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
