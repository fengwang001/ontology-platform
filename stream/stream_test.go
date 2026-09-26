package stream

import (
	"bytes"
	"testing"

	"ontology/splitmix"
)

// naiveKeystream is the straightforward reference: it derives every byte by
// walking the stream from block 0, exactly the construction the
// random-access implementation must match byte for byte.
func naiveKeystream(seed uint64, off int64, n int) []byte {
	out := make([]byte, n)
	for k := range out {
		pos := uint64(off) + uint64(k)
		b := splitmix.SplitMix64(seed ^ (pos / 8))
		out[k] = byte(b >> (8 * (pos % 8)))
	}
	return out
}

func TestKeystreamMatchesNaiveReference(t *testing.T) {
	cases := []struct {
		name  string
		seed  uint64
		off   int64
		bytes int
	}{
		{"seed1 block0", 1, 0, 8},
		{"spec seed block0", 0xe821eebbc0778421, 0, 8},
		{"unaligned small", 0x123456789abcdef0, 3, 21},
		{"across boundary", 42, 7, 18},
		{"far offset", 99, 1 << 20, 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(tc.seed)
			// XOR against zero bytes yields the keystream itself.
			got := c.XORAt(tc.off, make([]byte, tc.bytes))
			want := naiveKeystream(tc.seed, tc.off, tc.bytes)
			if !bytes.Equal(got, want) {
				t.Fatalf("keystream at off=%d n=%d differs from naive reference", tc.off, tc.bytes)
			}
		})
	}
}

func TestEncryptAtMatchesSequential(t *testing.T) {
	const seed = uint64(0x0123456789abcdef)
	const size = 4096
	seq := naiveKeystream(seed, 0, size)
	c := New(seed)
	cases := []struct {
		off int64
		n   int
	}{{0, 8}, {1, 7}, {8, 8}, {63, 9}, {1000, 33}, {3000, 1000}}
	for _, tc := range cases {
		p := make([]byte, tc.n)
		for i := range p {
			p[i] = byte(0x37*i + 11)
		}
		got := c.XORAt(int64(tc.off), p)
		for i := range got {
			if want := p[i] ^ seq[int(tc.off)+i]; got[i] != want {
				t.Fatalf("off=%d i=%d: got 0x%02x want 0x%02x", tc.off, i, got[i], want)
			}
		}
	}
}

func TestRandomAccessComputesOneBlock(t *testing.T) {
	const seed = uint64(777)
	// m ranges from 100 to 10000: a single byte at byte offset 8*m (and an
	// unaligned variant) must always cost exactly one block regardless of m.
	for _, m := range []int64{100, 333, 1000, 5000, 10000} {
		for _, delta := range []int64{0, 3, 7} {
			c := New(seed)
			c.XORAt(8*m+delta, []byte{0xAB})
			if c.blocksComputed != 1 {
				t.Fatalf("m=%d delta=%d: computed %d blocks, want 1", m, delta, c.blocksComputed)
			}
		}
	}
	// Sanity: the counter really counts — a 17-byte call starting at block
	// boundary spans 3 blocks.
	c := New(seed)
	c.XORAt(0, make([]byte, 17))
	if c.blocksComputed != 3 {
		t.Fatalf("17 aligned bytes computed %d blocks, want 3", c.blocksComputed)
	}
}
