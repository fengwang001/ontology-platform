package codec

import (
	"bytes"
	"errors"
	"testing"

	"ontology/b64"
)

// sizes spans 100..10000 per section 4.
var sizes = []int{100, 250, 1000, 5000, 10000}

func makeBuffer(n int) []byte {
	raw := make([]byte, 3*n)
	for i := range raw {
		raw[i] = byte(i*5 + 1)
	}
	buf := b64.Encode(raw)
	if len(buf) != 4*n {
		panic("bad fixture length")
	}
	return buf
}

// TestBlockLocationConstantRead proves O(1) addressing: locating either the
// last block (n-1) or an early block (1) reads exactly 4 bytes regardless of
// n. lastBlockRead is read here only because the test is in package codec;
// no exported function or method exposes it.
func TestBlockLocationConstantRead(t *testing.T) {
	for _, n := range sizes {
		buf := makeBuffer(n)
		for _, k := range []int{n - 1, 1} {
			var idx blockIndex
			if _, err := idx.at(buf, k); err != nil {
				t.Fatalf("n=%d k=%d: %v", n, k, err)
			}
			if idx.lastBlockRead != 4 {
				t.Fatalf("n=%d k=%d: read %d bytes, want 4 (O(1) direct offset)",
					n, k, idx.lastBlockRead)
			}
		}
	}
}

// TestBlockResults verifies direct addressing returns the right bytes and
// that the counter stays 4 on a padded final block (1- and 2-byte tails).
func TestBlockResults(t *testing.T) {
	cases := []struct {
		raw  string
		k    int
		want string
	}{
		{"foobarb", 0, "foo"}, // full block
		{"foobarb", 1, "bar"}, // full block in the middle
		{"foobarb", 2, "b"},   // 1-byte tail, two '='
		{"fooba", 1, "ba"},    // 2-byte tail, one '='
	}
	for _, c := range cases {
		var idx blockIndex
		buf := b64.Encode([]byte(c.raw))
		got, err := idx.at(buf, c.k)
		if err != nil || string(got) != c.want {
			t.Fatalf("%q k=%d: got %q err=%v want %q", c.raw, c.k, got, err, c.want)
		}
		if idx.lastBlockRead != 4 {
			t.Fatalf("%q k=%d: read %d, want 4", c.raw, c.k, idx.lastBlockRead)
		}
		if BlockCount(buf) != len(buf)/4 {
			t.Fatal("BlockCount mismatch")
		}
	}
}

// TestBlockOutOfRange pins the decidable bounds error.
func TestBlockOutOfRange(t *testing.T) {
	buf := b64.Encode([]byte("foobarb")) // 3 blocks
	for _, k := range []int{-1, 3, 99} {
		if _, err := DecodeBlockAt(buf, k); !errors.Is(err, ErrBlockOutOfRange) {
			t.Fatalf("k=%d: got %v want ErrBlockOutOfRange", k, err)
		}
	}
}

// TestBlockRejectsMalformed verifies block-level validation: unused tail
// bits on the final block, padding in a non-final block, and illegal chars
// all fail with the matching sentinel and no result.
func TestBlockRejectsMalformed(t *testing.T) {
	if out, err := DecodeBlockAt([]byte("Zm9="), 0); !errors.Is(err, b64.ErrUnusedBits) || out != nil {
		t.Fatalf("unused-bits tail block: %v partial=%v", err, out)
	}
	buf := b64.Encode([]byte("foobarbaz")) // 3 full blocks
	copy(buf[4:8], []byte("Zm9="))         // '=' inside a non-final block
	if out, err := DecodeBlockAt(buf, 1); !errors.Is(err, b64.ErrPadding) || out != nil {
		t.Fatalf("non-final padding: %v partial=%v", err, out)
	}
	bad := append(bytes.Clone(buf[:4]), []byte("****")...) // block 1 illegal
	if _, err := DecodeBlockAt(bad, 1); !errors.Is(err, b64.ErrChar) {
		t.Fatalf("illegal char block: %v", err)
	}
}
