package overagg

import (
	"fmt"
	"reflect"
	"testing"
)

func out(key string, ts, val, sum, cnt, max int64) Output {
	return Output{Key: []byte(key), Ts: ts, Val: val, Sum: sum, Cnt: cnt, Max: max}
}

func mustInsert(t *testing.T, a *Aggregator, key string, ts, val int64, want InsertResult) Output {
	t.Helper()
	got, o := a.Insert([]byte(key), ts, val)
	if got != want {
		t.Fatalf("Insert(%q,%d,%d) = %v, want %v", key, ts, val, got, want)
	}
	return o
}

func mustAdvance(t *testing.T, a *Aggregator, w int64, want []Output) {
	t.Helper()
	got, err := a.Advance(w)
	if err != nil {
		t.Fatalf("Advance(%d) unexpected error: %v", w, err)
	}
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Advance(%d) = %v, want %v", w, got, want)
	}
}

func mustRetained(t *testing.T, a *Aggregator, want int64) {
	t.Helper()
	if got := a.Retained(); got != want {
		t.Fatalf("Retained() = %d, want %d", got, want)
	}
}

func mustNew(t *testing.T, r, al, cap int64) *Aggregator {
	t.Helper()
	a, err := New(r, al, cap)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) unexpected error: %v", r, al, cap, err)
	}
	return a
}

// TestSpecExampleBasic replays the worked example from the specification.
func TestSpecExampleBasic(t *testing.T) {
	a := mustNew(t, 2, 0, MaxCap)

	mustInsert(t, a, "a", 5, 10, InsertBuffered) // seq 1
	mustInsert(t, a, "a", 3, 1, InsertBuffered)  // seq 2
	mustInsert(t, a, "a", 5, 20, InsertBuffered) // seq 3
	mustInsert(t, a, "b", 4, 7, InsertBuffered)  // seq 4

	mustAdvance(t, a, 4, []Output{
		out("a", 3, 1, 1, 1, 1),
		out("b", 4, 7, 7, 1, 7),
	})
	mustRetained(t, a, 2)

	mustAdvance(t, a, 5, []Output{
		out("a", 5, 10, 31, 3, 20),
		out("a", 5, 20, 31, 3, 20),
	})
	mustRetained(t, a, 3)

	mustInsert(t, a, "a", 5, 99, InsertLate)
	mustInsert(t, a, "a", 6, 4, InsertBuffered) // seq 5
	if got := a.Seq(); got != 5 {
		t.Fatalf("Seq() = %d, want 5", got)
	}

	mustAdvance(t, a, 8, []Output{
		out("a", 6, 4, 34, 3, 20),
	})
	mustRetained(t, a, 0)

	if _, err := a.Advance(7); err != ErrWatermarkRegression {
		t.Fatalf("Advance(7) err = %v, want ErrWatermarkRegression", err)
	}
	if got := a.WM(); got != 8 {
		t.Fatalf("WM() = %d, want 8", got)
	}
}

// TestSpecExampleReissue replays the reissue example from the specification.
func TestSpecExampleReissue(t *testing.T) {
	a := mustNew(t, 2, 2, MaxCap)

	mustInsert(t, a, "a", 5, 10, InsertBuffered)
	mustAdvance(t, a, 5, []Output{out("a", 5, 10, 10, 1, 10)})
	mustRetained(t, a, 1)

	if got, want := mustInsert(t, a, "a", 4, 3, InsertReissued), out("a", 4, 3, 3, 1, 3); !reflect.DeepEqual(got, want) {
		t.Fatalf("reissue (a,4,3) = %+v, want %+v", got, want)
	}
	if got, want := mustInsert(t, a, "a", 5, 7, InsertReissued), out("a", 5, 7, 20, 3, 10); !reflect.DeepEqual(got, want) {
		t.Fatalf("reissue (a,5,7) = %+v, want %+v", got, want)
	}
	mustInsert(t, a, "a", 3, 1, InsertLate)
	mustAdvance(t, a, 8, nil)
	mustRetained(t, a, 2)
}

// TestLateBoundaryALZero: with AL=0, ts == wm is dropped while ts == wm+1
// is accepted into the buffer.
func TestLateBoundaryALZero(t *testing.T) {
	a := mustNew(t, 2, 0, MaxCap)
	mustInsert(t, a, "a", 5, 1, InsertBuffered)
	mustAdvance(t, a, 5, []Output{out("a", 5, 1, 1, 1, 1)})

	mustInsert(t, a, "a", 5, 2, InsertLate)     // ts == wm dropped
	mustInsert(t, a, "a", 6, 3, InsertBuffered) // wm+1 accepted
	mustAdvance(t, a, 6, []Output{out("a", 6, 3, 4, 2, 3)})

	st := a.Stats()
	if st.Late != 1 || st.Reissued != 0 {
		t.Fatalf("Stats = %+v, want Late=1 Reissued=0", st)
	}
}

// TestLateBoundaryALPositive: with AL>0, ts == wm-AL is dropped while
// ts == wm-AL+1 is reissued.
func TestLateBoundaryALPositive(t *testing.T) {
	a := mustNew(t, 2, 3, MaxCap)
	mustInsert(t, a, "a", 10, 1, InsertBuffered)
	mustAdvance(t, a, 10, []Output{out("a", 10, 1, 1, 1, 1)})
	// wm = 10, wm-AL = 7.
	mustInsert(t, a, "a", 7, 9, InsertLate) // ts == wm-AL dropped
	got := mustInsert(t, a, "a", 8, 2, InsertReissued)
	// Frame [6,8] excludes the retained row at ts=10.
	if want := out("a", 8, 2, 2, 1, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("reissue (a,8,2) = %+v, want %+v", got, want)
	}
}

// TestReissueFrameContents: a reissue frame excludes released rows with
// larger ts but includes same-ts released rows and earlier reissued rows.
func TestReissueFrameContents(t *testing.T) {
	a := mustNew(t, 3, 10, MaxCap)
	mustInsert(t, a, "a", 10, 5, InsertBuffered)
	mustInsert(t, a, "a", 8, 1, InsertBuffered)
	mustAdvance(t, a, 10, []Output{
		out("a", 8, 1, 1, 1, 1),
		out("a", 10, 5, 6, 2, 5),
	})

	// Frame [6,9]: contains a8 but not a10 (larger ts).
	if got, want := mustInsert(t, a, "a", 9, 2, InsertReissued), out("a", 9, 2, 3, 2, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("reissue (a,9,2) = %+v, want %+v", got, want)
	}
	// Frame [7,10]: contains a8, a10 (same ts), the earlier reissue a9, self.
	if got, want := mustInsert(t, a, "a", 10, 7, InsertReissued), out("a", 10, 7, 15, 4, 7); !reflect.DeepEqual(got, want) {
		t.Fatalf("reissue (a,10,7) = %+v, want %+v", got, want)
	}
	// A no-op advance must not alter retained rows or re-emit anything.
	mustAdvance(t, a, 10, nil)
	mustRetained(t, a, 4)
}

// TestReissueAndLateIgnoreCap: late and reissued rows are never reported as
// buffer-full, and reissues are not limited by Cap.
func TestReissueAndLateIgnoreCap(t *testing.T) {
	a := mustNew(t, 2, 5, 1)
	mustInsert(t, a, "z", 100, 1, InsertBuffered) // buffer now at Cap
	mustInsert(t, a, "y", 101, 1, InsertFull)
	mustAdvance(t, a, 10, nil) // wm=10, z100 stays buffered

	mustInsert(t, a, "a", 5, 1, InsertLate)     // 5 <= 10-5, dropped, not full
	mustInsert(t, a, "a", 9, 9, InsertReissued) // reissue despite full buffer
	mustInsert(t, a, "a", 8, 8, InsertReissued)
	mustInsert(t, a, "y", 101, 1, InsertFull) // buffer still full

	st := a.Stats()
	if st.Late != 1 || st.Reissued != 2 || st.Full != 2 || st.Buffered != 1 {
		t.Fatalf("Stats = %+v, want Late=1 Reissued=2 Full=2 Buffered=1", st)
	}
}

// TestCleanupBoundaryALZero: with AL=0 the cleanup line is w-R; a row at
// exactly w-R is cleaned while one at w-R+1 is retained.
func TestCleanupBoundaryALZero(t *testing.T) {
	a := mustNew(t, 2, 0, MaxCap)
	mustInsert(t, a, "a", 3, 1, InsertBuffered) // ts == 5-2: cleaned
	mustInsert(t, a, "a", 4, 1, InsertBuffered) // ts == 5-2+1: retained
	mustInsert(t, a, "a", 5, 1, InsertBuffered)
	mustAdvance(t, a, 5, []Output{
		out("a", 3, 1, 1, 1, 1),
		out("a", 4, 1, 2, 2, 1),
		out("a", 5, 1, 3, 3, 1),
	})
	mustRetained(t, a, 2)
}

// TestCleanupBoundaryWithAL: the cleanup line is w-R-AL.
func TestCleanupBoundaryWithAL(t *testing.T) {
	a := mustNew(t, 2, 2, MaxCap)
	mustInsert(t, a, "b", 4, 1, InsertBuffered) // ts == 8-2-2: cleaned at Advance(8)
	mustInsert(t, a, "b", 5, 1, InsertBuffered) // ts == 8-2-2+1: retained
	mustInsert(t, a, "b", 6, 1, InsertBuffered)
	mustAdvance(t, a, 6, []Output{
		out("b", 4, 1, 1, 1, 1),
		out("b", 5, 1, 2, 2, 1),
		out("b", 6, 1, 3, 3, 1),
	})
	mustRetained(t, a, 3) // cleanup line 6-2-2=2 removes nothing
	mustAdvance(t, a, 8, nil)
	mustRetained(t, a, 2) // cleanup line 8-2-2=4 removes b4
}

// TestTieFrames: same-key rows at the same ts released in one Advance all
// see each other, including ties ordered after them.
func TestTieFrames(t *testing.T) {
	a := mustNew(t, 2, 0, MaxCap)
	mustInsert(t, a, "a", 5, 10, InsertBuffered)
	mustInsert(t, a, "a", 5, 20, InsertBuffered)
	mustInsert(t, a, "a", 5, -5, InsertBuffered)
	mustAdvance(t, a, 5, []Output{
		out("a", 5, 10, 25, 3, 20),
		out("a", 5, 20, 25, 3, 20),
		out("a", 5, -5, 25, 3, 20),
	})
}

// TestFrameLowerBound: ts-R is included in the frame, ts-R-1 is excluded.
func TestFrameLowerBound(t *testing.T) {
	a := mustNew(t, 3, 0, MaxCap)
	mustInsert(t, a, "a", 4, 1, InsertBuffered)
	mustInsert(t, a, "a", 7, 2, InsertBuffered)
	mustInsert(t, a, "a", 8, 3, InsertBuffered)
	mustAdvance(t, a, 8, []Output{
		out("a", 4, 1, 1, 1, 1),
		out("a", 7, 2, 3, 2, 2), // 7-3=4 included
		out("a", 8, 3, 5, 2, 3), // 8-3-1=4 excluded
	})
}

// TestRZero: with R=0 a frame contains only rows at the same ts.
func TestRZero(t *testing.T) {
	a := mustNew(t, 0, 0, MaxCap)
	mustInsert(t, a, "a", 5, 1, InsertBuffered)
	mustInsert(t, a, "a", 5, 2, InsertBuffered)
	mustInsert(t, a, "a", 6, 3, InsertBuffered)
	mustAdvance(t, a, 6, []Output{
		out("a", 5, 1, 3, 2, 2),
		out("a", 5, 2, 3, 2, 2),
		out("a", 6, 3, 3, 1, 3),
	})
}

// TestCrossKeyTies: cross-key ties are ordered by seq and frames never mix.
func TestCrossKeyTies(t *testing.T) {
	a := mustNew(t, 5, 0, MaxCap)
	mustInsert(t, a, "b", 5, 1, InsertBuffered)  // seq 1
	mustInsert(t, a, "a", 5, 10, InsertBuffered) // seq 2
	mustInsert(t, a, "b", 5, 2, InsertBuffered)  // seq 3
	mustAdvance(t, a, 5, []Output{
		out("b", 5, 1, 3, 2, 2),
		out("a", 5, 10, 10, 1, 10),
		out("b", 5, 2, 3, 2, 2),
	})
}

// TestEarlierTsFeedsLaterFrame: within one Advance, rows released at an
// earlier ts join the frames of rows released at a later ts.
func TestEarlierTsFeedsLaterFrame(t *testing.T) {
	a := mustNew(t, 10, 0, MaxCap)
	mustInsert(t, a, "a", 3, 5, InsertBuffered)
	mustInsert(t, a, "a", 9, 7, InsertBuffered)
	mustAdvance(t, a, 9, []Output{
		out("a", 3, 5, 5, 1, 5),
		out("a", 9, 7, 12, 2, 7),
	})
}

// TestMaxFallback: the max falls back once the row holding it leaves the frame.
func TestMaxFallback(t *testing.T) {
	a := mustNew(t, 2, 0, MaxCap)
	mustInsert(t, a, "a", 3, 100, InsertBuffered)
	mustInsert(t, a, "a", 4, 1, InsertBuffered)
	mustInsert(t, a, "a", 5, 2, InsertBuffered)
	mustInsert(t, a, "a", 6, 3, InsertBuffered)
	mustAdvance(t, a, 6, []Output{
		out("a", 3, 100, 100, 1, 100),
		out("a", 4, 1, 101, 2, 100),
		out("a", 5, 2, 103, 3, 100),
		out("a", 6, 3, 6, 3, 3), // 100 at ts=3 is outside [4,6]
	})
}

// TestInvalidParams: constructor and call arguments out of range are
// rejected as invalid.
func TestInvalidParams(t *testing.T) {
	for _, args := range [][3]int64{
		{-1, 0, 1}, {MaxParam + 1, 0, 1},
		{0, -1, 1}, {0, MaxParam + 1, 1},
		{0, 0, 0}, {0, 0, MaxCap + 1},
	} {
		if _, err := New(args[0], args[1], args[2]); err != ErrInvalidParam {
			t.Fatalf("New%v err = %v, want ErrInvalidParam", args, err)
		}
	}

	a := mustNew(t, 2, 2, 10)
	mustInsert(t, a, "", 1, 1, InsertInvalid)
	mustInsert(t, a, "a", -1, 1, InsertInvalid)
	mustInsert(t, a, "a", MaxParam+1, 1, InsertInvalid)
	mustInsert(t, a, "a", 1, MaxVal+1, InsertInvalid)
	mustInsert(t, a, "a", 1, -MaxVal-1, InsertInvalid)
	mustInsert(t, a, "a", 1, MaxVal, InsertBuffered)
	mustInsert(t, a, "a", 1, -MaxVal, InsertBuffered)

	st := a.Stats()
	if st.Invalid != 5 || st.Buffered != 2 {
		t.Fatalf("Stats = %+v, want Invalid=5 Buffered=2", st)
	}
}

// TestAdvanceRejection: invalid watermarks and regressions are rejected in
// that order, a no-op advance changes nothing, and rejected advances leave
// every piece of state untouched.
func TestAdvanceRejection(t *testing.T) {
	a := mustNew(t, 2, 0, MaxCap)
	mustInsert(t, a, "a", 5, 1, InsertBuffered)
	mustAdvance(t, a, 5, []Output{out("a", 5, 1, 1, 1, 1)})
	mustInsert(t, a, "a", 9, 2, InsertBuffered)

	snap := func() string {
		st := a.Stats()
		return fmt.Sprintf("%d/%d/%d/%d/%+v", a.WM(), a.Buffered(), a.Retained(), a.Seq(), st)
	}
	before := snap()

	if _, err := a.Advance(-1); err != ErrInvalidParam {
		t.Fatalf("Advance(-1) err = %v, want ErrInvalidParam", err)
	}
	if _, err := a.Advance(MaxParam + 1); err != ErrInvalidParam {
		t.Fatalf("Advance(MaxParam+1) err = %v, want ErrInvalidParam", err)
	}
	if _, err := a.Advance(4); err != ErrWatermarkRegression {
		t.Fatalf("Advance(4) err = %v, want ErrWatermarkRegression", err)
	}
	if got, err := a.Advance(5); err != nil || len(got) != 0 {
		t.Fatalf("Advance(5) no-op = %v, %v", got, err)
	}
	if after := snap(); after != before {
		t.Fatalf("rejected/no-op advances changed state: %q -> %q", before, after)
	}
}

// TestRejectedInsertKeepsState: invalid and buffer-full inserts must not
// touch the watermark, buffer, released rows, seq, or the late/reissue
// counters.
func TestRejectedInsertKeepsState(t *testing.T) {
	a := mustNew(t, 2, 2, 1)
	mustInsert(t, a, "a", 5, 1, InsertBuffered)
	mustAdvance(t, a, 5, []Output{out("a", 5, 1, 1, 1, 1)})
	mustInsert(t, a, "a", 9, 2, InsertBuffered) // buffer at Cap=1

	type snapshot struct {
		wm, buffered, retained, seq, late, reissued int64
	}
	snap := func() snapshot {
		st := a.Stats()
		return snapshot{a.WM(), int64(a.Buffered()), a.Retained(), a.Seq(), st.Late, st.Reissued}
	}
	before := snap()

	mustInsert(t, a, "", 9, 9, InsertInvalid) // invalid checked before full
	mustInsert(t, a, "b", 99, 1, InsertFull)

	if after := snap(); after != before {
		t.Fatalf("rejected inserts changed state: %+v -> %+v", before, after)
	}
}

// TestFrameWorkBound: 20000 rows on one key with strictly increasing ts;
// frameWork must stay within 4x the released row count for both R=10 and
// R=1e9.
func TestFrameWorkBound(t *testing.T) {
	const n = 20000
	for _, r := range []int64{10, 1_000_000_000} {
		a := mustNew(t, r, 0, MaxCap)
		for i := int64(0); i < n; i++ {
			mustInsert(t, a, "k", i, i%97, InsertBuffered)
		}
		outs, err := a.Advance(n - 1)
		if err != nil {
			t.Fatalf("Advance: %v", err)
		}
		if len(outs) != n {
			t.Fatalf("R=%d: got %d outputs, want %d", r, len(outs), n)
		}
		if a.frameWork > 4*n {
			t.Fatalf("R=%d: frameWork=%d exceeds 4*%d", r, a.frameWork, n)
		}
		t.Logf("R=%d: frameWork=%d (budget %d)", r, a.frameWork, 4*n)
	}
}

// TestLateWorkBound: the lateWork spent on one reissue is bounded by the
// number of distinct in-frame timestamps plus 2 and does not depend on the
// number of out-of-frame retained timestamps (10 vs 10000).
func TestLateWorkBound(t *testing.T) {
	var deltas []int64
	for _, f := range []int64{10, 10000} {
		a := mustNew(t, 10, MaxParam, MaxCap)
		for i := int64(0); i < f; i++ {
			mustInsert(t, a, "k", 100+i, 1, InsertBuffered) // out-of-frame buckets
		}
		mustInsert(t, a, "k", 20000, 1, InsertBuffered) // in frame
		mustInsert(t, a, "k", 20005, 1, InsertBuffered) // in frame
		mustInsert(t, a, "k", 20010, 1, InsertBuffered) // beyond frame
		if _, err := a.Advance(20010); err != nil {
			t.Fatalf("F=%d: Advance: %v", f, err)
		}
		mustRetained(t, a, f+3) // cleanup line 20010-10-1e15 < 0

		before := a.lateWork
		got := mustInsert(t, a, "k", 20008, 9, InsertReissued)
		delta := a.lateWork - before

		if want := out("k", 20008, 9, 11, 3, 9); !reflect.DeepEqual(got, want) {
			t.Fatalf("F=%d: reissue = %+v, want %+v", f, got, want)
		}
		if delta > 2+2 {
			t.Fatalf("F=%d: lateWork delta %d exceeds in-frame buckets + 2", f, delta)
		}
		deltas = append(deltas, delta)
		t.Logf("F=%d: lateWork delta=%d", f, delta)
	}
	if deltas[0] != deltas[1] {
		t.Fatalf("lateWork delta differs: F=10 -> %d, F=10000 -> %d", deltas[0], deltas[1])
	}
}
