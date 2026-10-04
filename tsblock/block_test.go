package tsblock

import (
	"bytes"
	"math"
	"sync"
	"testing"
)

func mustNew(t *testing.T, start int64, maxBytes int) *Block {
	t.Helper()
	b, err := New(start, maxBytes)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", start, maxBytes, err)
	}
	return b
}

func mustAppend(t *testing.T, b *Block, ts int64, v float64) {
	t.Helper()
	if err := b.Append(ts, v); err != nil {
		t.Fatalf("Append(%d, %v): %v", ts, v, err)
	}
}

func wantBits(t *testing.T, b *Block, want int) {
	t.Helper()
	if got := b.Bits(); got != want {
		t.Fatalf("Bits() = %d, want %d", got, want)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	b := mustNew(t, 1000, 64)
	mustAppend(t, b, 1010, 12.0)
	wantBits(t, b, 142) // 64 header + 14 d0 + 64 value
	mustAppend(t, b, 1020, 12.0)
	wantBits(t, b, 144) // dod=0 '0' + x=0 '0'
	mustAppend(t, b, 1030, 24.0)
	// dod=0 (1 bit); x=2^52, lz=11 tz=52 -> '11'+01011+000001+'1' (14 bits)
	wantBits(t, b, 159)
	mustAppend(t, b, 1031, 24.0)
	// dod=-9 -> '10'+1110111 (9 bits); x=0 (1 bit)
	wantBits(t, b, 169)
	mustAppend(t, b, 1032, 28.0)
	// dod=0 (1 bit); x=2^50: lz=13 ok but tz=50 < 52 -> reopen (14 bits)
	wantBits(t, b, 184)
	if err := b.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	wantBits(t, b, 220)
	if got := len(b.Bytes()); got != 28 {
		t.Fatalf("len(Bytes()) = %d, want 28", got)
	}
	if got := b.Len(); got != 5 {
		t.Fatalf("Len() = %d, want 5", got)
	}

	start, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if start != 1000 {
		t.Fatalf("start = %d, want 1000", start)
	}
	want := []Sample{{1010, 12.0}, {1020, 12.0}, {1030, 24.0}, {1031, 24.0}, {1032, 28.0}}
	if len(samples) != len(want) {
		t.Fatalf("decoded %d samples, want %d", len(samples), len(want))
	}
	for i, s := range samples {
		if s.T != want[i].T || math.Float64bits(s.V) != math.Float64bits(want[i].V) {
			t.Fatalf("sample %d = (%d, %x), want (%d, %x)",
				i, s.T, math.Float64bits(s.V), want[i].T, math.Float64bits(want[i].V))
		}
	}
}

// TestCapacityExample replays the capacity example from the specification.
func TestCapacityExample(t *testing.T) {
	b := mustNew(t, 1000, 24) // 192 bits
	mustAppend(t, b, 1010, 12.0)
	mustAppend(t, b, 1020, 12.0)
	wantBits(t, b, 144)

	snapBits := b.Bits()
	snapBytes := b.Bytes()
	snapLen := b.Len()

	// 159 + 36 = 195 > 192 -> ErrFull, state untouched.
	wantErr(t, b.Append(1030, 24.0), ErrFull)
	wantBits(t, b, snapBits)
	if !bytes.Equal(b.Bytes(), snapBytes) {
		t.Fatal("Bytes changed after rejected append")
	}
	if b.Len() != snapLen {
		t.Fatal("Len changed after rejected append")
	}

	// dod is still 0 after the rejection: this append fits.
	mustAppend(t, b, 1030, 12.0)
	wantBits(t, b, 146)
}

func TestNewParam(t *testing.T) {
	for _, mb := range []int{0, 1, 12, -5, 1048577, 1 << 30} {
		if _, err := New(0, mb); err != ErrParam {
			t.Fatalf("New(0, %d) err = %v, want ErrParam", mb, err)
		}
	}
	for _, mb := range []int{13, 14, 64, 1048576} {
		if _, err := New(0, mb); err != nil {
			t.Fatalf("New(0, %d) err = %v, want nil", mb, err)
		}
	}
}

// TestFirstDelta covers d0 boundaries: 0 and 15359 pass, 15360 and
// t < start fail with ErrDelta.
func TestFirstDelta(t *testing.T) {
	for _, d0 := range []int64{0, 1, 15359} {
		b := mustNew(t, 1000, 64)
		mustAppend(t, b, 1000+d0, 1.0)
		if b.Len() != 1 {
			t.Fatalf("d0=%d: Len = %d, want 1", d0, b.Len())
		}
	}
	for _, d0 := range []int64{15360, 15361, 1 << 40} {
		b := mustNew(t, 1000, 64)
		wantErr(t, b.Append(1000+d0, 1.0), ErrDelta)
		wantBits(t, b, 64)
	}
	// t < start is ErrDelta, not ErrOrder.
	b := mustNew(t, 1000, 64)
	wantErr(t, b.Append(999, 1.0), ErrDelta)
	// Overflow of t - start is also ErrDelta.
	b = mustNew(t, math.MinInt64, 64)
	wantErr(t, b.Append(0, 1.0), ErrDelta)
	wantBits(t, b, 64)
}

// dodSeq builds a block whose last sample has the given dod, and
// returns the block. The previous delta is ramped to 1<<31 so that any
// int32 dod keeps d >= 0. Layout: d0=0, d1=1<<30, d2=1<<31, then
// d3 = 1<<31 + dod.
func dodSeq(t *testing.T, dod int64) *Block {
	t.Helper()
	b := mustNew(t, 0, 1<<20)
	mustAppend(t, b, 0, 1.0)
	mustAppend(t, b, 1<<30, 1.0)
	mustAppend(t, b, (1<<30)+(1<<31), 1.0)
	mustAppend(t, b, (1<<30)+(1<<31)+(1<<31)+dod, 1.0)
	return b
}

// TestDodRanges checks both ends and one-past-each-end of every dod
// range, plus the int32 extremes of the 32-bit range.
func TestDodRanges(t *testing.T) {
	// Bits before the last sample: 64 + (14+64) + (36+1) + (36+1) = 216.
	const before = 216
	cases := []struct {
		dod  int64
		bits int
	}{
		{0, 1},
		{64, 9}, {65, 12},
		{-63, 9}, {-64, 12},
		{256, 12}, {257, 16},
		{-255, 12}, {-256, 16},
		{2048, 16}, {2049, 36},
		{-2047, 16}, {-2048, 36},
		{math.MaxInt32, 36}, {math.MinInt32, 36},
		{1, 9}, {-1, 9},
	}
	for _, c := range cases {
		b := dodSeq(t, c.dod)
		// +1 for the x=0 value bit.
		wantBits(t, b, before+c.bits+1)
		if err := b.Seal(); err != nil {
			t.Fatalf("dod=%d: Seal: %v", c.dod, err)
		}
		_, samples, err := Decode(b.Bytes())
		if err != nil {
			t.Fatalf("dod=%d: Decode: %v", c.dod, err)
		}
		wantT := int64((1<<30)+(1<<31)+(1<<31)) + c.dod
		if got := samples[3].T; got != wantT {
			t.Fatalf("dod=%d: decoded t = %d, want %d", c.dod, got, wantT)
		}
	}
}

// TestDodExactBits verifies the exact two's complement bit patterns of
// the 7-bit dod range for dod=64 and dod=-63.
func TestDodExactBits(t *testing.T) {
	var want bitStr
	want.w(1000, 64) // header
	want.w(10, 14)   // d0 = 10
	want.w(math.Float64bits(12.0), 64)
	want.w(0b10, 2) // dod = 54 -> '10' + 7 bits
	want.w(54, 7)   // 54 = 0110110
	want.w(0, 1)    // x = 0
	want.w(0b10, 2) // dod = -63 -> '10' + 1000001
	want.w(0b1000001, 7)
	want.w(0, 1) // x = 0

	b := mustNew(t, 1000, 64)
	mustAppend(t, b, 1010, 12.0) // d0 = 10
	mustAppend(t, b, 1074, 12.0) // d = 64, dod = 54
	mustAppend(t, b, 1075, 12.0) // d = 1, dod = -63
	if got := b.Bytes(); !bytes.Equal(got, want.bytes()) {
		t.Fatalf("Bytes = %x, want %x", got, want.bytes())
	}
	wantBits(t, b, len(want.b))

	// dod = 64 -> '10' + 1000000.
	var want2 bitStr
	want2.w(1000, 64)
	want2.w(10, 14)
	want2.w(math.Float64bits(12.0), 64)
	want2.w(0b10, 2)
	want2.w(0b1000000, 7) // 64 stays +64
	want2.w(0, 1)
	b2 := mustNew(t, 1000, 64)
	mustAppend(t, b2, 1010, 12.0)
	mustAppend(t, b2, 1084, 12.0) // d = 74, dod = 64
	if got := b2.Bytes(); !bytes.Equal(got, want2.bytes()) {
		t.Fatalf("Bytes = %x, want %x", got, want2.bytes())
	}
}

// TestDodOutOfRange checks dod values outside int32.
func TestDodOutOfRange(t *testing.T) {
	for _, dod := range []int64{math.MaxInt32 + 1, math.MinInt32 - 1, 1 << 40} {
		b := mustNew(t, 0, 1<<20)
		mustAppend(t, b, 0, 1.0)
		mustAppend(t, b, 1<<30, 1.0)
		// Ramp prevD to 1<<30 + MaxInt32 so that even
		// dod = MinInt32-1 keeps d >= 0 (isolating ErrDelta).
		mustAppend(t, b, (1<<30)+(1<<30)+math.MaxInt32, 1.0)
		snap := b.Bytes()
		prevT := int64((1 << 30) + (1 << 30) + math.MaxInt32)
		prevD := int64((1 << 30) + math.MaxInt32)
		wantErr(t, b.Append(prevT+prevD+dod, 1.0), ErrDelta)
		if !bytes.Equal(b.Bytes(), snap) {
			t.Fatalf("dod=%d: state changed after ErrDelta", dod)
		}
		if b.Len() != 3 {
			t.Fatalf("dod=%d: Len changed after ErrDelta", dod)
		}
	}
	// d itself overflowing int64 is ErrDelta.
	b := mustNew(t, -10, 1<<20)
	mustAppend(t, b, -10, 1.0)
	wantErr(t, b.Append(math.MaxInt64, 1.0), ErrDelta)
}

// TestOrderAndDuplicate checks ErrOrder and that equal timestamps are
// allowed.
func TestOrderAndDuplicate(t *testing.T) {
	b := mustNew(t, 0, 64)
	mustAppend(t, b, 0, 1.0)
	mustAppend(t, b, 10, 1.0)
	wantErr(t, b.Append(9, 1.0), ErrOrder)
	wantBits(t, b, 152) // 64 + 78 + (9 dod bits + 1 value bit)
	// Duplicates: d = 0, dod = -10 then 0.
	mustAppend(t, b, 10, 1.0)
	mustAppend(t, b, 10, 1.0)
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	_, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range samples {
		if want := []int64{0, 10, 10, 10}[i]; s.T != want {
			t.Fatalf("sample %d t = %d, want %d", i, s.T, want)
		}
	}
}

// xorSeq appends samples whose values are chosen so that the XOR with
// the previous value equals the given x values. It returns the block.
// The first sample value is 0.0 (bits 0); timestamps are 0,10,20,...
func xorSeq(t *testing.T, xs ...uint64) *Block {
	t.Helper()
	b := mustNew(t, 0, 1<<20)
	mustAppend(t, b, 0, math.Float64frombits(0))
	prev := uint64(0)
	for i, x := range xs {
		cur := prev ^ x
		mustAppend(t, b, int64(10*(i+1)), math.Float64frombits(cur))
		prev = cur
	}
	return b
}

// TestWindowReuseBoundary: reopen happens only when strictly shorter.
// With window (10,10) reuse costs 2+44=46 bits; (lz-pl)+(tz-pt)=11
// makes reopen also 46 -> reuse; 12 makes reopen 45 -> reopen.
func TestWindowReuseBoundary(t *testing.T) {
	x1 := uint64(1<<53) | (1 << 10) // lz=10, tz=10 -> window (10,10)
	x2 := uint64(1<<48) | (1 << 16) // lz=15, tz=16: diff 11 -> reuse (46)
	x3 := uint64(1<<47) | (1 << 16) // lz=16, tz=16: diff 12 -> reopen (45)
	b := xorSeq(t, x1, x2, x3)
	// 64 + 78 = 142; each dod: d=10, dod=10 then 0,0 -> 9+1+1 = 11
	// values: 13+44=57, 46, 45
	wantBits(t, b, 142+9+57+1+46+1+45)
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	_, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	prev := uint64(0)
	for i, x := range []uint64{x1, x2, x3} {
		prev ^= x
		if got := math.Float64bits(samples[i+1].V); got != prev {
			t.Fatalf("sample %d bits = %x, want %x", i+1, got, prev)
		}
	}
}

// TestWindowReuseNeedsBoth: reuse requires lz >= pl AND tz >= pt; if
// either fails the window is reopened even when reuse would be shorter.
func TestWindowReuseNeedsBoth(t *testing.T) {
	x1 := uint64(1<<47) | (1 << 16) // lz=16, tz=16 -> window (16,16)
	x2 := uint64(1<<43) | (1 << 10) // lz=20 ok, tz=10 < 16 -> reopen
	x3 := uint64(1<<53) | (1 << 20) // lz=10 < 16, tz=20 ok -> reopen
	b := xorSeq(t, x1, x2, x3)
	// values: 13+32=45, 13+(64-20-10)=47, 13+(64-10-20)=47
	wantBits(t, b, 142+9+45+1+47+1+47)
}

// TestWindowKeptOnZeroXor: x=0 does not touch the window.
func TestWindowKeptOnZeroXor(t *testing.T) {
	x1 := uint64(1<<53) | (1 << 10) // window (10,10)
	x2 := uint64(0)                 // same value: window must survive
	x3 := uint64(1<<50) | (1 << 12) // lz=13, tz=12: reuse 46 vs reopen 52
	b := xorSeq(t, x1, x2, x3)
	wantBits(t, b, 142+9+57+1+1+1+46)
}

// TestLeadingZerosCapped: lz is capped at 31.
func TestLeadingZerosCapped(t *testing.T) {
	b := xorSeq(t, 1) // x=1: true lz=63 -> stored 31, tz=0, siglen=33
	var want bitStr
	want.w(0, 64)
	want.w(0, 14)
	want.w(0, 64) // first value 0.0
	want.w(0b10, 2)
	want.w(10, 7) // dod = 10
	want.w(0b11, 2)
	want.w(31, 5) // lz capped at 31
	want.w(33, 6) // siglen = 64-31-0
	want.w(1, 33) // x >> 0 in 33 bits
	if got := b.Bytes(); !bytes.Equal(got, want.bytes()) {
		t.Fatalf("Bytes = %x, want %x", got, want.bytes())
	}
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	_, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := math.Float64bits(samples[1].V); got != 1 {
		t.Fatalf("decoded bits = %x, want 1", got)
	}
}

// TestSigLen64StoredAs0: a full-width XOR stores length 64 as 0.
func TestSigLen64StoredAs0(t *testing.T) {
	b := xorSeq(t, ^uint64(0)) // lz=0, tz=0, siglen=64
	var want bitStr
	want.w(0, 64)
	want.w(0, 14)
	want.w(0, 64)
	want.w(0b10, 2)
	want.w(10, 7)
	want.w(0b11, 2)
	want.w(0, 5) // lz = 0
	want.w(0, 6) // siglen 64 stored as 0
	want.w(^uint64(0), 64)
	if got := b.Bytes(); !bytes.Equal(got, want.bytes()) {
		t.Fatalf("Bytes = %x, want %x", got, want.bytes())
	}
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	_, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := math.Float64bits(samples[1].V); got != ^uint64(0) {
		t.Fatalf("decoded bits = %x, want %x", got, ^uint64(0))
	}
}

// TestDistinctBitPatterns: -0.0 vs +0.0 and distinct NaN patterns are
// preserved bit-exactly.
func TestDistinctBitPatterns(t *testing.T) {
	vals := []uint64{
		0x0000000000000000, // +0.0
		0x8000000000000000, // -0.0
		0x7FF8000000000000, // quiet NaN
		0x7FF8000000000001, // NaN with payload
		0xFFF8000000000000, // negative NaN
		0x7FF0000000000000, // +Inf
		0xFFF0000000000000, // -Inf
	}
	b := mustNew(t, 0, 1<<20)
	for i, vb := range vals {
		mustAppend(t, b, int64(i), math.Float64frombits(vb))
	}
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	_, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for i, vb := range vals {
		if got := math.Float64bits(samples[i].V); got != vb {
			t.Fatalf("sample %d bits = %x, want %x", i, got, vb)
		}
	}
}

// TestCapacityExactFit: an append that lands exactly on the capacity
// (bits + 36 == maxBytes*8) passes; one sample more is rejected.
func TestCapacityExactFit(t *testing.T) {
	// Four identical samples: 64+78+2+2+2 = 148 bits; 148+36 = 184 = 23*8.
	b := mustNew(t, 1000, 23)
	for i := 1; i <= 4; i++ {
		mustAppend(t, b, int64(1000+10*i), 12.0)
	}
	wantBits(t, b, 148)
	wantErr(t, b.Append(1050, 12.0), ErrFull) // 150+36 = 186 > 184
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	wantBits(t, b, 184)
	if got := len(b.Bytes()); got != 23 {
		t.Fatalf("len(Bytes()) = %d, want 23", got)
	}
	// One byte less: even the first sample does not fit (142+36=178 > 176).
	b2 := mustNew(t, 1000, 22)
	wantErr(t, b2.Append(1010, 12.0), ErrFull)
	if b2.Len() != 0 {
		t.Fatal("Len changed after ErrFull")
	}
	// 23 bytes fits the first sample exactly (178 <= 184, but not 2 more).
	b3 := mustNew(t, 1000, 23)
	mustAppend(t, b3, 1010, 12.0)
	wantBits(t, b3, 142)
}

// TestZeroSampleSeal: sealing an empty block is valid.
func TestZeroSampleSeal(t *testing.T) {
	b := mustNew(t, 5, 13)
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	wantBits(t, b, 100) // 64 header + 36 marker
	if got := len(b.Bytes()); got != 13 {
		t.Fatalf("len(Bytes()) = %d, want 13", got)
	}
	start, samples, err := Decode(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if start != 5 || len(samples) != 0 {
		t.Fatalf("Decode = (%d, %d samples), want (5, 0)", start, len(samples))
	}
	// Exact bit pattern: header then '1111' + 32 zeros.
	var want bitStr
	want.w(5, 64)
	want.w(0b1111, 4)
	want.w(0, 32)
	if got := b.Bytes(); !bytes.Equal(got, want.bytes()) {
		t.Fatalf("Bytes = %x, want %x", got, want.bytes())
	}
}

// TestSealedErrors: after Seal everything reports ErrSealed.
func TestSealedErrors(t *testing.T) {
	b := mustNew(t, 0, 64)
	mustAppend(t, b, 0, 1.0)
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	wantErr(t, b.Seal(), ErrSealed)
	wantErr(t, b.Append(10, 1.0), ErrSealed)
	// ErrSealed wins over ErrOrder/ErrDelta/ErrFull.
	wantErr(t, b.Append(-1, 1.0), ErrSealed)
}

// TestErrorOrder: ErrOrder beats ErrDelta beats ErrFull.
func TestErrorOrder(t *testing.T) {
	// 148 bits used; any further append exceeds 23*8 = 184 with the
	// seal reservation (148+2+36 = 186 > 184).
	b := mustNew(t, 0, 23)
	for i := 0; i < 4; i++ {
		mustAppend(t, b, 0, 1.0)
	}
	wantBits(t, b, 148)
	// t going backwards AND capacity exceeded -> ErrOrder.
	wantErr(t, b.Append(-1, 1.0), ErrOrder)
	// dod out of int32 AND capacity exceeded -> ErrDelta.
	wantErr(t, b.Append(math.MaxInt64, 1.0), ErrDelta)
	// Valid append, capacity exceeded -> ErrFull.
	wantErr(t, b.Append(0, 2.0), ErrFull)
	wantBits(t, b, 148)
}

// TestRejectedAppendKeepsStream: after any rejection the stream is
// identical to one where the attempt never happened.
func TestRejectedAppendKeepsStream(t *testing.T) {
	build := func(rejected func(b *Block)) *Block {
		b := mustNew(t, 1000, 24)
		mustAppend(t, b, 1010, 12.0)
		mustAppend(t, b, 1020, 12.0)
		if rejected != nil {
			rejected(b)
		}
		mustAppend(t, b, 1030, 12.0)
		if err := b.Seal(); err != nil {
			t.Fatal(err)
		}
		return b
	}
	clean := build(nil)
	attempts := []func(b *Block){
		func(b *Block) { b.Append(1030, 24.0) }, // ErrFull
		func(b *Block) { b.Append(1005, 1.0) },  // ErrOrder
		func(b *Block) { b.Append(1<<50, 1.0) }, // ErrDelta
	}
	for i, attempt := range attempts {
		got := build(attempt)
		if !bytes.Equal(got.Bytes(), clean.Bytes()) || got.Bits() != clean.Bits() || got.Len() != clean.Len() {
			t.Fatalf("attempt %d changed the stream", i)
		}
	}
}

// TestDecodeTruncation: every strict bit-prefix of a sealed stream must
// be rejected, as must nonzero padding and trailing garbage.
func TestDecodeTruncation(t *testing.T) {
	b := mustNew(t, 1000, 4096)
	vals := []float64{12.0, 12.0, 24.0, 28.0, -0.0, 1e300, 1e-300}
	for i, v := range vals {
		mustAppend(t, b, int64(1000+7*i*i), v)
	}
	// Force a 32-bit dod sample.
	mustAppend(t, b, 1000+7*6*6+(1<<20), 3.14)
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	full := b.Bytes()
	totalBits := b.Bits()

	for k := 0; k < totalBits; k++ {
		trunc := make([]byte, (k+7)/8)
		copy(trunc, full)
		for bit := k; bit < len(trunc)*8; bit++ {
			trunc[bit/8] &^= 1 << (7 - uint(bit%8))
		}
		// Decode operates on whole bytes: if the truncated bits were
		// all padding zeros the byte string is identical to the full
		// stream and must still decode.
		if bytes.Equal(trunc, full) {
			continue
		}
		if _, _, err := Decode(trunc); err != ErrCorrupt {
			t.Fatalf("truncation at %d bits: err = %v, want ErrCorrupt", k, err)
		}
	}
	if _, _, err := Decode(full); err != nil {
		t.Fatalf("full stream: %v", err)
	}
	// Nonzero padding bit after the marker.
	if totalBits%8 != 0 {
		bad := append([]byte(nil), full...)
		bad[len(bad)-1] |= 1 << (7 - uint(totalBits%8))
		if _, _, err := Decode(bad); err != ErrCorrupt {
			t.Fatalf("nonzero padding: err = %v, want ErrCorrupt", err)
		}
	}
	// A whole extra byte after the marker.
	bad := append(append([]byte(nil), full...), 0)
	if _, _, err := Decode(bad); err != ErrCorrupt {
		t.Fatalf("extra byte: err = %v, want ErrCorrupt", err)
	}
	// Empty and short inputs.
	for _, in := range [][]byte{nil, {}, {0}, make([]byte, 7)} {
		if _, _, err := Decode(in); err != ErrCorrupt {
			t.Fatalf("input %x: err = %v, want ErrCorrupt", in, err)
		}
	}
}

// TestDecodeCorruptStreams: hand-crafted invalid streams.
func TestDecodeCorruptStreams(t *testing.T) {
	// '10' value control with no window established.
	var s bitStr
	s.w(0, 64)
	s.w(0, 14)
	s.w(0, 64)
	s.w(0, 1)    // dod = 0
	s.w(0b10, 2) // reuse without a window
	s.w(0, 54)
	s.w(0b1111, 4)
	s.w(0, 32)
	if _, _, err := Decode(s.bytes()); err != ErrCorrupt {
		t.Fatalf("reuse-without-window: err = %v, want ErrCorrupt", err)
	}
	// Marker with nonzero bits in the 32-bit field is a valid dod of 0?
	// No: '1111' + nonzero is a 32-bit dod; '1111' + zero mid-stream is
	// the marker, so a stream ending right after a nonzero field but
	// without a real marker must fail.
	var s2 bitStr
	s2.w(0, 64)
	s2.w(0b1111, 4)
	s2.w(1, 32) // not a marker, and no first sample either
	if _, _, err := Decode(s2.bytes()); err != ErrCorrupt {
		t.Fatalf("bad marker field: err = %v, want ErrCorrupt", err)
	}
	// lz/siglen combination with tz < 0.
	var s3 bitStr
	s3.w(0, 64)
	s3.w(0, 14)
	s3.w(0, 64)
	s3.w(0, 1)    // dod = 0
	s3.w(0b11, 2) // reopen
	s3.w(31, 5)   // lz = 31
	s3.w(0, 6)    // siglen = 64 -> tz = -31
	s3.w(0, 64)
	if _, _, err := Decode(s3.bytes()); err != ErrCorrupt {
		t.Fatalf("negative tz: err = %v, want ErrCorrupt", err)
	}
}

// TestConcurrent: all methods are safe for concurrent use; with
// identical appends every interleaving yields the same stream.
func TestConcurrent(t *testing.T) {
	b := mustNew(t, 0, 4096)
	const goroutines = 8
	const perG = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				_ = b.Append(0, 1.0)
				_ = b.Bits()
				_ = b.Bytes()
				_ = b.Len()
			}
		}()
	}
	wg.Wait()
	if got := b.Len(); got != goroutines*perG {
		t.Fatalf("Len = %d, want %d", got, goroutines*perG)
	}
	// First sample: 64+14+64; the rest: dod=0 + x=0 = 2 bits each.
	wantBits(t, b, 142+2*(goroutines*perG-1))
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// TestReplay: the same operation sequence reproduces the same stream.
func TestReplay(t *testing.T) {
	run := func() []byte {
		b := mustNew(t, 42, 4096)
		vals := []float64{1.5, -2.75, 1.5, 0, -0.0, 1e-9, 1e9}
		for i, v := range vals {
			mustAppend(t, b, int64(42+i*13), v)
		}
		if err := b.Seal(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	if a, c := run(), run(); !bytes.Equal(a, c) {
		t.Fatalf("replay mismatch: %x vs %x", a, c)
	}
}
