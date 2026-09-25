package mtf

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// naiveEncode is the plain reference: linear scan, move to front.
func naiveEncode(data, alpha []byte) []byte {
	list := append([]byte(nil), alpha...)
	out := make([]byte, 0, len(data))
	for _, s := range data {
		idx := bytes.IndexByte(list, s)
		if idx < 0 {
			return nil
		}
		out = append(out, byte(idx))
		list = append([]byte{s}, append(list[:idx], list[idx+1:]...)...)
	}
	return out
}

// naiveDecode mirrors the rule with plain list operations.
func naiveDecode(indices, alpha []byte) []byte {
	list := append([]byte(nil), alpha...)
	out := make([]byte, 0, len(indices))
	for _, i := range indices {
		s := list[i]
		out = append(out, s)
		list = append([]byte{s}, append(list[:i], list[i+1:]...)...)
	}
	return out
}

func TestNewInvalidAlphabet(t *testing.T) {
	for _, alpha := range [][]byte{{}, {'a', 'b', 'a'}, {1, 1}} {
		if _, err := New(alpha); !errors.Is(err, ErrInvalidAlphabet) {
			t.Fatalf("alpha=%v: got %v want ErrInvalidAlphabet", alpha, err)
		}
	}
}

func TestEncodeSymbolMatchesNaive(t *testing.T) {
	alpha := []byte("abcdef")
	cases := [][]byte{
		[]byte("ababaadd"), []byte("aaaaaa"), []byte("fedcba"),
	}
	rng := rand.New(rand.NewSource(1)) // random cases generated in a loop
	for i := 0; i < 20; i++ {
		d := make([]byte, 64)
		for j := range d {
			d[j] = alpha[rng.Intn(len(alpha))]
		}
		cases = append(cases, d)
	}
	for n, data := range cases {
		want := naiveEncode(data, alpha)
		c, err := New(alpha)
		if err != nil {
			t.Fatal(err)
		}
		for k, s := range data {
			got, err := c.EncodeSymbol(s)
			if err != nil || got != int(want[k]) {
				t.Fatalf("case %d step %d: got (%d,%v) want %d", n, k, got, err, want[k])
			}
		}
	}
}

func TestDecodeSymbolMatchesNaive(t *testing.T) {
	alpha := []byte("abcdef")
	indices := []byte{0, 1, 1, 1, 1, 0, 3, 0}
	want := naiveDecode(indices, alpha)
	c, _ := New(alpha)
	for i, x := range indices {
		got, err := c.DecodeSymbol(int(x))
		if err != nil || got != want[i] {
			t.Fatalf("step %d: got (%q,%v) want %q", i, got, err, want[i])
		}
	}
}

func TestListIsPermutation(t *testing.T) {
	alpha := []byte("abcdef")
	c, _ := New(alpha)
	for _, s := range []byte("ababaaddfedcba") {
		if _, err := c.EncodeSymbol(s); err != nil {
			t.Fatal(err)
		}
		counts := map[byte]int{}
		for i, e := range c.c.list {
			counts[e]++
			if c.c.pos[e] != i {
				t.Fatalf("pos[%q]=%d but list index %d", e, c.c.pos[e], i)
			}
		}
		if len(counts) != len(alpha) {
			t.Fatalf("list not a permutation: %v", c.c.list)
		}
		for _, a := range alpha {
			if counts[a] != 1 {
				t.Fatalf("symbol %q count %d", a, counts[a])
			}
		}
	}
}

func TestStepFailureStateUnchanged(t *testing.T) {
	c, _ := New([]byte("abcdef"))
	before := append([]byte(nil), c.c.list...)
	if _, err := c.EncodeSymbol('z'); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("got %v want ErrUnknownSymbol", err)
	}
	if !bytes.Equal(c.c.list, before) {
		t.Fatalf("list changed after unknown symbol: %v", c.c.list)
	}
	if _, err := c.DecodeSymbol(6); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("got %v want ErrInvalidIndex", err)
	}
	if !bytes.Equal(c.c.list, before) {
		t.Fatalf("list changed after bad index: %v", c.c.list)
	}
	if idx, err := c.EncodeSymbol('d'); err != nil || idx != 3 { // still usable
		t.Fatalf("unusable after rejection: idx=%d err=%v", idx, err)
	}
}

// TestProbeCountConstantInM pins O(1) location independent of m; int symbols exceed byte's 256-name limit.
func TestProbeCountConstantInM(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		alpha := make([]int, m)
		for i := range alpha {
			alpha[i] = i
		}
		e, ok := newCore(alpha)
		if !ok {
			t.Fatalf("m=%d: alphabet rejected", m)
		}
		for _, s := range []int{m - 1, m / 2, 0, m - 1} {
			if _, err := e.encode(s); err != nil || e.probeCount > 1 {
				t.Fatalf("m=%d err=%v probes=%d, want <=1 O(1) lookup", m, err, e.probeCount)
			}
		}
	}
}
