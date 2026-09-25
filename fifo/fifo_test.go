package fifo

import (
	"errors"
	"slices"
	"testing"
)

func liveBuf(k *KeyState) []int64 { return append([]int64(nil), k.buf[k.head:]...) }
func isPrefix(k *KeyState) bool {
	for i := range k.emitted {
		if k.emitted[i] != int64(i)+1 {
			return false
		}
	}
	return true
}
func TestEightStepScenario(t *testing.T) {
	k, _ := New(3)
	cases := []struct {
		seq, next int64
		buf, out  []int64
	}{
		{5, 1, []int64{5}, nil},
		{2, 1, []int64{2, 5}, nil},
		{4, 1, []int64{2, 4, 5}, nil},
		{1, 3, []int64{4, 5}, []int64{1, 2}},
		{3, 6, []int64{}, []int64{3, 4, 5}},
		{6, 7, []int64{}, []int64{6}},
		{7, 8, []int64{}, []int64{7}},
		{2, 8, []int64{}, nil},
	}
	for i, c := range cases {
		out, err := k.Feed(c.seq)
		if err != nil || k.next != c.next || !slices.Equal(liveBuf(k), c.buf) ||
			!slices.Equal(out, c.out) || !isPrefix(k) {
			t.Fatalf("step %d: next=%d buf=%v out=%v emitted=%v", i+1, k.next, liveBuf(k), out, k.Emitted())
		}
	}
	if k.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", k.Dropped())
	}
}
func TestOrderingNoHoles(t *testing.T) {
	cases := []struct {
		max  int
		seqs []int64
	}{
		{3, []int64{5, 2, 4, 1, 3, 6, 7, 2}},
		{2, []int64{3, 2, 1, 4, 1, 5}},
		{1, []int64{2, 1, 3, 2, 4}},
		{5, []int64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}},
		{4, []int64{1, 3, 2, 6, 4, 5, 7}},
	}
	for _, c := range cases {
		k, _ := New(c.max)
		for _, s := range c.seqs {
			if _, err := k.Feed(s); err != nil && !errors.Is(err, ErrBackpressure) {
				t.Fatal(err)
			}
			if k.Buffered() > c.max || !isPrefix(k) {
				t.Fatalf("bound=%d buffered=%d emitted=%v", c.max, k.Buffered(), k.Emitted())
			}
		}
	}
}
func reference(max int, seqs []int64) (emitted []int64, dropped int64) {
	next, held := int64(1), map[int64]bool{}
	for _, s := range seqs {
		switch {
		case s < next:
			dropped++
		case s == next:
			for {
				emitted, next = append(emitted, next), next+1
				if !held[next] {
					break
				}
				delete(held, next)
			}
		case !held[s] && len(held) < max:
			held[s] = true
		}
	}
	return
}
func TestNaiveReference(t *testing.T) {
	cases := []struct {
		max  int
		seqs []int64
	}{
		{3, []int64{5, 2, 4, 1, 3, 6, 7, 2}},
		{2, []int64{3, 2, 1, 4, 1, 5, 5, 2}},
		{4, []int64{8, 1, 2, 9, 3, 10, 11, 4, 5, 6, 7, 1}},
	}
	for _, c := range cases {
		k, _ := New(c.max)
		for _, s := range c.seqs {
			_, _ = k.Feed(s)
		}
		wantEmitted, wantDropped := reference(c.max, c.seqs)
		if !slices.Equal(k.Emitted(), wantEmitted) || k.Dropped() != wantDropped {
			t.Fatalf("max=%d seqs=%v: got %v/%d want %v/%d",
				c.max, c.seqs, k.Emitted(), k.Dropped(), wantEmitted, wantDropped)
		}
	}
}
func TestBackpressureBound(t *testing.T) {
	k, _ := New(3)
	for _, s := range []int64{10, 20, 30} {
		if _, err := k.Feed(s); err != nil {
			t.Fatal(err)
		}
	}
	before := liveBuf(k)
	if _, err := k.Feed(40); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("want ErrBackpressure, got %v", err)
	}
	if !slices.Equal(liveBuf(k), before) || k.next != 1 || k.Dropped() != 0 {
		t.Fatalf("rejection changed state: %v next=%d dropped=%d", liveBuf(k), k.next, k.Dropped())
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidMax) {
		t.Fatalf("New(0): want ErrInvalidMax, got %v", err)
	}
}
func TestProbeBound(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		k, _ := New(m)
		for s := int64(m + 1); s >= 2; s-- { // m consecutive future seqs, out of order
			if _, err := k.Feed(s); err != nil {
				t.Fatalf("m=%d seed %d: %v", m, s, err)
			}
		}
		if k.Buffered() != m {
			t.Fatalf("m=%d: buffered %d", m, k.Buffered())
		}
		if _, err := k.Feed(1); err != nil {
			t.Fatal(err)
		}
		if k.probes > int64(m)+4 { // O(1) head look per release, not an O(m) rescan
			t.Fatalf("m=%d: probes=%d exceeds m+const", m, k.probes)
		}
		if k.Buffered() != 0 || int64(len(k.Emitted())) != int64(m)+1 {
			t.Fatalf("m=%d: buf=%d emitted=%d", m, k.Buffered(), len(k.Emitted()))
		}
	}
}
