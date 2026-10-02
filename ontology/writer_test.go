package ontology

import (
	"bytes"
	"errors"
	"math/bits"
	"testing"
)

func mustAppend(t *testing.T, w *Writer, vs ...uint32) {
	t.Helper()
	for _, v := range vs {
		if err := w.Append(v); err != nil {
			t.Fatalf("Append(%d): %v", v, err)
		}
	}
}

func TestNewParams(t *testing.T) {
	for _, c := range [][2]int{
		{0, 10}, {65537, 10}, {10, 0}, {10, 65537}, {-1, 10}, {10, -1},
	} {
		if _, err := New(c[0], c[1]); !errors.Is(err, ErrParam) {
			t.Fatalf("New(%d,%d) err=%v, want ErrParam", c[0], c[1], err)
		}
	}
	for _, c := range [][2]int{{1, 1}, {65536, 65536}} {
		if _, err := New(c[0], c[1]); err != nil {
			t.Fatalf("New(%d,%d): %v", c[0], c[1], err)
		}
	}
}

func TestAppendFullAndFlushEmpty(t *testing.T) {
	w, _ := New(100, 2)
	mustAppend(t, w, 1, 2)
	if err := w.Append(3); !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v want ErrFull", err)
	}
	p, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if p.Rows != 2 {
		t.Fatalf("rows=%d", p.Rows)
	}
	if _, err := w.Flush(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty flush err=%v want ErrEmpty", err)
	}
	mustAppend(t, w, 9)
}

func TestHybridExamples(t *testing.T) {
	cases := []struct {
		name string
		w    int
		in   []int
		want []byte
	}{
		{"spec ex1 w=2", 2, dup(1, 11, []int{0, 2, 3}),
			[]byte{0x16, 0x01, 0x03, 0x38, 0x00}},
		{"spec ex2 borrow f=5 rle=8", 3, dupAfter([]int{5, 6, 7}, 1, 13),
			[]byte{0x03, 0xF5, 0x93, 0x24, 0x10, 0x01}},
		{"spec ex2b absorb whole run", 3, dupAfter([]int{5, 6, 7}, 1, 12),
			[]byte{0x05, 0xF5, 0x93, 0x24, 0x49, 0x92, 0x04}},
		{"w=0 five zeros", 0, make([]int, 5), []byte{0x03}},
		{"w=0 nine zeros", 0, make([]int, 9), []byte{0x12}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hybridEncode(c.in, c.w, new(int))
			if !bytes.Equal(got, c.want) {
				t.Fatalf("encode=% x want % x", got, c.want)
			}
			dictLen := 1
			if c.w > 0 {
				dictLen = 1 << c.w
			}
			dec, err := hybridDecode(got, c.w, dictLen, len(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if !intsEqual(dec, c.in) {
				t.Fatalf("decode=%v want %v", dec, c.in)
			}
		})
	}
}

func TestRunLengths8And7(t *testing.T) {
	got8 := hybridEncode(dup(1, 8, nil), 2, new(int))
	if want := []byte{0x10, 0x01}; !bytes.Equal(got8, want) {
		t.Fatalf("len8=% x want % x", got8, want)
	}
	got7 := hybridEncode(dup(1, 7, nil), 2, new(int))
	if want := []byte{0x03, 0x55, 0x15}; !bytes.Equal(got7, want) {
		t.Fatalf("len7=% x want % x", got7, want)
	}
}

func TestWidthBoundaries(t *testing.T) {
	for k := 1; k <= 15; k++ {
		d := 1 << k
		if got := bits.Len(uint(d - 1)); got != k {
			t.Fatalf("D=%d w=%d want %d", d, got, k)
		}
		if got := bits.Len(uint(d)); got != k+1 {
			t.Fatalf("D=%d w=%d want %d", d+1, got, k+1)
		}
	}
}
