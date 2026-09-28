package segment

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveLookup scans every record from the start and returns the first whose
// offset is >= target. It is the reference implementation that the sparse
// index lookup must match.
func naiveLookup(records []Record, target int64) (Record, bool) {
	for _, r := range records {
		if r.Offset >= target {
			return r, true
		}
	}
	return Record{}, false
}

// snap records a segment snapshot for "state unchanged" assertions.
type snap struct {
	recordsLen int
	entriesLen int
	totalBytes int64
	lastOffset int64
	hasRecords bool
}

func takeSnap(s *Segment) snap {
	return snap{
		recordsLen: len(s.Records()),
		entriesLen: len(s.Entries()),
		totalBytes: s.TotalBytes(),
		lastOffset: s.lastOffset,
		hasRecords: s.hasRecords,
	}
}

func assertErrIs(t *testing.T, where string, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want error %v, got %v", where, target, err)
	}
}

func TestAppendAndIndexBoundary(t *testing.T) {
	// Interval of 3 bytes; sizes [2,2,2,2,...].
	// Record0: bytesSinceIndex 0 < 3 -> no entry, then 2.
	// Record1: 2 < 3 -> no entry, then 4.
	// Record2: 4 >= 3 -> entry at record2, then 2.
	// Record3: 2 < 3 -> no entry, then 4.
	// Record4: 4 >= 3 -> entry at record4.
	seg, err := NewSegment(100, 3)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	indexed := make([]bool, 5)
	for i := 0; i < 5; i++ {
		indexed[i], err = seg.Append(int64(100+i), 2)
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		t.Logf("append offset=%d size=%d -> indexed=%v; index=%v",
			100+i, 2, indexed[i], seg.Entries())
	}

	want := []Entry{
		{RelativeOffset: 2, Position: 4},
		{RelativeOffset: 4, Position: 8},
	}
	got := seg.Entries()
	if len(got) != len(want) {
		t.Fatalf("entries len: want %d, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entry %d: want %+v, got %+v", i, want[i], got[i])
		}
	}
	if seg.TotalBytes() != 10 {
		t.Fatalf("total bytes: want 10, got %d", seg.TotalBytes())
	}
	// Index entries must be strictly increasing on both fields.
	for i := 1; i < len(got); i++ {
		if got[i].RelativeOffset <= got[i-1].RelativeOffset {
			t.Fatalf("relative offsets not strictly increasing: %v", got)
		}
		if got[i].Position <= got[i-1].Position {
			t.Fatalf("positions not strictly increasing: %v", got)
		}
	}
	// Index must match a from-scratch recomputation.
	records := seg.Records()
	if got := ExpectedEntries(records, seg.BaseOffset(), 3); len(got) != len(want) {
		t.Fatalf("recomputed index mismatch: %v", got)
	}
}

func TestLookupMatchesNaiveScan(t *testing.T) {
	// Offsets with holes: records at 0, 4, 10, 20, 100 with varying sizes.
	seg, err := NewSegment(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	offsets := []int64{0, 4, 10, 20, 100}
	sizes := []int64{3, 7, 1, 9, 2}
	for i, off := range offsets {
		if _, err := seg.Append(off, sizes[i]); err != nil {
			t.Fatalf("append %d: %v", off, err)
		}
	}
	t.Logf("index=%v records=%v", seg.Entries(), seg.Records())

	targets := []int64{
		0,    // exactly the first record
		1, 3, // between record0 and record1 -> record at 4
		4,        // exactly record1
		5, 9, 10, // around record2
		11, 19, 20, // holes -> record at 20
		21, 99, 100, // last record exact / below
		101, // beyond every record -> not found
	}
	for _, target := range targets {
		got, err := seg.Lookup(target)
		want, found := naiveLookup(seg.Records(), target)
		if !found {
			assertErrIs(t, fmt.Sprintf("target=%d", target), err, ErrRecordNotFound)
			t.Logf("lookup target=%d -> not found (matches scan)", target)
			continue
		}
		if err != nil {
			t.Fatalf("target=%d: unexpected error %v", target, err)
		}
		if got != want {
			t.Fatalf("target=%d: index lookup %+v != naive scan %+v", target, got, want)
		}
		t.Logf("lookup target=%d -> record offset=%d position=%d (matches scan)",
			target, got.Offset, got.Position)
	}
}

func TestLookupAtAndAfterIndexedEntry(t *testing.T) {
	// With interval 1, every record after the first is indexed; check that
	// a target landing exactly on / between index entries starts the scan
	// from the correct (last <= target) entry.
	seg, _ := NewSegment(10, 1)
	for i := int64(0); i < 6; i++ {
		if _, err := seg.Append(10+2*i, 1); err != nil { // offsets 10,12,...,20
			t.Fatal(err)
		}
	}
	t.Logf("index=%v", seg.Entries())
	for target := int64(10); target <= 22; target++ {
		got, err := seg.Lookup(target)
		want, found := naiveLookup(seg.Records(), target)
		if !found {
			assertErrIs(t, "tail", err, ErrRecordNotFound)
			continue
		}
		if err != nil || got.Offset != want.Offset {
			t.Fatalf("target=%d: got %+v err=%v, want offset %d", target, got, err, want.Offset)
		}
	}
}

func TestRejectionsLeaveNoTrace(t *testing.T) {
	seg, err := NewSegment(10, 4)
	if err != nil {
		t.Fatal(err)
	}
	must := func(offset, size int64) {
		t.Helper()
		if _, err := seg.Append(offset, size); err != nil {
			t.Fatalf("seed append (%d,%d): %v", offset, size, err)
		}
	}
	must(10, 2) // bytesSinceIndex=2, no entry
	must(11, 2) // 2<4 no entry; bytes=4
	want := takeSnap(seg)

	type badCase struct {
		name   string
		offset int64
		size   int64
		want   error
	}
	cases := []badCase{
		{"zero size", 12, 0, ErrInvalidRecordSize},
		{"negative size", 12, -3, ErrInvalidRecordSize},
		{"offset equals last", 11, 1, ErrNonMonotonicOffset},
		{"offset below last but above base", 10, 1, ErrNonMonotonicOffset},
		{"offset below base", 9, 1, ErrOffsetBelowBase},
		{"relative offset overflow", 10 + math.MaxInt32 + 1, 1, ErrRelativeOffsetOverflow},
	}
	for _, c := range cases {
		indexed, err := seg.Append(c.offset, c.size)
		assertErrIs(t, c.name, err, c.want)
		if indexed {
			t.Fatalf("%s: rejected append must not report an index entry", c.name)
		}
		if got := takeSnap(seg); got != want {
			t.Fatalf("%s: state changed after rejection: want %+v, got %+v", c.name, want, got)
		}
		after := takeSnap(seg)
		t.Logf("rejected append offset=%d size=%d reason=%v; state unchanged: %+v",
			c.offset, c.size, c.want, after)
	}

	// Lookup below base is rejected without touching anything.
	if _, err := seg.Lookup(9); !errors.Is(err, ErrOffsetBelowBase) {
		t.Fatalf("lookup below base: want %v, got %v", ErrOffsetBelowBase, err)
	}
	if got := takeSnap(seg); got != want {
		t.Fatalf("state changed after rejected lookup: %+v", got)
	}
}

func TestNewSegmentRejectsBadInterval(t *testing.T) {
	for _, interval := range []int64{0, -1} {
		if _, err := NewSegment(0, interval); !errors.Is(err, ErrInvalidRecordSize) {
			t.Fatalf("interval=%d: want %v, got %v", interval, ErrInvalidRecordSize, err)
		}
	}
}

func TestRejectedAppendDoesNotConsumeInterval(t *testing.T) {
	// bytesSinceIndex reaches the interval, but the append overflows the
	// relative-offset range: no entry and no bytes may be consumed. A later
	// valid append must still trigger the pending index entry.
	seg, _ := NewSegment(0, 4)
	if _, err := seg.Append(0, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := seg.Append(1, 2); err != nil {
		t.Fatal(err)
	}
	// bytesSinceIndex is now 4 >= interval.
	if _, err := seg.Append(math.MaxInt32+1, 1); !errors.Is(err, ErrRelativeOffsetOverflow) {
		t.Fatalf("overflow rejection: %v", err)
	}
	if n := len(seg.Entries()); n != 0 {
		t.Fatalf("rejected append recorded %d entries", n)
	}
	indexed, err := seg.Append(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatalf("pending index entry must fire on the next valid append; entries=%v", seg.Entries())
	}
	entries := seg.Entries()
	if len(entries) != 1 || entries[0] != (Entry{RelativeOffset: 2, Position: 4}) {
		t.Fatalf("unexpected entries after recovery: %v", entries)
	}
}

func TestConcurrentAppendsAndLookups(t *testing.T) {
	const writers = 16
	const perWriter = 200
	seg, _ := NewSegment(0, 37)

	var wg sync.WaitGroup
	var next int64 = -1 // first claimed offset is 0
	// Writers race for strictly increasing offsets. A writer whose claimed
	// offset loses the race (someone appended past it) handles the
	// ErrNonMonotonicOffset rejection by claiming a fresh offset, exactly
	// as a real multi-writer caller must.
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				offset := atomic.AddInt64(&next, 1)
				for {
					if _, err := seg.Append(offset, 1+int64(offset%5)); err != nil {
						if errors.Is(err, ErrNonMonotonicOffset) {
							offset = atomic.AddInt64(&next, 1)
							continue
						}
						t.Errorf("append %d: %v", offset, err)
						return
					}
					break
				}
			}
		}()
	}
	// Concurrent readers: every lookup must match a naive scan at its point
	// in time. Records observed by the snapshot are append-prefix-stable.
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < perWriter; k++ {
				target := int64(k * 7)
				seg.mu.RLock()
				snapshot := make([]Record, len(seg.records))
				copy(snapshot, seg.records)
				got, err := seg.lookupLocked(target)
				seg.mu.RUnlock()
				want, found := naiveLookup(snapshot, target)
				if !found {
					if !errors.Is(err, ErrRecordNotFound) {
						t.Errorf("lookup %d: want not-found, got %v", target, err)
					}
					continue
				}
				if err != nil {
					t.Errorf("lookup %d: %v", target, err)
					continue
				}
				if got.Offset != want.Offset {
					t.Errorf("lookup %d: got offset %d, want %d", target, got.Offset, want.Offset)
				}
			}
		}()
	}
	wg.Wait()

	records := seg.Records()
	if len(records) != writers*perWriter {
		t.Fatalf("record count: want %d, got %d", writers*perWriter, len(records))
	}
	var total int64
	for i, r := range records {
		if r.Position != total {
			t.Fatalf("record %d position: want %d, got %d", i, total, r.Position)
		}
		if i > 0 && r.Offset <= records[i-1].Offset {
			t.Fatalf("offsets not strictly increasing at %d", i)
		}
		total += r.Size
	}
	if seg.TotalBytes() != total {
		t.Fatalf("total bytes: want %d, got %d", total, seg.TotalBytes())
	}
	entries := seg.Entries()
	for i := 1; i < len(entries); i++ {
		if entries[i].RelativeOffset <= entries[i-1].RelativeOffset ||
			entries[i].Position <= entries[i-1].Position {
			t.Fatalf("index not strictly increasing at %d: %v", i, entries)
		}
	}
	if want := ExpectedEntries(records, 0, 37); len(want) != len(entries) {
		t.Fatalf("recomputed index has %d entries, segment has %d", len(want), len(entries))
	} else {
		for i := range want {
			if want[i] != entries[i] {
				t.Fatalf("entry %d: recomputed %+v != actual %+v", i, want[i], entries[i])
			}
		}
	}
	t.Logf("concurrent run: %d records, %d bytes, %d index entries",
		len(records), total, len(entries))
}
