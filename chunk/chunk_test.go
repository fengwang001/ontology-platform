package chunk

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		size   int
		err    bool
		nblock int
		tail   int
	}{
		{"empty", nil, 4, false, 0, 0},
		{"exact", []byte("abcdefgh"), 4, false, 2, 4},
		{"partial", []byte("abcdefghijk"), 4, false, 3, 3},
		{"bigger", []byte("ab"), 8, false, 1, 2},
		{"zero", []byte("ab"), 0, true, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bs, err := Split(c.data, c.size)
			if c.err {
				if err != ErrBadBlockSize {
					t.Fatalf("want ErrBadBlockSize, got %v", err)
				}
				return
			}
			if err != nil || len(bs) != c.nblock {
				t.Fatalf("blocks=%d err=%v, want %d", len(bs), err, c.nblock)
			}
			if len(bs) > 0 && len(bs[len(bs)-1].Data) != c.tail {
				t.Fatalf("tail=%d want %d", len(bs[len(bs)-1].Data), c.tail)
			}
		})
	}
}

func TestRollingEveryOffset(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 8; trial++ {
		data := make([]byte, 200+rng.Intn(300))
		rng.Read(data)
		size := 1 + rng.Intn(64)
		r, err := NewRoller(data, size)
		if err != nil {
			t.Fatal(err)
		}
		for r.Valid() {
			want := Weak(data[r.Pos() : r.Pos()+size])
			if r.Weak() != want {
				t.Fatalf("trial=%d offset=%d roll=%d recompute=%d",
					trial, r.Pos(), r.Weak(), want)
			}
			if !r.Advance() {
				break
			}
		}
		if r.WeakOps() > 4*len(data) {
			t.Fatalf("weakOps=%d > 4*%d", r.WeakOps(), len(data))
		}
	}
}

func TestWeakCollisionBlockedByStrong(t *testing.T) {
	a := []byte{1, 2, 3, 4}
	b := []byte{0, 2, 3, 5}
	if Weak(a) != Weak(b) {
		t.Fatalf("test setup: weak values differ: %d vs %d", Weak(a), Weak(b))
	}
	if bytes.Equal(Strong(a), Strong(b)) {
		t.Fatal("strong hashes unexpectedly equal")
	}
}
