package chunk

import (
	"math/rand"
	"testing"
)

func TestSplitTable(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		size int
		err  bool
		n    int
	}{
		{"zero-size", []byte("abc"), 0, true, 0},
		{"empty", []byte{}, 4, false, 0},
		{"size-bigger", []byte("ab"), 8, false, 1},
		{"exact-multiple", []byte("abcdef"), 3, false, 2},
		{"last-partial", []byte("abcde"), 2, false, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blocks, err := Split(c.data, c.size)
			if c.err {
				if err != ErrBlockSize {
					t.Fatalf("want ErrBlockSize, got %v", err)
				}
				return
			}
			if err != nil || len(blocks) != c.n {
				t.Fatalf("blocks=%d err=%v, want %d", len(blocks), err, c.n)
			}
			for i, b := range blocks {
				if b.Index != i || b.Weak != Weak(b.Data) || b.Strong != Strong(b.Data) {
					t.Fatalf("block %d checksums inconsistent", i)
				}
			}
		})
	}
}

func TestRollingMatchesRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, n := range []int{1, 2, 3, 7, 16, 64} {
		data := make([]byte, 300)
		rng.Read(data)
		r, err := NewRoller(data, n)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i+n <= len(data); i++ {
			want := Weak(data[i : i+n])
			if got := r.Weak(); got != want {
				t.Fatalf("n=%d offset=%d rolling=%d recompute=%d", n, i, got, want)
			}
			if r.Offset() != i {
				t.Fatalf("offset=%d want %d", r.Offset(), i)
			}
			r.Advance()
		}
	}
}

func TestWeakOpsBound(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sizes := []int{4, 16, 64}
	for _, n := range sizes {
		data := make([]byte, 512)
		rng.Read(data)
		r, _ := NewRoller(data, n)
		for r.Valid() {
			r.Advance()
		}
		if got := r.Stats().WeakOps; got > 4*len(data) {
			t.Fatalf("n=%d weakOps=%d > 4*len=%d", n, got, 4*len(data))
		}
	}
}

func TestWeakCollisionBlockedByStrong(t *testing.T) {
	pairs := [][2][]byte{
		{{0x10, 0x00}, {0x00, 0x10}},
		{{0xAB, 0xCD}, {0xCD, 0xAB}},
		{{0x01, 0x02, 0x03}, {0x00, 0x03, 0x03}},
	}
	for _, p := range pairs {
		if Weak(p[0]) != Weak(p[1]) {
			t.Fatalf("setup failed: weak sums differ")
		}
		if Strong(p[0]) == Strong(p[1]) {
			t.Fatalf("strong sums must differ for distinct blocks")
		}
	}
}
