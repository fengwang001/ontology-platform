package bits

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
)

func randFields(maxN, maxW int) []Field {
	fs := []Field{}
	total := 0
	for n := 1 + rand.IntN(maxN); n > 0 && total < 64; n-- {
		w := 1 + rand.IntN(min(maxW, 64-total))
		signed := rand.IntN(2) == 0
		v := int64(rand.Uint64() & (uint64(1)<<uint(w) - 1))
		if signed {
			v -= int64(uint64(1) << uint(w-1))
		} else {
			v &= 1<<63 - 1
		}
		fs = append(fs, Field{w, v, signed})
		total += w
	}
	return fs
}

func roundTrip(t *testing.T, fs []Field) uint64 {
	word, err := PackFields(fs)
	if err != nil {
		t.Fatal(err)
	}
	off := 0
	for _, f := range fs {
		if Extract(word, off, f.Width, f.Signed) != f.Value {
			t.Fatalf("off=%d %+v word=%x", off, f, word)
		}
		off += f.Width
	}
	return word
}

func TestRoundTrip(t *testing.T) {
	notes := []Field{{3, 5, false}, {5, 18, false}, {4, -3, true}, {2, 1, false}}
	if w := roundTrip(t, notes); w != 7573 {
		t.Fatalf("notes vector packed to %d, want 7573", w)
	}
	for range 500 {
		roundTrip(t, randFields(8, 64))
	}
}

func TestNaiveReference(t *testing.T) {
	for range 300 {
		fs := randFields(6, 16)
		got, err := PackFields(fs)
		if err != nil {
			t.Fatal(err)
		}
		var ref, off uint64
		for _, f := range fs {
			ref |= (uint64(f.Value) & (uint64(1)<<uint(f.Width) - 1)) << off
			off += uint64(f.Width)
		}
		if got != ref {
			t.Fatalf("got %x ref %x %+v", got, ref, fs)
		}
	}
}

func TestRejectedPackNoTrace(t *testing.T) {
	bad := []struct {
		fs []Field
		e  error
	}{
		{[]Field{{3, 9, false}}, ErrValueOverflow}, {[]Field{{4, 8, true}}, ErrValueOverflow},
		{[]Field{{4, -9, true}}, ErrValueOverflow}, {[]Field{{8, -1, false}}, ErrValueOverflow},
		{[]Field{{0, 0, false}}, ErrBadWidth}, {[]Field{{65, 0, false}}, ErrBadWidth},
		{[]Field{{33, 0, false}, {33, 0, false}}, ErrBadWidth},
	}
	for i, c := range bad {
		if w, err := PackFields(c.fs); w != 0 || !errors.Is(err, c.e) {
			t.Fatalf("case %d = %d,%v want 0,%v", i, w, err, c.e)
		}
	}
	if w, err := PackFields([]Field{{3, 5, false}}); err != nil || w != 5 {
		t.Fatal("use after rejection failed")
	}
}

func TestBitmapErrorsNoTrace(t *testing.T) {
	bm := make([]byte, 13)
	for _, i := range []int{0, 7, 99} {
		if SetBit(bm, i) != nil {
			t.Fatal(i)
		}
		if ok, _ := TestBit(bm, i); !ok {
			t.Fatal(i)
		}
		if ClearBit(bm, i) != nil {
			t.Fatal(i)
		}
		if ok, _ := TestBit(bm, i); ok {
			t.Fatal(i)
		}
	}
	for _, raw := range [][]byte{{}, {0}, {0xFF, 0}} {
		b := append([]byte(nil), raw...)
		for _, i := range []int{-1, 8 * len(b), 8*len(b) + 5} {
			if SetBit(b, i) != ErrBitIndex || ClearBit(b, i) != ErrBitIndex {
				t.Fatalf("len=%d i=%d", len(b), i)
			}
			if _, err := TestBit(b, i); err != ErrBitIndex {
				t.Fatal("TestBit wrong error")
			}
		}
		if string(b) != string(raw) {
			t.Fatal("bitmap mutated on rejection")
		}
	}
	if ErrBitIndex == ErrValueOverflow || ErrBitIndex == ErrBadWidth {
		t.Fatal("sentinels must be distinct")
	}
	if SetBit(make([]byte, 2), 9) != nil {
		t.Fatal("must stay usable after rejection")
	}
}

func TestConcurrentSet(t *testing.T) {
	for _, n := range []int{2, 64, 1000} {
		bm := make([]byte, (n+7)/8)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); _ = SetBit(bm, i) }(i)
		}
		wg.Wait()
		for i := 0; i < 8*len(bm); i++ {
			if got, _ := TestBit(bm, i); got != (i < n) {
				t.Fatalf("n=%d bit %d wrong", n, i)
			}
		}
	}
}
