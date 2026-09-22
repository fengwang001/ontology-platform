package ontology

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func compareRowsDesc(desc []bool, a, b []any) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		c := Compare(a[i], b[i])
		if i < len(desc) && desc[i] && !isNilKey(a[i]) && !isNilKey(b[i]) {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return sign(len(a) - len(b))
}

func randKey(r *rand.Rand, allowNaN bool) any {
	switch r.Intn(12) {
	case 0:
		return nil
	case 1:
		if allowNaN {
			return math.NaN()
		}
		return int64(r.Intn(256) - 128)
	case 2, 3, 4: // small and large ints, both signs and zero
		return int64(r.Intn(257) - 128)
	case 5:
		return int64(r.Uint64())
	case 6:
		return []int64{math.MinInt64, math.MaxInt64, -1, 0, 1}[r.Intn(5)]
	case 7, 8: // floats: negatives, zero, positives
		return float64(r.Intn(2001)-1000) / 8
	case 9:
		return []float64{0, -1, 1, 0.5, -0.5, math.Inf(1), math.Inf(-1),
			math.SmallestNonzeroFloat64, math.MaxFloat64}[r.Intn(9)]
	case 10: // random finite float64 (exponent clamped away from Inf/NaN)
		b := r.Uint64() &^ (uint64(1) << 63)
		if b>>52 == 0x7FF {
			b &^= 1 << 52
		}
		return math.Float64frombits(b) * []float64{1, -1}[r.Intn(2)]
	default: // strings, possibly with 0x00 / 0xFF bytes
		n := r.Intn(9)
		bs := make([]byte, n)
		for i := range bs {
			switch r.Intn(6) {
			case 0:
				bs[i] = 0x00
			case 1:
				bs[i] = 0xFF
			default:
				bs[i] = byte('a' + r.Intn(4))
			}
		}
		return string(bs)
	}
}

func randRow(r *rand.Rand, n int, allowNaN bool) []any {
	row := make([]any, n)
	for i := range row {
		row[i] = randKey(r, allowNaN)
	}
	return row
}

func TestOrderDifferentialRandom(t *testing.T) {
	r := rand.New(rand.NewSource(20260922))
	for iter := 0; iter < 40000; iter++ {
		n := 1 + r.Intn(4)
		desc := make([]bool, n)
		for i := range desc {
			desc[i] = r.Intn(2) == 0
		}
		a := randRow(r, n, true)
		b := randRow(r, n, true)
		if r.Intn(3) == 0 { // force shared prefixes / adjacent keys
			copy(b, a)
			if r.Intn(2) == 0 {
				b[n-1] = a[n-1]
			}
		}
		enc := NewEncoder(desc)
		got := sign(bytes.Compare(enc.Encode(a), enc.Encode(b)))
		want := compareRowsDesc(desc, a, b)
		if got != want {
			t.Fatalf("iter %d: keys %v vs %v desc=%v: bytes.Compare=%d, want %d",
				iter, a, b, desc, got, want)
		}
	}
}

func TestOrderCrossTypeSweep(t *testing.T) {
	nums := []float64{-1e300, -5, -3.5, -3, -1, -0.5, 0, 0.5, 1, 3, 3.5, 5, 1e300}
	var keys []any
	for _, f := range nums {
		keys = append(keys, f)
	}
	for _, i := range []int64{math.MinInt64, -5, -3, -1, 0, 1, 3, 5, math.MaxInt64} {
		keys = append(keys, i)
	}
	keys = append(keys, nil, math.NaN(), "", "a", "ab", "\x00", "\xff")
	for _, dir := range []bool{false, true} {
		enc := NewEncoder([]bool{dir})
		for _, x := range keys {
			for _, y := range keys {
				got := sign(bytes.Compare(enc.Encode([]any{x}), enc.Encode([]any{y})))
				want := Compare(x, y)
				if dir && !isNilKey(x) && !isNilKey(y) {
					want = -want
				}
				if isNilKey(x) && isNilKey(y) {
					want = 0 // nil and NaN share one encoding
				}
				if got != want {
					t.Fatalf("dir=%v %v vs %v: got %d want %d", dir, x, y, got, want)
				}
			}
		}
	}
}

func TestStringPrefixNoAmbiguity(t *testing.T) {
	enc := NewEncoder(nil)
	if bytes.Compare(enc.Encode([]any{"a"}), enc.Encode([]any{"ab"})) >= 0 {
		t.Fatal(`"a" must sort before "ab"`)
	}
	// "a" followed by another key must not blend into "ab".
	got := bytes.Compare(enc.Encode([]any{"a", "zzz"}), enc.Encode([]any{"ab"}))
	if sign(got) != CompareRows([]any{"a", "zzz"}, []any{"ab"}) {
		t.Fatal("prefix key leaked into the next key")
	}
	row := []any{"a", "ab", "a", ""}
	back, err := Decode(nil, enc.Encode(row))
	if err != nil {
		t.Fatal(err)
	}
	for i := range row {
		if back[i] != row[i] {
			t.Fatalf("key %d: got %v want %v", i, back[i], row[i])
		}
	}
}

func TestNilPositionDirectionOrthogonal(t *testing.T) {
	others := []any{int64(math.MinInt64), math.Inf(-1), "", "\xff\xff", float64(0)}
	for _, dir := range []bool{false, true} {
		enc := NewEncoder([]bool{dir})
		nilEnc := enc.Encode([]any{nil})
		if !bytes.Equal(nilEnc, []byte{tagNil}) {
			t.Fatalf("dir=%v: nil encoding moved", dir)
		}
		for _, o := range others {
			if bytes.Compare(nilEnc, enc.Encode([]any{o})) >= 0 {
				t.Fatalf("dir=%v: nil must sort before %v", dir, o)
			}
		}
	}
}

func TestNaNCountedAndTreatedAsNil(t *testing.T) {
	enc := NewEncoder(nil)
	row := []any{math.NaN(), int64(1), math.NaN()}
	got := enc.Encode(row)
	want := NewEncoder(nil).Encode([]any{nil, int64(1), nil})
	if !bytes.Equal(got, want) {
		t.Fatal("NaN must encode exactly like nil")
	}
	enc.Encode([]any{math.NaN()})
	if enc.NaNCount() != 3 {
		t.Fatalf("NaNCount = %d, want 3", enc.NaNCount())
	}
	back, err := Decode(nil, got)
	if err != nil || back[0] != nil || back[2] != nil {
		t.Fatalf("NaN must decode as nil: %v %v", back, err)
	}
}
