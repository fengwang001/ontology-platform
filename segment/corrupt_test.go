package segment

import (
	"errors"
	"testing"
)

func sampleSegment(t *testing.T) []byte {
	t.Helper()
	b := NewBuilder(Config{MaxDictCard: 4})
	g1 := group(1, 2, 3, 2, 1)
	g1.nulls[2] = true
	g2 := group(100, -50, 77, 1000000, -3, 88)
	g3 := group(0, 0)
	g3.nulls[0] = true
	g3.nulls[1] = true
	for _, g := range []struct {
		vals  []int64
		nulls []bool
	}{g1, g2, g3} {
		if err := b.AddRowGroup(g.vals, g.nulls); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

// TestEveryTruncationPoint cuts the segment at every possible byte
// offset and requires a staged CorruptError, never a panic and never
// a partial result.
func TestEveryTruncationPoint(t *testing.T) {
	full := sampleSegment(t)
	if _, err := Open(full); err != nil {
		t.Fatalf("full segment must open: %v", err)
	}
	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("cut=%d panicked: %v", cut, r)
				}
			}()
			truncated := full[:cut]
			seg, err := Open(truncated)
			if err != nil {
				var ce *CorruptError
				if !errors.As(err, &ce) {
					t.Fatalf("cut=%d: error %v is not a CorruptError", cut, err)
				}
				return
			}
			// Open succeeded, so decoding every group must too: the
			// skeleton parse covers the whole segment.
			for g := 0; g < seg.NumRowGroups(); g++ {
				if _, _, err := seg.DecodeRowGroup(g); err != nil {
					t.Fatalf("cut=%d: open ok but group %d fails: %v", cut, g, err)
				}
			}
		}()
	}
}

func TestCorruptErrorStages(t *testing.T) {
	full := sampleSegment(t)
	// Cut inside the header.
	_, err := Open(full[:3])
	var ce *CorruptError
	if !errors.As(err, &ce) || ce.Stage != StageHeader || ce.Group != -1 {
		t.Fatalf("header cut: %v", err)
	}
	// Cut well inside the first group's statistics.
	_, err = Open(full[:headerLen+5])
	if !errors.As(err, &ce) || ce.Stage != StageStats || ce.Group != 0 {
		t.Fatalf("stats cut: %v", err)
	}
}

func TestLimitsLeaveStateUntouched(t *testing.T) {
	b := NewBuilder(Config{MaxRows: 4, MaxRowGroups: 1})
	if err := b.AddRowGroup([]int64{1, 2}, []bool{false, false}); err != nil {
		t.Fatal(err)
	}
	before := b.Bytes()
	if err := b.AddRowGroup([]int64{3}, []bool{false}); err != ErrTooManyRowGroups {
		t.Fatalf("want ErrTooManyRowGroups, got %v", err)
	}
	b2 := NewBuilder(Config{MaxRows: 3})
	if err := b2.AddRowGroup([]int64{1, 2}, []bool{false, false}); err != nil {
		t.Fatal(err)
	}
	if err := b2.AddRowGroup([]int64{3, 4}, []bool{false, false}); err != ErrTooManyRows {
		t.Fatalf("want ErrTooManyRows, got %v", err)
	}
	if b2.Rows() != 2 {
		t.Fatalf("rejected write changed row count: %d", b2.Rows())
	}
	if b.Rows() != 2 || b.RowGroups() != 1 {
		t.Fatalf("rejected writes changed counters: %d rows, %d groups",
			b.Rows(), b.RowGroups())
	}
	after := b.Bytes()
	if len(before) != len(after) {
		t.Fatal("rejected writes changed segment bytes")
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatal("rejected writes changed segment bytes")
		}
	}
	// The three limit errors are mutually distinguishable.
	if errors.Is(ErrTooManyRows, ErrTooManyRowGroups) {
		t.Fatal("limit errors must be distinguishable")
	}
}

func TestDictFallbackToBitpack(t *testing.T) {
	b := NewBuilder(Config{MaxDictCard: 2})
	// Three distinct values exceed the dictionary limit: the group
	// must silently fall back to bit-packing, not fail.
	vals := []int64{10, 20, 30, 10}
	if err := b.AddRowGroup(vals, []bool{false, false, false, false}); err != nil {
		t.Fatalf("cardinality overflow must fall back, got %v", err)
	}
	seg, err := Open(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if seg.GroupEncoding(0) != BitPacked {
		t.Fatalf("encoding %v, want bitpack fallback", seg.GroupEncoding(0))
	}
	checkGroup(t, seg, 0, vals, []bool{false, false, false, false})
}

func TestMetadataQueriesDoNotDecode(t *testing.T) {
	seg, err := Open(sampleSegment(t))
	if err != nil {
		t.Fatal(err)
	}
	a := seg.NumRows()
	g := seg.NumRowGroups()
	s0 := seg.GroupStats(0)
	e0 := seg.GroupEncoding(0)
	// Repeat: results must be identical and no decode may happen.
	if seg.NumRows() != a || seg.NumRowGroups() != g ||
		seg.GroupStats(0) != s0 || seg.GroupEncoding(0) != e0 {
		t.Fatal("metadata queries are not stable")
	}
	if seg.DecodedGroups() != 0 || seg.DecodedValues() != 0 {
		t.Fatalf("metadata queries decoded: groups=%d values=%d",
			seg.DecodedGroups(), seg.DecodedValues())
	}
}
