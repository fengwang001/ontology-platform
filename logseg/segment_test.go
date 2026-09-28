package logseg_test

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/logseg"
)

// naiveScan is the reference implementation: scan from the head and return
// the first record whose position >= target.
func naiveScan(records []logseg.Record, target int64) (logseg.Record, error) {
	for _, r := range records {
		if r.Position >= target {
			return r, nil
		}
	}
	return logseg.Record{}, logseg.ErrNotFound
}

// recomputeIndex rebuilds the sparse index from scratch per the spec rule:
// the first record is always sampled; afterwards an entry is taken before
// writing a record when the bytes accumulated since the last entry reach
// the interval.
func recomputeIndex(records []logseg.Record, base, interval int64) []logseg.IndexEntry {
	var idx []logseg.IndexEntry
	var total, since int64
	for i, r := range records {
		if i == 0 || since >= interval {
			idx = append(idx, logseg.IndexEntry{RelPos: int32(r.Position - base), PhysOff: total})
			since = 0
		}
		total += r.Size
		since += r.Size
	}
	return idx
}

func logIndex(t *testing.T, seg *logseg.Segment) {
	t.Helper()
	t.Logf("index=%v len=%d totalBytes=%d", seg.Index(), seg.Len(), seg.TotalBytes())
}

// checkIndexInvariants verifies both fields are strictly increasing and the
// index equals a from-scratch recomputation.
func checkIndexInvariants(t *testing.T, seg *logseg.Segment, records []logseg.Record, base, interval int64) {
	t.Helper()
	got := seg.Index()
	want := recomputeIndex(records, base, interval)
	if len(got) != len(want) {
		t.Fatalf("index length %d, recomputed %d", len(got), len(want))
	}
	for i, e := range got {
		if e.RelPos != want[i].RelPos || e.PhysOff != want[i].PhysOff {
			t.Fatalf("entry %d = %+v, recomputed %+v", i, e, want[i])
		}
		if i > 0 && (e.RelPos <= got[i-1].RelPos || e.PhysOff <= got[i-1].PhysOff) {
			t.Fatalf("entry %d not strictly increasing: %+v after %+v", i, e, got[i-1])
		}
	}
	t.Logf("index invariant ok: %d entries strictly increasing and match recomputation", len(got))
}

func appendOK(t *testing.T, seg *logseg.Segment, records *[]logseg.Record, pos, size int64) {
	t.Helper()
	t.Logf("append input: position=%d size=%d", pos, size)
	if err := seg.Append(pos, size); err != nil {
		t.Fatalf("append(%d,%d) rejected: %v", pos, size, err)
	}
	*records = append(*records, logseg.Record{Position: pos, Size: size})
	logIndex(t, seg)
}

func lookupOK(t *testing.T, seg *logseg.Segment, records []logseg.Record, target int64) logseg.Record {
	t.Helper()
	got, err := seg.Lookup(target)
	want, wantErr := naiveScan(records, target)
	t.Logf("lookup input: target=%d -> got=%+v err=%v; naive=%+v err=%v", target, got, err, want, wantErr)
	if !errors.Is(err, wantErr) || got != want {
		t.Fatalf("lookup(%d) = %+v,%v; naive scan = %+v,%v", target, got, err, want, wantErr)
	}
	return got
}

func TestIndexBoundary(t *testing.T) {
	const base, interval = 1000, 100
	seg, err := logseg.NewSegment(base, interval)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	var records []logseg.Record
	// First record is always sampled. Sizes 60+40 hit the interval exactly,
	// so the entry must be taken on the *next* record (>=, not >).
	appendOK(t, seg, &records, 1000, 60)
	appendOK(t, seg, &records, 1005, 40)
	appendOK(t, seg, &records, 1010, 10)
	appendOK(t, seg, &records, 1020, 100)
	appendOK(t, seg, &records, 1030, 1)

	want := []logseg.IndexEntry{
		{RelPos: 0, PhysOff: 0},
		{RelPos: 10, PhysOff: 100},
		{RelPos: 30, PhysOff: 210},
	}
	got := seg.Index()
	if len(got) != len(want) {
		t.Fatalf("index=%v, want %v", got, want)
	}
	for i := range want {
		if got[i].RelPos != want[i].RelPos || got[i].PhysOff != want[i].PhysOff {
			t.Fatalf("entry %d = %+v, want %+v (boundary rule: sample when accumulated >= interval)", i, got[i], want[i])
		}
	}
	t.Logf("boundary decision basis: entries at accumulated bytes 0, 100(exact), 210; got %v", got)
	checkIndexInvariants(t, seg, records, base, interval)
}

func TestLookupBetweenEntriesAndGaps(t *testing.T) {
	const base, interval = 0, 50
	seg, _ := logseg.NewSegment(base, interval)
	var records []logseg.Record
	// Positions carry holes; sizes force several index entries.
	for _, rs := range [][2]int64{{3, 30}, {7, 30}, {42, 10}, {43, 60}, {90, 5}, {200, 5}} {
		appendOK(t, seg, &records, rs[0], rs[1])
	}
	// Targets inside holes and between index entries.
	for _, target := range []int64{0, 3, 4, 7, 8, 41, 42, 44, 89, 90, 91, 199, 200} {
		lookupOK(t, seg, records, target)
	}
	checkIndexInvariants(t, seg, records, base, interval)
}

func TestLookupNotFound(t *testing.T) {
	seg, _ := logseg.NewSegment(10, 16)
	if _, err := seg.Lookup(10); !errors.Is(err, logseg.ErrNotFound) {
		t.Fatalf("empty segment lookup err=%v, want ErrNotFound", err)
	}
	t.Logf("empty segment: lookup(10) -> ErrNotFound (no records to scan)")
	var records []logseg.Record
	appendOK(t, seg, &records, 10, 8)
	appendOK(t, seg, &records, 20, 8)
	if _, err := seg.Lookup(21); !errors.Is(err, logseg.ErrNotFound) {
		t.Fatalf("lookup above last position err=%v, want ErrNotFound", err)
	}
	t.Logf("target=21 above last position=20 -> ErrNotFound (scan from last entry reaches end)")
	if _, err := seg.Lookup(math.MaxInt64); !errors.Is(err, logseg.ErrNotFound) {
		t.Fatalf("lookup at MaxInt64 err=%v, want ErrNotFound", err)
	}
	t.Logf("target=MaxInt64 beyond relative range -> ErrNotFound")
}

func TestRejections(t *testing.T) {
	if _, err := logseg.NewSegment(0, 0); !errors.Is(err, logseg.ErrInvalidInterval) {
		t.Fatalf("interval=0 err=%v, want ErrInvalidInterval", err)
	}
	if _, err := logseg.NewSegment(-1, 8); !errors.Is(err, logseg.ErrInvalidBasePosition) {
		t.Fatalf("base=-1 err=%v, want ErrInvalidBasePosition", err)
	}
	t.Logf("constructor rejects: interval<=0 -> ErrInvalidInterval, base<0 -> ErrInvalidBasePosition")

	const base, interval = 100, 16
	seg, _ := logseg.NewSegment(base, interval)
	var records []logseg.Record
	appendOK(t, seg, &records, 100, 8)
	appendOK(t, seg, &records, 110, 8)

	snapshot := func() string {
		return fmt.Sprintf("len=%d bytes=%d index=%v", seg.Len(), seg.TotalBytes(), seg.Index())
	}
	reject := func(name string, err error, want error) {
		t.Helper()
		before := snapshot()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err=%v, want %v", name, err, want)
		}
		after := snapshot()
		if after != before {
			t.Fatalf("%s: state changed after rejection: %s -> %s", name, before, after)
		}
		t.Logf("%s rejected with %v; state unchanged: %s", name, err, after)
	}

	reject("append size=0", seg.Append(120, 0), logseg.ErrInvalidSize)
	reject("append size=-1", seg.Append(120, -1), logseg.ErrInvalidSize)
	reject("append equal position", seg.Append(110, 4), logseg.ErrNonMonotonicPosition)
	reject("append smaller position", seg.Append(105, 4), logseg.ErrNonMonotonicPosition)
	reject("append relative overflow", seg.Append(base+math.MaxInt32+1, 4), logseg.ErrPositionOverflow)

	if _, err := seg.Lookup(base - 1); true {
		reject("lookup below base", err, logseg.ErrBelowBasePosition)
	}

	// Segment still fully functional after rejections.
	appendOK(t, seg, &records, 120, 8)
	lookupOK(t, seg, records, 111)
	checkIndexInvariants(t, seg, records, base, interval)
}

func TestConsistentWithNaiveScan(t *testing.T) {
	const base, interval = 500, 24
	seg, _ := logseg.NewSegment(base, interval)
	var records []logseg.Record
	// Deterministic pseudo-random walk with holes and varied sizes.
	pos, size := int64(base), int64(0)
	for i := 0; i < 200; i++ {
		pos += int64(i%7 + 1)
		size = int64(i%13 + 1)
		appendOK(t, seg, &records, pos, size)
		if i%10 == 0 {
			checkIndexInvariants(t, seg, records, base, interval)
		}
	}
	for target := int64(base); target <= pos+5; target++ {
		lookupOK(t, seg, records, target)
	}
	t.Logf("all %d targets in [%d,%d] match naive scan", pos+5-base+1, base, pos+5)
}

func TestConcurrentAppendAndLookup(t *testing.T) {
	const base, interval = 0, 64
	seg, _ := logseg.NewSegment(base, interval)
	var counter atomic.Int64
	counter.Store(base)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				pos := counter.Add(1)
				err := seg.Append(pos, int64(i%9+1))
				if err != nil && !errors.Is(err, logseg.ErrNonMonotonicPosition) {
					t.Errorf("worker %d: unexpected append error: %v", id, err)
					return
				}
				target := pos - int64(i%50)
				rec, err := seg.Lookup(target)
				switch {
				case errors.Is(err, logseg.ErrNotFound):
				case err != nil:
					t.Errorf("worker %d: unexpected lookup error: %v", id, err)
					return
				case rec.Position < target:
					t.Errorf("worker %d: lookup(%d) = %+v below target", id, target, rec)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	idx := seg.Index()
	for i := 1; i < len(idx); i++ {
		if idx[i].RelPos <= idx[i-1].RelPos || idx[i].PhysOff <= idx[i-1].PhysOff {
			t.Fatalf("index not strictly increasing at %d: %+v after %+v", i, idx[i], idx[i-1])
		}
	}
	t.Logf("concurrent run done: len=%d bytes=%d entries=%d, index strictly increasing",
		seg.Len(), seg.TotalBytes(), len(idx))
}
