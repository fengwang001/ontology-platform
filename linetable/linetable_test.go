package linetable

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// appendSeq applies (pc, line) pairs in order and fails on error.
func appendSeq(t *testing.T, tb *Table, pairs ...[2]int) {
	t.Helper()
	for _, p := range pairs {
		if err := tb.Append(p[0], p[1]); err != nil {
			t.Fatalf("Append(%d, %d): %v", p[0], p[1], err)
		}
	}
}

// naive builds a per-pc lookup: for each pc in [0, n), the line of the
// last entry with entry.pc <= pc; -1 when no entry qualifies.
func naive(n int, pairs ...[2]int) []int {
	out := make([]int, n)
	latest := -1
	hi := 0
	for _, p := range pairs {
		for ; hi < p[0]; hi++ {
			out[hi] = latest
		}
		latest = p[1]
	}
	for ; hi < n; hi++ {
		out[hi] = latest
	}
	return out
}

func TestNewInvalidSize(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		_, err := New(n)
		t.Logf("input: N=%d, output: err=%v, reason: N must be >= 1", n, err)
		if !errors.Is(err, ErrInvalidSize) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidSize", n, err)
		}
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1): %v", err)
	}
}

func TestAppendValidationOrder(t *testing.T) {
	tb, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	err = tb.Append(1, 10)
	t.Logf("input: Append(1, 10) on empty table, output: %v, reason: first pc must be 0", err)
	if !errors.Is(err, ErrFirstPCNotZero) {
		t.Fatalf("got %v, want ErrFirstPCNotZero", err)
	}
	err = tb.Append(4, 0)
	t.Logf("input: Append(4, 0), output: %v, reason: pc range checked before line sign", err)
	if !errors.Is(err, ErrPCOutOfRange) {
		t.Fatalf("got %v, want ErrPCOutOfRange", err)
	}
	err = tb.Append(0, 0)
	t.Logf("input: Append(0, 0), output: %v, reason: line sign checked before pc increase", err)
	if !errors.Is(err, ErrNonPositiveLine) {
		t.Fatalf("got %v, want ErrNonPositiveLine", err)
	}
	appendSeq(t, tb, [2]int{0, 10})
	err = tb.Append(0, 20)
	t.Logf("input: Append(0, 20) with lastPC=0, output: %v, reason: pc must strictly increase", err)
	if !errors.Is(err, ErrPCNotIncreasing) {
		t.Fatalf("got %v, want ErrPCNotIncreasing", err)
	}
	if got := tb.Encode(); len(got) != 2 {
		t.Fatalf("rejected append mutated table: Encode len = %d, want 2", len(got))
	}
}

func TestPCBoundary(t *testing.T) {
	const n = 5
	tb, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	appendSeq(t, tb, [2]int{0, 7})
	err = tb.Append(n-1, 9)
	t.Logf("input: Append(%d, 9) with N=%d, output: %v, reason: pc == N-1 is the last valid pc", n-1, n, err)
	if err != nil {
		t.Fatalf("Append(N-1): %v", err)
	}
	err = tb.Append(n, 11)
	t.Logf("input: Append(%d, 11) with N=%d, output: %v, reason: pc == N is out of range", n, n, err)
	if !errors.Is(err, ErrPCOutOfRange) {
		t.Fatalf("Append(N) got %v, want ErrPCOutOfRange", err)
	}
	err = tb.Append(-1, 11)
	t.Logf("input: Append(-1, 11), output: %v, reason: negative pc is out of range", err)
	if !errors.Is(err, ErrPCOutOfRange) {
		t.Fatalf("Append(-1) got %v, want ErrPCOutOfRange", err)
	}
	line, err := tb.Line(n - 1)
	t.Logf("input: Line(%d), output: line=%d err=%v, reason: query at pc == N-1 is valid", n-1, line, err)
	if err != nil || line != 9 {
		t.Fatalf("Line(N-1) = %d, %v; want 9, nil", line, err)
	}
	if _, err = tb.Line(n); !errors.Is(err, ErrPCOutOfRange) {
		t.Fatalf("Line(N) got %v, want ErrPCOutOfRange", err)
	}
}

func TestQueryEmptyTable(t *testing.T) {
	tb, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tb.Line(0)
	t.Logf("input: Line(0) on empty table, output: %v, reason: empty table checked before pc range", err)
	if !errors.Is(err, ErrEmptyTable) {
		t.Fatalf("got %v, want ErrEmptyTable", err)
	}
	if _, err = tb.Line(99); !errors.Is(err, ErrEmptyTable) {
		t.Fatalf("got %v, want ErrEmptyTable (empty checked first)", err)
	}
}

func TestMergeAdvancesLastPC(t *testing.T) {
	tb, err := New(10)
	if err != nil {
		t.Fatal(err)
	}
	appendSeq(t, tb, [2]int{0, 5})
	before := tb.Encode()
	if err := tb.Append(4, 5); err != nil {
		t.Fatalf("merge append: %v", err)
	}
	after := tb.Encode()
	t.Logf("input: Append(4, 5) after (0, 5), output: encode %v -> %v, reason: same line merges, no new entry", before, after)
	if !bytes.Equal(before, after) {
		t.Fatalf("merge changed encoding: %v -> %v", before, after)
	}
	err = tb.Append(4, 9)
	t.Logf("input: Append(4, 9) after merge, output: %v, reason: merge still advances last appended pc to 4", err)
	if !errors.Is(err, ErrPCNotIncreasing) {
		t.Fatalf("got %v, want ErrPCNotIncreasing", err)
	}
	if err := tb.Append(5, 9); err != nil {
		t.Fatalf("Append(5, 9): %v", err)
	}
	line, err := tb.Line(4)
	t.Logf("input: Line(4), output: line=%d err=%v, reason: only entry (0,5) covers pc 4", line, err)
	if err != nil || line != 5 {
		t.Fatalf("Line(4) = %d, %v; want 5, nil", line, err)
	}
}

func TestNegativeLineDelta(t *testing.T) {
	tb, err := New(10)
	if err != nil {
		t.Fatal(err)
	}
	appendSeq(t, tb, [2]int{0, 100}, [2]int{4, 30})
	got := tb.Encode()
	// (0,100): pc delta 0 -> 00, line delta +100 -> zigzag 200 -> C8 01
	// (4,30):  pc delta 4 -> 08, line delta -70 -> zigzag 139 -> 8B 01
	want := []byte{0x00, 0xC8, 0x01, 0x08, 0x8B, 0x01}
	t.Logf("input: entries (0,100),(4,30), output: %v, reason: line delta -70 zigzag-maps to 139", got)
	if !bytes.Equal(got, want) {
		t.Fatalf("Encode = %v, want %v", got, want)
	}
	line, err := tb.Line(6)
	t.Logf("input: Line(6), output: line=%d err=%v, reason: entry (4,30) is the last entry with pc <= 6", line, err)
	if err != nil || line != 30 {
		t.Fatalf("Line(6) = %d, %v; want 30, nil", line, err)
	}
}

func TestDeltaByteBoundaries(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		pairs [][2]int
		want  []byte
		why   string
	}{
		{"pc delta 63", 200, [][2]int{{0, 1}, {63, 2}},
			[]byte{0x00, 0x02, 0x7E, 0x02},
			"zigzag(63)=126 fits in one byte"},
		{"pc delta 64", 200, [][2]int{{0, 1}, {64, 2}},
			[]byte{0x00, 0x02, 0x80, 0x01, 0x02},
			"zigzag(64)=128 needs two bytes"},
		{"line delta -64", 10, [][2]int{{0, 100}, {1, 36}},
			[]byte{0x00, 0xC8, 0x01, 0x02, 0x7F},
			"zigzag(-64)=127 fits in one byte"},
		{"line delta -65", 10, [][2]int{{0, 100}, {1, 35}},
			[]byte{0x00, 0xC8, 0x01, 0x02, 0x81, 0x01},
			"zigzag(-65)=129 needs two bytes"},
		{"line delta +63", 10, [][2]int{{0, 1}, {1, 64}},
			[]byte{0x00, 0x02, 0x02, 0x7E},
			"zigzag(63)=126 fits in one byte"},
		{"line delta +64", 10, [][2]int{{0, 1}, {1, 65}},
			[]byte{0x00, 0x02, 0x02, 0x80, 0x01},
			"zigzag(64)=128 needs two bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb, err := New(tc.n)
			if err != nil {
				t.Fatal(err)
			}
			appendSeq(t, tb, tc.pairs...)
			got := tb.Encode()
			t.Logf("input: %v, output: %v, reason: %s", tc.pairs, got, tc.why)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("Encode = %v, want %v", got, tc.want)
			}
			dec, err := Decode(got, tc.n)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if re := dec.Encode(); !bytes.Equal(re, got) {
				t.Fatalf("re-encode = %v, want %v", re, got)
			}
		})
	}
}

func TestDecodeVarintErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
		why  string
	}{
		{"too long", bytes.Repeat([]byte{0x80}, 10), ErrVarintTooLong,
			"10 continuation bytes without terminator"},
		{"truncated", []byte{0x80}, ErrVarintTruncated,
			"stream ends right after a continuation bit"},
		{"truncated after valid entry", []byte{0x00, 0x0A, 0x80}, ErrVarintTruncated,
			"second entry's pc delta is cut off"},
		{"non-minimal", []byte{0x80, 0x00}, ErrVarintNonMinimal,
			"multi-byte varint ending in 0x00 is not minimal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := Decode(tc.data, 16)
			t.Logf("input: %v, output: err=%v, reason: %s", tc.data, err, tc.why)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Decode err = %v, want %v", err, tc.want)
			}
			if dec != nil {
				t.Fatalf("failed decode left a partial table: %+v", dec)
			}
		})
	}
}

func TestDecodeEntryErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		n    int
		want error
		why  string
	}{
		{"first pc delta non-zero", []byte{0x02, 0x0A}, 16, ErrFirstPCDeltaNonZero,
			"first entry is measured against pc 0, so its pc delta must be 0"},
		{"non-first pc delta zero", []byte{0x00, 0x0A, 0x00, 0x02}, 16, ErrPCDeltaNotPositive,
			"second entry repeats pc 0"},
		{"non-first pc delta negative", []byte{0x00, 0x0A, 0x01, 0x02}, 16, ErrPCDeltaNotPositive,
			"zigzag 1 decodes to pc delta -1"},
		{"cumulative pc reaches N", []byte{0x00, 0x0A, 0x04, 0x02}, 2, ErrPCBeyondN,
			"pc delta 2 lands on pc 2 with N=2"},
		{"non-first line delta zero", []byte{0x00, 0x0A, 0x02, 0x00}, 16, ErrLineDeltaZero,
			"a repeated line would have been merged, never encoded"},
		{"first line non-positive", []byte{0x00, 0x05}, 16, ErrLineNotPositive,
			"zigzag 5 decodes to line delta -3, cumulative line -3"},
		{"later line non-positive", []byte{0x00, 0x0A, 0x02, 0x13}, 16, ErrLineNotPositive,
			"line 5 plus delta -10 gives cumulative line -5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := Decode(tc.data, tc.n)
			t.Logf("input: %v (N=%d), output: err=%v, reason: %s", tc.data, tc.n, err, tc.why)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Decode err = %v, want %v", err, tc.want)
			}
			if dec != nil {
				t.Fatalf("failed decode left a partial table: %+v", dec)
			}
		})
	}
}

func TestDecodeErrorPrecedence(t *testing.T) {
	// First entry violates both "first pc delta" and "line positive";
	// the pc delta check is listed first and must win.
	_, err := Decode([]byte{0x02, 0x05}, 16)
	t.Logf("input: [02 05], output: %v, reason: pc delta check precedes line check", err)
	if !errors.Is(err, ErrFirstPCDeltaNonZero) {
		t.Fatalf("got %v, want ErrFirstPCDeltaNonZero", err)
	}
	// A varint error later in the stream is reported even though an
	// entry-level check on the following bytes would also fail.
	_, err = Decode([]byte{0x00, 0x0A, 0x80}, 16)
	t.Logf("input: [00 0A 80], output: %v, reason: byte-order scan hits truncation first", err)
	if !errors.Is(err, ErrVarintTruncated) {
		t.Fatalf("got %v, want ErrVarintTruncated", err)
	}
}

func TestRoundTrip(t *testing.T) {
	const n = 64
	pairs := [][2]int{{0, 3}, {2, 3}, {5, 40}, {9, 1}, {30, 200}, {63, 199}}
	tb, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	appendSeq(t, tb, pairs...)
	enc := tb.Encode()
	dec, err := Decode(enc, n)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	re := dec.Encode()
	t.Logf("input: %v, output: encode=%v re-encode=%v, reason: decode then encode must reproduce the bytes", pairs, enc, re)
	if !bytes.Equal(enc, re) {
		t.Fatalf("round trip: %v != %v", enc, re)
	}
	ref := naive(n, pairs...)
	for pc := 0; pc < n; pc++ {
		got, err := dec.Line(pc)
		if err != nil {
			t.Fatalf("Line(%d): %v", pc, err)
		}
		if got != ref[pc] {
			t.Fatalf("Line(%d) = %d, naive says %d", pc, got, ref[pc])
		}
	}
}

func TestAgainstNaive(t *testing.T) {
	const n = 300
	rng := rand.New(rand.NewSource(1))
	tb, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	var entries [][2]int
	pc, line := 0, 1
	for pc < n {
		if rng.Intn(5) == 0 && len(entries) > 0 {
			line = entries[len(entries)-1][1] // force a merge
		} else {
			line = 1 + rng.Intn(150)
		}
		if err := tb.Append(pc, line); err != nil {
			t.Fatalf("Append(%d, %d): %v", pc, line, err)
		}
		if len(entries) == 0 || entries[len(entries)-1][1] != line {
			entries = append(entries, [2]int{pc, line})
		}
		pc += 1 + rng.Intn(7)
	}
	ref := naive(n, entries...)
	bad := 0
	for q := 0; q < n; q++ {
		got, err := tb.Line(q)
		if err != nil {
			t.Fatalf("Line(%d): %v", q, err)
		}
		if got != ref[q] {
			bad++
		}
	}
	t.Logf("input: %d entries over N=%d, output: %d mismatches, reason: every pc must match the per-pc naive table", len(entries), n, bad)
	if bad != 0 {
		t.Fatalf("%d mismatches against naive table", bad)
	}
	// The decoded twin must answer identically.
	dec, err := Decode(tb.Encode(), n)
	if err != nil {
		t.Fatal(err)
	}
	for q := 0; q < n; q++ {
		got, _ := dec.Line(q)
		if got != ref[q] {
			t.Fatalf("decoded Line(%d) = %d, naive says %d", q, got, ref[q])
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	const n = 50
	pairs := [][2]int{{0, 8}, {1, 8}, {7, 2}, {20, 90}, {21, 3}, {49, 3}}
	build := func() *Table {
		tb, err := New(n)
		if err != nil {
			t.Fatal(err)
		}
		appendSeq(t, tb, pairs...)
		return tb
	}
	a, b := build(), build()
	ea, eb := a.Encode(), b.Encode()
	t.Logf("input: replay %v twice, output: %v vs %v, reason: identical append sequences give identical bytes", pairs, ea, eb)
	if !bytes.Equal(ea, eb) {
		t.Fatalf("replay mismatch: %v != %v", ea, eb)
	}
	for pc := 0; pc < n; pc++ {
		la, _ := a.Line(pc)
		lb, _ := b.Line(pc)
		if la != lb {
			t.Fatalf("Line(%d): %d != %d across replays", pc, la, lb)
		}
	}
}

func TestConcurrentFirstAppend(t *testing.T) {
	tb, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	const g = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tb.Append(0, 7); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("input: %d goroutines race Append(0, 7), output: %d successes, reason: calls behave as some serial order", g, wins)
	if wins != 1 {
		t.Fatalf("wins = %d, want exactly 1", wins)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	const n = 200
	tb, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				if line, err := tb.Line(rng.Intn(n)); err == nil && line <= 0 {
					t.Errorf("Line returned non-positive %d", line)
				}
				enc := tb.Encode()
				if _, err := Decode(enc, n); err != nil {
					t.Errorf("concurrent Encode does not decode: %v", err)
				}
			}
		}(int64(i))
	}
	for pc := 0; pc < n; pc++ {
		if err := tb.Append(pc, pc%50+1); err != nil {
			t.Fatalf("Append(%d): %v", pc, err)
		}
	}
	close(stop)
	wg.Wait()
	// Final state must equal a purely sequential run.
	ref := naive(n, func() [][2]int {
		var p [][2]int
		for pc := 0; pc < n; pc++ {
			p = append(p, [2]int{pc, pc%50 + 1})
		}
		return p
	}()...)
	for pc := 0; pc < n; pc++ {
		got, err := tb.Line(pc)
		if err != nil || got != ref[pc] {
			t.Fatalf("Line(%d) = %d, %v; want %d", pc, got, err, ref[pc])
		}
	}
}

func TestConcurrentEncodeStable(t *testing.T) {
	const n = 32
	tb, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	appendSeq(t, tb, [2]int{0, 4}, [2]int{3, 9}, [2]int{10, 9}, [2]int{31, 1})
	want := tb.Encode()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if got := tb.Encode(); !bytes.Equal(got, want) {
					t.Errorf("Encode = %v, want constant %v", got, want)
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("input: 8 goroutines x 100 Encode calls, output: all equal %v, reason: Encode is read-only and constant for a fixed state", want)
}
