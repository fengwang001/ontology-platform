package hemo

import (
	"fmt"
	"testing"
)

// BenchmarkFeasibilityVsHistory populates ONE chair with n past treatments
// (spread over the fixed time domain) and measures the cost of deciding
// whether a new treatment fits in a free slot near the end. The production
// path touches O(1) trie levels; the naive linear scan is O(n).
//
// Run with two history sizes:
//
//	go test ./hemo/ -run NONE -bench BenchmarkFeasibility -benchtime=100x
//	go test ./hemo/ -run NONE -bench BenchmarkFeasibilityLarge -benchtime=100x

func buildHistorySystem(b testing.TB, n int) (*System, *Chair, []*Treatment) {
	cfg := Config{
		RegularNegative: 30, RegularHBV: 30, RegularHCV: 30,
		RegularUnknown: 30, DeepDisinfect: 200, MinRecovery: 0,
	}
	s := NewSystem(cfg)
	if err := s.RegisterChair(0, "C1", ZoneNormal, false); err != nil {
		b.Fatal(err)
	}
	if err := s.RegisterChair(0, "C2", ZoneNormal, false); err != nil {
		b.Fatal(err)
	}
	// Place n non-overlapping historical treatments on C1. Each treatment
	// 60 min plus 30 disinfection => period 90. Domain 1e7 fits ~111k.
	period := 90
	items := make([]*Treatment, 0, n)
	tl := s.chairTimelines["C1"]
	for i := 0; i < n; i++ {
		start := i * period
		if start+60 > MaxTime {
			break
		}
		it := &Treatment{
			ID: fmt.Sprintf("hist-%d", i), PatientID: fmt.Sprintf("HP-%d", i),
			ChairID: "C1", Start: start, End: start + 60, Duration: 60,
			Occurrence: -1, InfectionAtStart: InfectionNegative,
		}
		tl.Add(it)
		s.patientTimelines[it.PatientID] = &Timeline{}
		s.treats[it.ID] = it
		items = append(items, it)
	}
	return s, s.chairs["C1"], items
}

// TestProbeBoundIndependence asserts that predecessor/successor probe counts
// for a feasibility decision do not grow between a small and a large history.
func TestProbeBoundIndependence(t *testing.T) {
	measure := func(n int) (ProbeStats, bool) {
		s, c, items := buildHistorySystem(t, n)
		last := items[len(items)-1]
		// Candidate starts in the free gap immediately after the last item:
		// last.End+30 == last.Start+90; choose exactly there.
		cand := &Treatment{
			ID: "cand", PatientID: "candP", Start: last.Start + 90,
			End: last.Start + 150, Duration: 60,
			InfectionAtStart: InfectionNegative,
		}
		s.AuditProbes = true
		s.ResetProbeStats()
		ok := s.rejectReason(c, cand, nil) == ""
		return s.ProbeStatsValue(), ok
	}

	small, smallOK := measure(1_000)
	large, largeOK := measure(100_000)
	if !smallOK || !largeOK {
		t.Fatalf("candidate should be feasible (small=%v large=%v)",
			smallOK, largeOK)
	}
	t.Logf("probes small(1k)=%+v", small)
	t.Logf("probes large(100k)=%+v", large)
	if large.LevelVisits != small.LevelVisits ||
		large.BitmapScans != small.BitmapScans ||
		large.ItemsScanned != small.ItemsScanned {
		t.Fatalf("probe counts grew with history: small=%+v large=%+v",
			small, large)
	}
}

func benchFeasibility(b *testing.B, n int) {
	s, c, items := buildHistorySystem(b, n)
	last := items[len(items)-1]
	b.ResetTimer()
	b.ReportAllocs()
	var x int
	for i := 0; i < b.N; i++ {
		cand := &Treatment{
			ID: fmt.Sprintf("cand-%d", i), PatientID: "candP",
			Start: last.Start + 90, End: last.Start + 150, Duration: 60,
			InfectionAtStart: InfectionNegative,
		}
		if s.rejectReason(c, cand, nil) == "" {
			x++
		}
	}
	b.StopTimer()
	if x != b.N {
		b.Fatalf("expected all feasible, got %d/%d", x, b.N)
	}
}

func BenchmarkFeasibilitySmall(b *testing.B) { benchFeasibility(b, 1_000) }
func BenchmarkFeasibilityLarge(b *testing.B) { benchFeasibility(b, 100_000) }

// BenchmarkNaiveScan measures the O(history) reference scan for contrast.
func BenchmarkNaiveScanSmall(b *testing.B) { benchNaiveScan(b, 1_000) }
func BenchmarkNaiveScanLarge(b *testing.B) { benchNaiveScan(b, 100_000) }

func benchNaiveScan(b *testing.B, n int) {
	s, _, items := buildHistorySystem(b, n)
	last := items[len(items)-1]
	tl := s.chairTimelines["C1"]
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Deliberately enumerate the whole chair timeline: O(history).
		var count int
		cur := tl.Successor(MinTime, nil)
		for cur != nil {
			count++
			cur = tl.Successor(cur.Start+1, nil)
		}
		if count < n {
			b.Fatalf("scanned %d want %d", count, n)
		}
		_ = last
	}
}
