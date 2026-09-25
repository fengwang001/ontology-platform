package verify

import "testing"

// fill appends records (1, v)..(n, v) with v = seq*10 and returns the log.
func fill(t *testing.T, segSize, n int) *Log {
	t.Helper()
	l, err := NewLog(segSize)
	if err != nil {
		t.Fatalf("NewLog(%d): %v", segSize, err)
	}
	for seq := int64(1); seq <= int64(n); seq++ {
		if err := l.Append(seq, seq*10); err != nil {
			t.Fatalf("Append(%d): %v", seq, err)
		}
	}
	return l
}

// naiveFirstCorrupt is the plain reference: recompute e record by record
// in ascending Seq order and return the first mismatch.
func naiveFirstCorrupt(l *Log, from, to int64) (int64, bool) {
	for seq := from; seq <= to; seq++ {
		if l.es[seq-1] != seq*100+l.vals[seq-1] {
			return seq, false
		}
	}
	return 0, true
}

// TestVerifyMatchesNaive pins invariant 1: Verify agrees with the naive
// ascending reference on every scenario.
func TestVerifyMatchesNaive(t *testing.T) {
	cases := []struct {
		segSize, n int
		corrupt    [][2]int64 // {seq, newVal}
		from, to   int64
	}{
		{3, 6, nil, 1, 6},
		{3, 6, [][2]int64{{4, 70}}, 1, 6},
		{3, 6, [][2]int64{{4, 70}, {5, 80}}, 1, 6},
		{3, 6, [][2]int64{{5, 80}}, 1, 5},
		{1, 9, [][2]int64{{9, 1}}, 1, 9},
		{4, 16, [][2]int64{{2, 1}, {7, 1}, {16, 1}}, 3, 12},
		{5, 25, [][2]int64{{25, 0}}, 20, 25},
	}
	for _, c := range cases {
		l := fill(t, c.segSize, c.n)
		for _, d := range c.corrupt {
			if err := l.Corrupt(d[0], d[1]); err != nil {
				t.Fatalf("Corrupt(%d): %v", d[0], err)
			}
		}
		wantSeq, wantOK := naiveFirstCorrupt(l, c.from, c.to)
		gotSeq, gotOK, err := l.Verify(c.from, c.to)
		if err != nil || gotSeq != wantSeq || gotOK != wantOK {
			t.Errorf("%+v: Verify=(%d,%v,%v), naive=(%d,%v)", c, gotSeq, gotOK, err, wantSeq, wantOK)
		}
	}
}

// TestTotalConsistent pins invariant 2: after every Append, total grows by
// exactly e and equals the sum of all segment sums.
func TestTotalConsistent(t *testing.T) {
	for _, segSize := range []int{1, 2, 3, 7} {
		l := fill(t, segSize, 0)
		var want int64
		for seq := int64(1); seq <= 50; seq++ {
			if err := l.Append(seq, seq*3-1); err != nil {
				t.Fatalf("segSize=%d Append(%d): %v", segSize, seq, err)
			}
			want += seq*100 + seq*3 - 1
			var segSum int64
			for _, s := range l.segs {
				segSum += s
			}
			if l.Total() != want || segSum != want {
				t.Fatalf("segSize=%d seq=%d: total=%d segsum=%d want=%d", segSize, seq, l.Total(), segSum, want)
			}
		}
	}
}

// TestFirstCorruptMinSeq pins invariant 3: the returned Seq is the smallest
// corrupted one inside the range; a clean range reports ok.
func TestFirstCorruptMinSeq(t *testing.T) {
	l := fill(t, 3, 6)
	for _, d := range [][2]int64{{4, 70}, {5, 80}} {
		if err := l.Corrupt(d[0], d[1]); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok, err := l.Verify(1, 6); err != nil || ok || got != 4 {
		t.Errorf("Verify(1,6)=(%d,%v,%v), want (4,false,nil)", got, ok, err)
	}
	if got, ok, err := l.Verify(5, 6); err != nil || ok || got != 5 {
		t.Errorf("Verify(5,6)=(%d,%v,%v), want (5,false,nil)", got, ok, err)
	}
	if _, ok, err := l.Verify(1, 3); err != nil || !ok {
		t.Errorf("Verify(1,3) ok=%v err=%v, want ok", ok, err)
	}
}

// TestCheckedNotLinearInN pins the complexity bound: verifying a fixed
// 10-record window touches at most 10 records no matter how large N is.
func TestCheckedNotLinearInN(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		l := fill(t, 7, n)
		if _, ok, err := l.Verify(int64(n)-9, int64(n)); err != nil || !ok {
			t.Fatalf("N=%d: Verify failed: %v", n, err)
		}
		if got := l.checked.Load(); got > 10 {
			t.Errorf("N=%d: checked %d records for a 10-record window", n, got)
		}
	}
}

// TestRecomputeHeals: after corruption the aggregates drift; Recompute
// rebuilds them from current values; a bad range changes nothing.
func TestRecomputeHeals(t *testing.T) {
	l := fill(t, 3, 6)
	if err := l.Corrupt(4, 70); err != nil {
		t.Fatal(err)
	}
	before := l.Total()
	if _, _, err := l.Recompute(0, 6); err != ErrBadRange {
		t.Fatalf("Recompute(0,6) err=%v, want ErrBadRange", err)
	}
	if l.Total() != before {
		t.Fatal("rejected Recompute changed total")
	}
	segs, total, err := l.Recompute(1, 6)
	if err != nil || len(segs) != 2 || segs[0] != 660 || segs[1] != 1680 || total != 2340 {
		t.Errorf("Recompute=(%v,%d,%v), want ([660 1680],2340,nil)", segs, total, err)
	}
}
