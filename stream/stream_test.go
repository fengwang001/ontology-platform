package stream

import (
	"bytes"
	"math/rand"
	"testing"

	"ontology/splitmix"
)

const (
	testKey   uint64 = 0x0123456789ABCDEF
	testNonce uint64 = 0x0000000000000001
)

func testSeed() uint64 { return splitmix.Seed(testKey, testNonce) }

// naiveKeystream is the independent "generate every block from 0 with
// splitmix64" reference written straight from the spec.
func naiveKeystream(seed uint64, n int64) []byte {
	out := make([]byte, n)
	for j := range out {
		b := splitmix.SplitMix64(seed ^ uint64(j/8))
		out[j] = byte(b >> (8 * uint64(j%8)))
	}
	return out
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func TestSplitMixVectors(t *testing.T) {
	seed := testSeed()
	cases := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"splitmix64(0)", splitmix.SplitMix64(0), 0xE220A8397B1DCDAF},
		{"seed", seed, 0xe821eebbc0778421},
		{"block0", New(seed).block(0), 0x8a236066f56b86f6},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %#016x, want %#016x", tc.name, tc.got, tc.want)
		}
	}
}

func TestEightByteVector(t *testing.T) {
	c := New(testSeed())
	plain := []byte("abcdefgh")
	wantKS := []byte{0xf6, 0x86, 0x6b, 0xf5, 0x66, 0x60, 0x23, 0x8a}
	wantCT := []byte{0x97, 0xe4, 0x08, 0x91, 0x03, 0x06, 0x44, 0xe2}
	b0 := c.block(0)
	ct := c.Encrypt(plain)
	for j := 0; j < 8; j++ {
		if ks := byte(b0 >> (8 * uint64(j))); ks != wantKS[j] {
			t.Errorf("keystream[%d] = %#02x, want %#02x", j, ks, wantKS[j])
		}
		if ct[j] != wantCT[j] {
			t.Errorf("cipher[%d] = %#02x, want %#02x", j, ct[j], wantCT[j])
		}
	}
}

func TestEncryptDecryptSymmetry(t *testing.T) {
	c := New(testSeed())
	rng := rand.New(rand.NewSource(1))
	cases := [][]byte{nil, {}, {0x00}, {0xff}, []byte("abcdefgh"), make([]byte, 15)}
	for _, n := range []int{1, 7, 8, 9, 16, 100, 4096} {
		b := make([]byte, n)
		rng.Read(b)
		cases = append(cases, b)
	}
	for i, p := range cases {
		ct := c.Encrypt(p)
		if back := c.Decrypt(ct); !bytes.Equal(back, p) {
			t.Errorf("case %d (len %d): round trip mismatch", i, len(p))
		}
	}
}

func TestKeystreamMatchesNaiveReference(t *testing.T) {
	seed := testSeed()
	c := New(seed)
	for _, n := range []int64{1, 7, 8, 9, 100, 1000} {
		// Encrypting all-zero plaintext yields the raw keystream.
		if got := c.Encrypt(make([]byte, n)); !bytes.Equal(got, naiveKeystream(seed, n)) {
			t.Errorf("keystream length %d diverges from naive reference", n)
		}
	}
}

func TestEncryptAtMatchesSequential(t *testing.T) {
	seed := testSeed()
	c := New(seed)
	rng := rand.New(rand.NewSource(7))
	cases := []struct {
		off int64
		n   int
	}{
		{0, 8}, {1, 7}, {7, 9}, {8, 1}, {4999, 15}, {8*1234 + 3, 20},
	}
	for _, tc := range cases {
		p := make([]byte, tc.n)
		rng.Read(p)
		ks := naiveKeystream(seed, tc.off+int64(tc.n)) // sequential from block 0
		want := xorBytes(p, ks[tc.off:])
		if got := c.EncryptAt(tc.off, p); !bytes.Equal(got, want) {
			t.Errorf("EncryptAt(%d, len %d) diverges from sequential reference", tc.off, tc.n)
		}
	}
}

func TestRandomAccessComputesOneBlock(t *testing.T) {
	c := New(testSeed())
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		c.EncryptAt(8*m, []byte{0xAB}) // aligned offset, single byte in block m
		// Read directly inside the package: the value must never be reachable
		// through an exported method.
		if c.blocksComputed != 1 {
			t.Errorf("EncryptAt(8*%d, 1 byte) computed %d blocks, want 1", m, c.blocksComputed)
		}
	}
}
