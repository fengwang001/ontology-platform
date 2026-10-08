package dtc

import (
	"fmt"
	"testing"
)

// buildHistory returns a manager whose DTC "A" has failed (and been
// confirmed) in cycles ignition cycles, plus a registered idle DTC "B".
func buildHistory(tb testing.TB, cycles int) *Manager {
	tb.Helper()
	m, err := NewManager(testConfig)
	if err != nil {
		tb.Fatal(err)
	}
	for _, id := range []string{"A", "B"} {
		if err := m.Register(id, 1); err != nil {
			tb.Fatal(err)
		}
	}
	var ts, odo int64
	for i := 0; i < cycles; i++ {
		for _, ev := range []Event{
			{Kind: EvIgnitionOn},
			{Kind: EvMonitorResult, DTC: "A"},
			{Kind: EvIgnitionOff},
		} {
			ts++
			odo += 10
			ev.Time, ev.Odometer = ts, odo
			if err := m.Handle(ev); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return m
}

// runMeasuredCycle runs one ignition cycle involving only DTC "B" and
// returns the number of per-DTC work units it cost.
func runMeasuredCycle(tb testing.TB, m *Manager, ts, odo *int64) int64 {
	tb.Helper()
	before := m.WorkUnits()
	for _, ev := range []Event{
		{Kind: EvIgnitionOn},
		{Kind: EvMonitorResult, DTC: "B"},
		{Kind: EvMonitorResult, DTC: "B", Passed: true},
		{Kind: EvIgnitionOff},
	} {
		*ts++
		*odo += 10
		ev.Time, ev.Odometer = *ts, *odo
		if err := m.Handle(ev); err != nil {
			tb.Fatal(err)
		}
	}
	return m.WorkUnits() - before
}

// TestCostIndependentOfHistory is the verifiable proof of the cost
// model: the per-DTC work of a monitor report and of an ignition-off
// settlement is identical whether the manager carries 100 or 100000
// cycles of history. Two tiers are compared; they must match exactly.
func TestCostIndependentOfHistory(t *testing.T) {
	tiers := []int{100, 100000}
	deltas := make([]int64, len(tiers))
	for i, cycles := range tiers {
		m := buildHistory(t, cycles)
		ts := int64(1 << 40)
		odo := int64(1 << 40)
		deltas[i] = runMeasuredCycle(t, m, &ts, &odo)
		t.Logf("history=%d cycles: measured cycle cost %d work units", cycles, deltas[i])
	}
	if deltas[0] != deltas[1] {
		t.Fatalf("cost grows with history: tier %d -> %d units, tier %d -> %d units",
			tiers[0], deltas[0], tiers[1], deltas[1])
	}
	// Sanity: the measured cycle touches exactly one DTC — 2 debounce
	// updates (fail + pass report) plus 1 settlement visit.
	if want := int64(3); deltas[0] != want {
		t.Fatalf("measured cycle cost = %d units, want exactly %d", deltas[0], want)
	}
}

// BenchmarkIgnitionOffSettlement measures a report+settlement cycle for
// an idle DTC against growing history lengths (run with -benchtime).
func BenchmarkIgnitionOffSettlement(b *testing.B) {
	for _, hist := range []int{0, 1000, 100000} {
		b.Run(fmt.Sprintf("history=%d", hist), func(b *testing.B) {
			m := buildHistory(b, hist)
			ts := int64(1 << 40)
			odo := int64(1 << 40)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, ev := range []Event{
					{Kind: EvIgnitionOn},
					{Kind: EvMonitorResult, DTC: "B"},
					{Kind: EvIgnitionOff},
				} {
					ts++
					odo += 10
					ev.Time, ev.Odometer = ts, odo
					if err := m.Handle(ev); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
