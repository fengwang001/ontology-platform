package gstream

import (
	"fmt"
	"math/rand"
	"testing"
)

func naiveParams(M int) (m int64, b int, lim int64) {
	m = int64(M)
	for int64(1)<<uint(b) < m {
		b++
	}
	return m, b, int64(1)<<uint(b) - m
}

// naiveEncode independently hand-computes q/r/unary/truncated binary, MSB packs.
func naiveEncode(M int, vs []int64) []byte {
	m, b, lim := naiveParams(M)
	var bits []bool
	put := func(v int64, w int) {
		for i := w - 1; i >= 0; i-- {
			bits = append(bits, v&(int64(1)<<uint(i)) != 0)
		}
	}
	for _, n := range vs {
		q, r := n/m, n%m
		for i := int64(0); i <= q; i++ { // q ones then a zero
			bits = append(bits, i < q)
		}
		if r < lim {
			put(r, b-1)
		} else {
			put(r+lim, b)
		}
	}
	out := make([]byte, (len(bits)+7)/8)
	for i, bit := range bits {
		if bit {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}

// naiveDecode independently decodes bit by bit.
func naiveDecode(M int, p []byte) []int64 {
	m, b, lim := naiveParams(M)
	at := func(i int) int64 { return int64(p[i/8]>>(7-uint(i%8))) & 1 }
	var out []int64
	pos, total := 0, 8*len(p)
	for pos < total {
		if total-pos < 8 { // padding: <8 bits left and all zero
			i := pos
			for ; i < total && at(i) == 0; i++ {
			}
			if i == total {
				break
			}
		}
		var q int64
		for ; at(pos) == 1; pos++ {
			q++
		}
		pos++
		var v int64
		for i := 0; b > 0 && i < b-1; i++ {
			v, pos = v<<1|at(pos), pos+1
		}
		if b > 0 && v >= lim {
			v, pos = (v<<1|at(pos))-lim, pos+1
		}
		out = append(out, q*m+v)
	}
	return out
}
func eq(a, b []int64) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// TestNaiveReference pins invariant 1: output matches the naive references.
func TestNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, M := range []int{1, 2, 3, 5, 8, 16, 100} {
		for size := 0; size <= 40; size++ {
			vs := make([]int64, size)
			for i := range vs {
				vs[i] = int64(rng.Intn(300))
			}
			if size > 0 { // trailing 0 word is ambiguous with padding
				vs[size-1] += 1 + int64(rng.Intn(10))
			}
			enc, err := Encode(M, vs)
			ref := naiveEncode(M, vs)
			dec, derr := Decode(M, enc)
			if err != nil || fmt.Sprintf("%x", enc) != fmt.Sprintf("%x", ref) ||
				derr != nil || !eq(dec, naiveDecode(M, enc)) || !eq(dec, vs) {
				t.Fatalf("M=%d %v: enc %x ref %x dec %v errs %v %v", M, vs, enc, ref, dec, err, derr)
			}
		}
	}
}

// TestChunkedFeed pins invariant 2: any chunking of Feed equals one-shot Decode.
func TestChunkedFeed(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, M := range []int{1, 5, 8} {
		vs := make([]int64, 50)
		for i := range vs {
			vs[i] = int64(rng.Intn(1000)) + 1
		}
		enc, _ := Encode(M, vs)
		want, _ := Decode(M, enc)
		for size := 1; size <= len(enc)+1; size++ {
			d, _ := NewDecoder(M)
			for i := 0; i < len(enc); i += size {
				d.Feed(enc[i:min(i+size, len(enc))])
			}
			if got, err := d.Decode(); err != nil || !eq(got, want) {
				t.Fatalf("M=%d chunk=%d: %v %v", M, size, got, err)
			}
		}
	}
}

// TestBitsIndependentOfPrefix pins the complexity constraint: bits for the
// final large N do not grow with the number of preceding zeros.
func TestBitsIndependentOfPrefix(t *testing.T) {
	const M = 5
	bad, _ := NewDecoder(M)
	bad.Feed([]byte{0xFF}) // invariant 4: failed Decode leaves counter untouched
	if out, err := bad.Decode(); err != ErrTruncated || out != nil || bad.lastBits != 0 {
		t.Fatalf("failed decode left state: out=%v err=%v bits=%d", out, err, bad.lastBits)
	}
	prev := -1
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		vs := make([]int64, m+1)
		vs[m] = 1 << 20
		enc, _ := Encode(M, vs)
		d, _ := NewDecoder(M)
		d.Feed(enc)
		if _, err := d.Decode(); err != nil {
			t.Fatal(err)
		}
		need := int(int64(1<<20)/int64(M)) + 1 + d.codec.B() // q+1 unary + remainder
		if d.lastBits > need || (prev >= 0 && d.lastBits != prev) {
			t.Fatalf("m=%d: bits %d, prev %d; must not grow with m", m, d.lastBits, prev)
		}
		prev = d.lastBits
	}
}
