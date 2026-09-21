package ontology

import "testing"

// TestStatsScaleWithRuns: on a ten-million-bit, two-run set, statistics
// touch two runs — not ten million bits.
func TestStatsScaleWithRuns(t *testing.T) {
	s := &Set{runs: []interval{{1_000, 5_000_999}, {8_000_000, 12_999_999}}}
	const bits = 5_000_000 + 5_000_000
	st := s.Stats()
	if st.Empty {
		t.Fatal("set reported empty")
	}
	if st.Count != bits {
		t.Fatalf("count = %d, want %d", st.Count, bits)
	}
	if st.Min != 1_000 || st.Max != 12_999_999 {
		t.Fatalf("min/max = %d/%d", st.Min, st.Max)
	}
	if st.RunsVisited != 2 {
		t.Fatalf("runs visited = %d, want 2", st.RunsVisited)
	}
	if st.RunsVisited >= bits/1000 {
		t.Fatalf("runs visited %d not far below bit count %d", st.RunsVisited, bits)
	}
	if s.Count() != bits {
		t.Fatal("Count() mismatch")
	}
	if lo, ok := s.Min(); !ok || lo != 1_000 {
		t.Fatal("Min() mismatch")
	}
	if hi, ok := s.Max(); !ok || hi != 12_999_999 {
		t.Fatal("Max() mismatch")
	}
}

// TestStatsSingletonAndEmpty covers the degenerate cases.
func TestStatsSingletonAndEmpty(t *testing.T) {
	empty := New()
	st := empty.Stats()
	if !st.Empty || st.Count != 0 || st.RunsVisited != 0 {
		t.Fatalf("empty stats = %+v", st)
	}
	one := New()
	one.Set(42)
	st = one.Stats()
	if st.Empty || st.Count != 1 || st.Min != 42 || st.Max != 42 || st.RunsVisited != 1 {
		t.Fatalf("singleton stats = %+v", st)
	}
}
