package chunk_test

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/chunk"
)

func TestRollEqualsRecomputeEveryOffset(t *testing.T) {
	cases := []struct{ n, bs int }{
		{1000, 16}, {4097, 64}, {100, 100}, {257, 1}, {513, 7},
	}
	for _, c := range cases {
		data := make([]byte, c.n)
		rand.New(rand.NewSource(int64(c.n))).Read(data)
		w := chunk.Weak(data[:c.bs])
		for i := 0; i+c.bs < len(data); i++ {
			w = chunk.RollSum(w, data[i], data[i+c.bs])
			if want := chunk.Weak(data[i+1 : i+1+c.bs]); w != want {
				t.Fatalf("n=%d bs=%d offset=%d: roll=%d recompute=%d", c.n, c.bs, i, w, want)
			}
		}
	}
}

func TestWeakCollisionStrongDistinguishes(t *testing.T) {
	cases := []struct{ a, b []byte }{
		{[]byte{1, 2, 3, 4}, []byte{2, 1, 3, 4}},
		{[]byte{0, 0, 5, 5}, []byte{5, 5, 0, 0}},
		{[]byte{10, 200, 30}, []byte{30, 200, 10}},
	}
	for _, c := range cases {
		if chunk.Weak(c.a) != chunk.Weak(c.b) {
			t.Fatalf("expected weak collision for %v vs %v", c.a, c.b)
		}
		if chunk.Strong(c.a) == chunk.Strong(c.b) {
			t.Fatalf("strong checksum must distinguish %v vs %v", c.a, c.b)
		}
	}
}

func TestWeakOpsBound(t *testing.T) {
	cases := []struct{ n, bs int }{
		{10000, 64}, {999, 16}, {500, 500},
	}
	for _, c := range cases {
		data := make([]byte, c.n)
		rand.New(rand.NewSource(int64(c.bs))).Read(data)
		chunk.ResetCounters()
		w := chunk.Weak(data[:c.bs])
		for i := 0; i+c.bs < len(data); i++ {
			w = chunk.RollSum(w, data[i], data[i+c.bs])
		}
		if got, limit := chunk.Ops(), int64(4*c.n); got > limit {
			t.Fatalf("n=%d bs=%d: weak ops %d exceed %d", c.n, c.bs, got, limit)
		}
	}
}

func TestSplitAndFullBlocks(t *testing.T) {
	cases := []struct {
		n, bs        int
		blocks, full int
		err          error
	}{
		{0, 8, 0, 0, nil},
		{8, 8, 1, 1, nil},
		{16, 8, 2, 2, nil},
		{17, 8, 3, 2, nil},
		{5, 8, 1, 0, nil},
		{10, 0, 0, 0, chunk.ErrBlockSize},
		{10, -3, 0, 0, chunk.ErrBlockSize},
	}
	for _, c := range cases {
		blocks, err := chunk.Split(make([]byte, c.n), c.bs)
		if !errors.Is(err, c.err) {
			t.Fatalf("n=%d bs=%d: err=%v want %v", c.n, c.bs, err, c.err)
		}
		if len(blocks) != c.blocks {
			t.Fatalf("n=%d bs=%d: got %d blocks want %d", c.n, c.bs, len(blocks), c.blocks)
		}
		if full := chunk.FullBlocks(c.n, c.bs); full != c.full {
			t.Fatalf("n=%d bs=%d: full=%d want %d", c.n, c.bs, full, c.full)
		}
	}
}
