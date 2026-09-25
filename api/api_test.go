package api

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// naiveEncode is the deliberately unoptimized reference, written straight
// from the defining formulas.
func naiveEncode(ns []int) []byte {
	var bits []int
	for _, n := range ns {
		d1, d2, d3, d4 := n>>3&1, n>>2&1, n>>1&1, n&1
		bits = append(bits, d1^d2^d4, d1^d3^d4, d1, d2^d3^d4, d2, d3, d4)
	}
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		out[i/8] |= byte(b) << (7 - uint(i%8))
	}
	return out
}

// naiveCorrect decodes one 7-bit codeword (positions 1..7) per the spec.
func naiveCorrect(cw [7]int) int {
	s1 := cw[0] ^ cw[2] ^ cw[4] ^ cw[6]
	s2 := cw[1] ^ cw[2] ^ cw[5] ^ cw[6]
	s3 := cw[3] ^ cw[4] ^ cw[5] ^ cw[6]
	if syn := s1 + 2*s2 + 4*s3; syn != 0 {
		cw[syn-1] ^= 1
	}
	return cw[2]*8 + cw[4]*4 + cw[5]*2 + cw[6]
}

var seqs = [][]int{{}, {11}, {0, 1, 2, 3}, {15, 0, 8, 7, 11, 5, 9, 14, 3}, {10, 10, 10, 10, 10}}

func TestAgainstNaiveReference(t *testing.T) {
	for _, ns := range seqs {
		if got := Encode(ns); !bytes.Equal(got, naiveEncode(ns)) {
			t.Errorf("Encode(%v) = %x, naive = %x", ns, got, naiveEncode(ns))
		}
	}
	for n := 0; n < 16; n++ { // every nibble x every single-bit error
		cw := naiveEncode([]int{n})
		for pos := 0; pos < 7; pos++ {
			bad := cw[0] ^ 1<<(7-uint(pos))
			got, err := Decode([]byte{bad}, 1)
			var bits [7]int
			for i := 0; i < 7; i++ {
				bits[i] = int(bad >> (7 - uint(i)) & 1)
			}
			if want := naiveCorrect(bits); err != nil || got[0] != want || want != n {
				t.Errorf("n=%d pos=%d: got %v,%v want %d", n, pos, got, err, want)
			}
		}
	}
}

func TestChunkingInvariance(t *testing.T) {
	for _, ns := range seqs {
		enc := Encode(ns)
		want, _ := Decode(enc, len(ns))
		for cut := 0; cut <= len(enc); cut++ { // every 2-chunk split
			f := &Feeder{}
			f.Feed(enc[:cut])
			f.Feed(enc[cut:])
			if got, _ := f.Decode(len(ns)); !reflect.DeepEqual(got, want) {
				t.Errorf("%v cut=%d: %v != %v", ns, cut, got, want)
			}
		}
		f := &Feeder{} // byte-at-a-time
		for _, b := range enc {
			f.Feed([]byte{b})
		}
		if got, _ := f.Decode(len(ns)); !reflect.DeepEqual(got, want) {
			t.Errorf("%v bytewise: %v != %v", ns, got, want)
		}
	}
}

func TestDeterministicAndSingleError(t *testing.T) {
	for _, ns := range seqs {
		if a, b := Encode(ns), Encode(ns); !bytes.Equal(a, b) {
			t.Fatalf("Encode(%v) not deterministic", ns)
		}
		enc := Encode(ns)
		for bit := 0; bit < 7*len(ns); bit++ { // all single-bit errors
			c := bytes.Clone(enc)
			c[bit/8] ^= 1 << (7 - uint(bit%8))
			if got, err := Decode(c, len(ns)); err != nil || !reflect.DeepEqual(got, ns) {
				t.Errorf("%v bit=%d: got %v,%v", ns, bit, got, err)
			}
		}
	}
}

func TestFailureAtomicity(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		n    int
		want error
	}{
		{"truncated", []byte{0x66}, 2, ErrTruncated},
		{"illegal-padding", []byte{0x67}, 1, ErrIllegalPadding},
		{"illegal-padding-far", []byte{0x66, 0x80}, 1, ErrIllegalPadding},
	}
	for _, c := range cases {
		if got, err := Decode(c.b, c.n); got != nil || !errors.Is(err, c.want) {
			t.Errorf("%s: got %v,%v want nil,%v", c.name, got, err, c.want)
		}
	}
	for _, bad := range [][]int{{16}, {-1}, {3, 100, 3}} {
		if Encode(bad) != nil || !errors.Is(CheckNibbles(bad), ErrInvalidNibble) {
			t.Errorf("nibble %v not rejected", bad)
		}
	}
	if errors.Is(ErrTruncated, ErrIllegalPadding) || errors.Is(ErrInvalidNibble, ErrTruncated) {
		t.Error("sentinel errors not distinguishable")
	}
	if got, err := Decode([]byte{0x66}, 1); err != nil || !reflect.DeepEqual(got, []int{11}) {
		t.Error("decoder unusable after rejection")
	}
}

func TestConcurrentDecode(t *testing.T) {
	ns := []int{11, 5, 9, 14, 3, 0, 8, 7}
	enc := Encode(ns)
	enc[2] ^= 0x10 // inject one single-bit error
	want, _ := Decode(enc, len(ns))
	var wg sync.WaitGroup
	res := make([][]int, 64)
	for g := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _ := Decode(enc, len(ns))
			res[g] = got
		}()
	}
	wg.Wait()
	for g := range res {
		if !reflect.DeepEqual(res[g], want) {
			t.Fatalf("goroutine %d: %v != %v", g, res[g], want)
		}
	}
}
