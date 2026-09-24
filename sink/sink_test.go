package sink

import (
	"fmt"
	"testing"

	"ontology/wm"
)

// TestRestartCheckedBounded proves Restart rebuilds from the persistent
// region directly: inspected entries stay bounded by partitions+keys plus
// a small constant, independent of the number m of applied records.
func TestRestartCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New(4)
		var batch []wm.Rec
		for i := 0; i < m; i++ {
			batch = append(batch, wm.Rec{Partition: i % 4, Offset: int64(i / 4),
				Key: fmt.Sprintf("k%d", i%3), Val: 1})
		}
		if err := s.Write(batch); err != nil {
			t.Fatal(err)
		}
		s.Restart()
		if limit := 4 + 3 + 8; s.checked > limit {
			t.Fatalf("m=%d: checked %d entries, want <= %d", m, s.checked, limit)
		}
	}
}

// TestWriteAndDuplicates pins per-record decisions and duplicate counting.
func TestWriteAndDuplicates(t *testing.T) {
	steps := []struct {
		batch []wm.Rec
		dups  int64
		wm0   int64
	}{
		{[]wm.Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 0, Offset: 1, Key: "a", Val: 2}}, 0, 1},
		{[]wm.Rec{{Partition: 0, Offset: 1, Key: "a", Val: 2}, {Partition: 0, Offset: 3, Key: "a", Val: 4}}, 1, 3},
		{[]wm.Rec{{Partition: 0, Offset: 2, Key: "a", Val: 9}}, 2, 3}, // cross-batch resend
	}
	s := New(2)
	for i, st := range steps {
		if err := s.Write(st.batch); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if s.Duplicates() != st.dups || s.Watermark(0) != st.wm0 {
			t.Fatalf("step %d: dups=%d wm0=%d, want %d/%d",
				i, s.Duplicates(), s.Watermark(0), st.dups, st.wm0)
		}
	}
	if got := s.Table()["a"]; got != 7 {
		t.Fatalf("table[a]=%d, want 7", got)
	}
}

// TestPartitionIndependence: writing partition p touches no other partition.
func TestPartitionIndependence(t *testing.T) {
	s := New(4)
	err := s.Write([]wm.Rec{{Partition: 0, Offset: 0, Key: "a", Val: 1},
		{Partition: 0, Offset: 2, Key: "b", Val: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for p := 1; p < 4; p++ {
		if s.Watermark(p) != -1 {
			t.Fatalf("partition %d touched: wm=%d", p, s.Watermark(p))
		}
	}
	if s.Watermark(0) != 2 {
		t.Fatalf("own watermark=%d, want 2", s.Watermark(0))
	}
}

// TestRestartIndependence: restarts at batch boundaries change nothing.
func TestRestartIndependence(t *testing.T) {
	batches := [][]wm.Rec{
		{{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 1, Offset: 0, Key: "b", Val: 5}},
		{{Partition: 0, Offset: 1, Key: "a", Val: 2}, {Partition: 1, Offset: 0, Key: "b", Val: 5},
			{Partition: 1, Offset: 2, Key: "a", Val: 3}},
		{{Partition: 0, Offset: 0, Key: "a", Val: 1}, {Partition: 0, Offset: 3, Key: "b", Val: 4}},
	}
	run := func(restart bool) (map[string]int64, int64) {
		s := New(4)
		for _, b := range batches {
			if err := s.Write(b); err != nil {
				t.Fatal(err)
			}
			if restart {
				s.Restart()
			}
		}
		return s.Table(), s.Duplicates()
	}
	t1, d1 := run(false)
	t2, d2 := run(true)
	if d1 != d2 || len(t1) != len(t2) {
		t.Fatalf("restart changed outcome: dups %d vs %d", d1, d2)
	}
	for k, v := range t1 {
		if t2[k] != v {
			t.Fatalf("restart changed table[%s]: %d vs %d", k, v, t2[k])
		}
	}
}
