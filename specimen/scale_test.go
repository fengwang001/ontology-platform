package specimen

import (
	"strconv"
	"testing"
)

func buildScaledSystem(tb testing.TB, history, catalogSize int) (*System, string, []string) {
	tb.Helper()
	sys := NewSystem()
	for index := 0; index < catalogSize; index++ {
		requirement := CatalogRequirement{TubeType: "hist", MaxDeliverySeconds: 10, HemolysisTolerance: 4}
		if err := sys.UpsertCatalogItem(0, "HIST"+strconv.Itoa(index), requirement); err != nil {
			tb.Fatal(err)
		}
	}
	if err := sys.UpsertCatalogItem(0, "TARGET", CatalogRequirement{TubeType: "target", MaxDeliverySeconds: 10, ColdRequired: true, HemolysisTolerance: 1}); err != nil {
		tb.Fatal(err)
	}
	for index := 0; index < history; index++ {
		patient := "history-" + strconv.Itoa(index)
		result, err := sys.Apply(0, patient, []string{"TARGET"}, "P")
		if err != nil {
			tb.Fatal(err)
		}
		tubeID, err := sys.Collect(0, patient, "target", result.ItemIDs, 0)
		if err != nil {
			tb.Fatal(err)
		}
		if err := sys.Dispatch(0, tubeID, TransportCold); err != nil {
			tb.Fatal(err)
		}
		if _, err := sys.Sign(0, tubeID, 0); err != nil {
			tb.Fatal(err)
		}
	}
	result, err := sys.Apply(1, "target", []string{"TARGET"}, "P")
	if err != nil {
		tb.Fatal(err)
	}
	tubeID, err := sys.Collect(2, "target", "target", result.ItemIDs, 2)
	if err != nil {
		tb.Fatal(err)
	}
	return sys, tubeID, result.ItemIDs
}

func BenchmarkSignScalesWithTubeItems(b *testing.B) {
	for _, scale := range []struct{ history, catalog int }{{10, 10}, {1000, 1000}} {
		b.Run(strconv.Itoa(scale.history), func(b *testing.B) {
			for range b.N {
				b.StopTimer()
				sys, tubeID, _ := buildScaledSystem(b, scale.history, scale.catalog)
				if err := sys.Dispatch(2, tubeID, TransportCold); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if _, err := sys.Sign(12, tubeID, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkQueryScalesWithActiveItems(b *testing.B) {
	for _, scale := range []struct{ history, catalog int }{{10, 10}, {1000, 1000}} {
		b.Run(strconv.Itoa(scale.history), func(b *testing.B) {
			sys, _, _ := buildScaledSystem(b, scale.history, scale.catalog)
			b.ResetTimer()
			for range b.N {
				if _, err := sys.Query(3, "target"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestComplexityBoundaryDoesNotScanAllItemsOrCatalog(t *testing.T) {
	sys, tubeID, targetItems := buildScaledSystem(t, 1000, 1000)
	before := len(sys.itemsByID)
	if err := sys.Dispatch(2, tubeID, TransportCold); err != nil {
		t.Fatal(err)
	}
	decisions, err := sys.Sign(12, tubeID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != len(targetItems) || len(sys.itemsByID) != before {
		t.Fatalf("sign must only touch its own tube: decisions=%d items=%d", len(decisions), len(sys.itemsByID))
	}
	view, err := sys.Query(13, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 0 {
		t.Fatalf("accepted historical and target items must leave the active set: %d", len(view.Items))
	}
}
