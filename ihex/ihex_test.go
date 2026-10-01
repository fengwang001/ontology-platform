package ihex

import (
	"encoding/hex"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// mkline builds a valid Intel HEX record line for the given fields.
func mkline(addr uint16, typ byte, data []byte) string {
	raw := []byte{byte(len(data)), byte(addr >> 8), byte(addr), typ}
	raw = append(raw, data...)
	var sum byte
	for _, b := range raw {
		sum += b
	}
	raw = append(raw, -sum)
	return ":" + strings.ToUpper(hex.EncodeToString(raw))
}

func TestMkline(t *testing.T) {
	got := mkline(0, 0x01, nil)
	if got != ":00000001FF" {
		t.Fatalf("mkline EOF = %q, want :00000001FF", got)
	}
}

// mustAdd feeds a line and fails the test on error.
func mustAdd(t *testing.T, l *Loader, line string) {
	t.Helper()
	err := l.AddLine(line)
	t.Logf("input=%q accepted err=%v", line, err)
	if err != nil {
		t.Fatalf("AddLine(%q) unexpected error: %v", line, err)
	}
}

// wantReject feeds a line and requires rejection with the given reason.
func wantReject(t *testing.T, l *Loader, line string, reason error) {
	t.Helper()
	err := l.AddLine(line)
	t.Logf("input=%q rejected err=%v want=%v", line, err, reason)
	if err == nil {
		t.Fatalf("AddLine(%q) accepted, want %v", line, reason)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("AddLine(%q) error %v, want errors.Is %v", line, err, reason)
	}
	var le *LineError
	if !errors.As(err, &le) || le.Line < 1 {
		t.Fatalf("AddLine(%q) error %v lacks line number", line, err)
	}
}

func TestChecksumExactZeroAndCarryWrap(t *testing.T) {
	l := NewLoader()
	// Sum of all bytes is exactly 0x000: every byte is zero.
	mustAdd(t, l, ":0000000000")
	// Sum wraps at 0x100: 02+FF+FF+00 = 0x200 == 0 (mod 256).
	mustAdd(t, l, mkline(0x1000, 0x00, []byte{0xFF, 0xFF}))
	// Bad checksum must be rejected.
	bad := ":0200000000FF00" // 02+FF = 0x101 != 0
	wantReject(t, l, bad, ErrChecksum)
	mustAdd(t, l, mkline(0, 0x01, nil))
	if err := l.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func TestChecksumRejectedLineKeepsState(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0, 0x04, []byte{0x12, 0x34})) // base = 0x12340000
	wantReject(t, l, ":02000000FFFF01", ErrChecksum)   // data record, bad sum
	mustAdd(t, l, mkline(0, 0x00, []byte{0xAB}))
	segs := l.Segments()
	t.Logf("segments=%+v decision: base unchanged by rejected line", segs)
	if len(segs) != 1 || segs[0].Start != 0x12340000 {
		t.Fatalf("segments=%+v, want single segment at 0x12340000", segs)
	}
}

func TestBoundaryExactAndOverflow(t *testing.T) {
	l := NewLoader()
	// AAAA+LL == 0x10000 exactly: allowed.
	mustAdd(t, l, mkline(0xFFF0, 0x00, make([]byte, 0x10)))
	// AAAA+LL == 0x10001: rejected.
	wantReject(t, l, mkline(0xFFF1, 0x00, make([]byte, 0x10)), ErrBoundary)
	// LL == 0 at AAAA 0xFFFF: boundary check applies, 0xFFFF+0 <= 0x10000.
	mustAdd(t, l, mkline(0xFFFF, 0x00, nil))
	// LL 0 data record occupies nothing and triggers no overlap check.
	mustAdd(t, l, mkline(0xFFF0, 0x00, nil))
	mustAdd(t, l, mkline(0, 0x01, nil))
	segs := l.Segments()
	t.Logf("segments=%+v", segs)
	if len(segs) != 1 || segs[0].Start != 0xFFF0 || len(segs[0].Data) != 0x10 {
		t.Fatalf("segments=%+v, want [0xFFF0,+0x10)", segs)
	}
}

func TestBaseOverrideBetween02And04(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0, 0x02, []byte{0x10, 0x00})) // base = 0x10000
	mustAdd(t, l, mkline(0x0010, 0x00, []byte{0xAA}))
	mustAdd(t, l, mkline(0, 0x04, []byte{0x00, 0x20})) // base = 0x200000
	mustAdd(t, l, mkline(0x0020, 0x00, []byte{0xBB}))
	mustAdd(t, l, mkline(0, 0x02, []byte{0x00, 0x30})) // base = 0x300
	mustAdd(t, l, mkline(0x0030, 0x00, []byte{0xCC}))
	mustAdd(t, l, mkline(0, 0x01, nil))
	segs := l.Segments()
	t.Logf("segments=%+v decision: 02->0x10000, 04 overrides->0x200000, 02 overrides->0x300", segs)
	want := []Segment{
		{Start: 0x300 + 0x30, Data: []byte{0xCC}},
		{Start: 0x10000 + 0x10, Data: []byte{0xAA}},
		{Start: 0x200000 + 0x20, Data: []byte{0xBB}},
	}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("segments=%+v, want %+v", segs, want)
	}
}

func TestAdjacentSegmentsMerge(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0x0004, 0x00, []byte{0x05, 0x06}))
	mustAdd(t, l, mkline(0x0000, 0x00, []byte{0x01, 0x02}))
	mustAdd(t, l, mkline(0x0002, 0x00, []byte{0x03, 0x04}))
	mustAdd(t, l, mkline(0, 0x01, nil))
	segs := l.Segments()
	t.Logf("segments=%+v decision: three touching runs merge into one", segs)
	want := []Segment{{Start: 0, Data: []byte{1, 2, 3, 4, 5, 6}}}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("segments=%+v, want %+v", segs, want)
	}
}

func TestOverlapSameValueRejected(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0x0100, 0x00, []byte{0xDE, 0xAD}))
	// Identical bytes at the same address still count as overlap.
	wantReject(t, l, mkline(0x0100, 0x00, []byte{0xDE, 0xAD}), ErrOverlap)
	// Partial overlap at the tail is also rejected.
	wantReject(t, l, mkline(0x0101, 0x00, []byte{0xAD, 0xBE}), ErrOverlap)
	// Touching but not overlapping is fine.
	mustAdd(t, l, mkline(0x0102, 0x00, []byte{0xBE}))
	mustAdd(t, l, mkline(0, 0x01, nil))
	segs := l.Segments()
	t.Logf("segments=%+v decision: rejected rewrites left image untouched", segs)
	want := []Segment{{Start: 0x100, Data: []byte{0xDE, 0xAD, 0xBE}}}
	if !reflect.DeepEqual(segs, want) {
		t.Fatalf("segments=%+v, want %+v", segs, want)
	}
}

func TestInvalidLines(t *testing.T) {
	eof := mkline(0, 0x01, nil)
	cases := []struct {
		name   string
		line   string
		reason error
	}{
		{"missing colon", "00000001FF", ErrSyntax},
		{"non-hex char", ":0000000GFF", ErrSyntax},
		{"odd hex digits", ":00000001F", ErrSyntax},
		{"length mismatch", ":0200000000FF", ErrSyntax},
		{"bad checksum", ":0200000000FE01", ErrChecksum},
		{"unknown type", mkline(0, 0x07, nil), ErrUnknownType},
		{"eof with data", mkline(0, 0x01, []byte{0x00}), ErrBadField},
		{"eof with addr", mkline(1, 0x01, nil), ErrBadField},
		{"type02 short data", mkline(0, 0x02, []byte{0x10}), ErrBadField},
		{"type02 nonzero addr", mkline(4, 0x02, []byte{0x10, 0x00}), ErrBadField},
		{"type04 short data", mkline(0, 0x04, []byte{0x10}), ErrBadField},
		{"type04 nonzero addr", mkline(4, 0x04, []byte{0x00, 0x20}), ErrBadField},
		{"type03 wrong len", mkline(0, 0x03, []byte{0, 0}), ErrBadField},
		{"type05 nonzero addr", mkline(8, 0x05, []byte{0, 0, 0, 0}), ErrBadField},
		{"boundary", mkline(0xFFFF, 0x00, []byte{0x01, 0x02}), ErrBoundary},
		{"after eof", "", ErrAfterEOF}, // placeholder replaced below
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLoader()
			if tc.reason == ErrAfterEOF {
				mustAdd(t, l, eof)
				wantReject(t, l, mkline(0, 0x00, []byte{0x01}), ErrAfterEOF)
				// Even a valid second EOF record is rejected.
				wantReject(t, l, eof, ErrAfterEOF)
				return
			}
			wantReject(t, l, tc.line, tc.reason)
			// Rejected line must not disturb any state.
			mustAdd(t, l, eof)
			if err := l.Finish(); err != nil {
				t.Fatalf("Finish after rejected line: %v", err)
			}
			if segs := l.Segments(); len(segs) != 0 {
				t.Fatalf("image changed by rejected line: %+v", segs)
			}
		})
	}
}

func TestPrioritySyntaxBeforeChecksum(t *testing.T) {
	l := NewLoader()
	// Both malformed and bad checksum: syntax wins.
	wantReject(t, l, ":ZZ", ErrSyntax)
}

func TestPriorityChecksumBeforeUnknownType(t *testing.T) {
	l := NewLoader()
	// Type 0x09 is unknown, and the checksum is wrong: checksum wins.
	wantReject(t, l, ":0000000900", ErrChecksum)
}

func TestPriorityBoundaryBeforeOverlap(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0xFFF0, 0x00, make([]byte, 0x10)))
	// Crosses the boundary AND would overlap: boundary wins.
	wantReject(t, l, mkline(0xFFF8, 0x00, make([]byte, 0x10)), ErrBoundary)
}

func TestLineNumbersAndRejectedLinesCount(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0, 0x00, []byte{0x01}))    // line 1
	wantReject(t, l, ":bad", ErrSyntax)             // line 2 (rejected, still counts)
	err := l.AddLine(mkline(0, 0x00, []byte{0x01})) // line 3, overlaps line 1
	var le *LineError
	if !errors.As(err, &le) || le.Line != 3 {
		t.Fatalf("err=%v, want line number 3", err)
	}
	t.Logf("err=%v decision: rejected line 2 consumed a number", err)
}

func TestFinishRequiresEOF(t *testing.T) {
	l := NewLoader()
	mustAdd(t, l, mkline(0, 0x00, []byte{0x01}))
	if err := l.Finish(); !errors.Is(err, ErrNoEOF) {
		t.Fatalf("Finish err=%v, want ErrNoEOF", err)
	}
	mustAdd(t, l, mkline(0, 0x01, nil))
	if err := l.Finish(); err != nil {
		t.Fatalf("Finish after EOF: %v", err)
	}
}

func TestStartAddressRecords(t *testing.T) {
	l := NewLoader()
	if _, ok := l.StartAddress(); ok {
		t.Fatal("start address set before any 03/05 record")
	}
	mustAdd(t, l, mkline(0, 0x03, []byte{0x11, 0x22, 0x33, 0x44}))
	mustAdd(t, l, mkline(0, 0x05, []byte{0xAA, 0xBB, 0xCC, 0xDD}))
	got, ok := l.StartAddress()
	t.Logf("start=%08x ok=%v decision: later 05 overrides earlier 03", got, ok)
	if !ok || got != 0xAABBCCDD {
		t.Fatalf("start=%08x ok=%v, want 0xAABBCCDD", got, ok)
	}
	if segs := l.Segments(); len(segs) != 0 {
		t.Fatalf("03/05 records must not enter the image: %+v", segs)
	}
	mustAdd(t, l, mkline(0, 0x01, nil))
}

// naiveImage is a per-byte reference model used to cross-check the loader.
type naiveImage struct {
	data    map[uint32]byte
	base    uint32
	eof     bool
	written map[uint32]bool
}

func newNaive() *naiveImage {
	return &naiveImage{data: map[uint32]byte{}, written: map[uint32]bool{}}
}

func (n *naiveImage) add(line string) error {
	rec, err := parse(line)
	if err != nil {
		return err
	}
	switch rec.typ {
	case 0x00:
		if uint32(rec.addr)+uint32(len(rec.data)) > 0x10000 {
			return ErrBoundary
		}
		abs := n.base + uint32(rec.addr)
		for i := range rec.data {
			if n.written[abs+uint32(i)] {
				return ErrOverlap
			}
		}
	case 0x01:
		if len(rec.data) != 0 || rec.addr != 0 {
			return ErrBadField
		}
	case 0x02, 0x04:
		if len(rec.data) != 2 || rec.addr != 0 {
			return ErrBadField
		}
	case 0x03, 0x05:
		if len(rec.data) != 4 || rec.addr != 0 {
			return ErrBadField
		}
	default:
		return ErrUnknownType
	}
	if n.eof {
		return ErrAfterEOF
	}
	switch rec.typ {
	case 0x00:
		abs := n.base + uint32(rec.addr)
		for i, b := range rec.data {
			n.data[abs+uint32(i)] = b
			n.written[abs+uint32(i)] = true
		}
	case 0x01:
		n.eof = true
	case 0x02:
		n.base = uint32(rec.data[0])<<12 | uint32(rec.data[1])<<4
	case 0x04:
		n.base = uint32(rec.data[0])<<24 | uint32(rec.data[1])<<16
	}
	return nil
}

func (n *naiveImage) segments() []Segment {
	var addrs []uint32
	for a := range n.data {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	var out []Segment
	for _, a := range addrs {
		if len(out) > 0 && out[len(out)-1].Start+uint32(len(out[len(out)-1].Data)) == a {
			out[len(out)-1].Data = append(out[len(out)-1].Data, n.data[a])
			continue
		}
		out = append(out, Segment{Start: a, Data: []byte{n.data[a]}})
	}
	return out
}

func TestAgainstNaiveImage(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	l := NewLoader()
	n := newNaive()
	var lines []string
	// Random extended-address records interleaved with data records placed
	// in a sparse, non-overlapping grid.
	for i := 0; i < 60; i++ {
		switch rng.Intn(4) {
		case 0:
			lines = append(lines, mkline(0, 0x02, []byte{byte(rng.Intn(256)), byte(rng.Intn(256))}))
		case 1:
			lines = append(lines, mkline(0, 0x04, []byte{byte(rng.Intn(256)), byte(rng.Intn(256))}))
		default:
			addr := uint16(rng.Intn(0x1000) * 0x10)
			data := make([]byte, 1+rng.Intn(8))
			rng.Read(data)
			lines = append(lines, mkline(addr, 0x00, data))
		}
	}
	lines = append(lines, mkline(0, 0x01, nil))
	for i, line := range lines {
		errL := l.AddLine(line)
		errN := n.add(line)
		t.Logf("line=%d input=%q loader_err=%v naive_err=%v", i+1, line, errL, errN)
		if (errL == nil) != (errN == nil) {
			t.Fatalf("line %d %q: loader err %v, naive err %v", i+1, line, errL, errN)
		}
	}
	got, want := l.Segments(), n.segments()
	t.Logf("loader segments=%d naive segments=%d", len(got), len(want))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("image mismatch:\n got=%+v\nwant=%+v", got, want)
	}
	if err := l.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func TestReplayDeterministic(t *testing.T) {
	lines := []string{
		mkline(0, 0x04, []byte{0x00, 0x01}),
		mkline(0x0100, 0x00, []byte{0x01, 0x02}),
		":garbage",
		mkline(0, 0x02, []byte{0x00, 0x02}),
		mkline(0x0200, 0x00, []byte{0x03}),
		mkline(0, 0x01, nil),
		mkline(0, 0x00, []byte{0x09}),
	}
	run := func() ([]Segment, []error) {
		l := NewLoader()
		var errs []error
		for _, line := range lines {
			errs = append(errs, l.AddLine(line))
		}
		return l.Segments(), errs
	}
	segs1, errs1 := run()
	segs2, errs2 := run()
	if !reflect.DeepEqual(segs1, segs2) {
		t.Fatalf("replay image mismatch: %+v vs %+v", segs1, segs2)
	}
	for i := range errs1 {
		if (errs1[i] == nil) != (errs2[i] == nil) ||
			(errs1[i] != nil && errs1[i].Error() != errs2[i].Error()) {
			t.Fatalf("replay error mismatch at %d: %v vs %v", i, errs1[i], errs2[i])
		}
	}
	t.Logf("replayed %d lines, segments=%+v errors=%v", len(lines), segs1, errs1)
}

func TestConcurrentDisjointWrites(t *testing.T) {
	const workers = 8
	l := NewLoader()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Addresses are disjoint across workers, so any interleaving
			// yields the same final image.
			for i := 0; i < 50; i++ {
				addr := uint16((w*50 + i) * 4)
				if err := l.AddLine(mkline(addr, 0x00, []byte{byte(w), byte(i)})); err != nil {
					t.Errorf("worker %d line %d: %v", w, i, err)
				}
				_ = l.Segments() // concurrent queries must be safe
			}
		}(w)
	}
	wg.Wait()
	segs := l.Segments()
	total := 0
	for _, s := range segs {
		total += len(s.Data)
	}
	t.Logf("segments=%d total_bytes=%d decision: disjoint writes commute", len(segs), total)
	if total != workers*50*2 {
		t.Fatalf("total bytes=%d, want %d", total, workers*50*2)
	}
}
