package ontology

import "testing"

func TestStatsVisitRunsNotBits(t *testing.T) {
	// Ten million bits across only four runs.
	s := fromRuns(
		run{0, 100},
		run{1, 5_000_000},
		run{0, 42},
		run{1, 5_000_000},
	)
	if got := s.Count(); got != 10_000_000 {
		t.Fatalf("Count = %d", got)
	}
	if got := s.LastStatRuns(); got != 4 {
		t.Fatalf("Count visited %d runs, want 4", got)
	}
	lo, ok := s.Min()
	if !ok || lo != 100 {
		t.Fatalf("Min = %d, %v", lo, ok)
	}
	if got := s.LastStatRuns(); got != 2 {
		t.Fatalf("Min visited %d runs, want 2", got)
	}
	hi, ok := s.Max()
	if !ok || hi != 100+5_000_000+42+5_000_000-1 {
		t.Fatalf("Max = %d, %v", hi, ok)
	}
	if got := s.LastStatRuns(); got != 1 {
		t.Fatalf("Max visited %d runs, want 1", got)
	}
	// Every stat must be vastly cheaper than walking ten million bits.
	if s.LastStatRuns() >= 10_000_000/1000 {
		t.Fatal("stat cost not far below bit count")
	}
}

func TestStatsEmpty(t *testing.T) {
	s := New()
	if s.Count() != 0 {
		t.Fatal("empty Count != 0")
	}
	if _, ok := s.Min(); ok {
		t.Fatal("empty Min ok")
	}
	if _, ok := s.Max(); ok {
		t.Fatal("empty Max ok")
	}
}

func TestStatsViaPublicAPI(t *testing.T) {
	// 100k contiguous bits built through the public Set path.
	s := New()
	for p := uint32(1000); p < 101_000; p++ {
		s.Set(p)
	}
	if got := s.Count(); got != 100_000 {
		t.Fatalf("Count = %d", got)
	}
	if got := s.LastStatRuns(); got != 2 {
		t.Fatalf("Count visited %d runs, want 2", got)
	}
	lo, _ := s.Min()
	hi, _ := s.Max()
	if lo != 1000 || hi != 100_999 {
		t.Fatalf("Min/Max = %d/%d", lo, hi)
	}
}
