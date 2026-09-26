package pack

import (
	"errors"
	"sync"
	"testing"

	"ontology/bits"
)

// TestSchemaLayout is invariant 2 at the pack layer: ranges start at 0,
// are contiguous with no overlap/gap, total <= 64; bad layouts rejected.
func TestSchemaLayout(t *testing.T) {
	cases := []struct {
		name   string
		fields []bits.Field
		total  int
		ok     bool
	}{
		{"notes", []bits.Field{{Width: 3, Value: 0, Signed: false}, {Width: 5, Value: 0, Signed: false}, {Width: 4, Value: 0, Signed: true}, {Width: 2, Value: 0, Signed: false}}, 14, true},
		{"exact64", []bits.Field{{Width: 64, Value: 0, Signed: false}}, 64, true},
		{"width0", []bits.Field{{Width: 0, Value: 0, Signed: false}}, 0, false},
		{"width65", []bits.Field{{Width: 65, Value: 0, Signed: false}}, 0, false},
		{"sum65", []bits.Field{{Width: 40, Value: 0, Signed: false}, {Width: 25, Value: 0, Signed: false}}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewSchema(c.fields)
			if c.ok != (err == nil) {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
			if !c.ok {
				if !errors.Is(err, bits.ErrBadWidth) {
					t.Fatalf("want ErrBadWidth, got %v", err)
				}
				return
			}
			rs := s.Ranges()
			wantOff := 0
			for _, r := range rs { // contiguous from 0, no overlap/gap
				if r.Off != wantOff {
					t.Fatalf("off=%d want %d (gap/overlap)", r.Off, wantOff)
				}
				wantOff += r.Width
			}
			if wantOff != c.total || wantOff > 64 {
				t.Fatalf("total=%d want %d", wantOff, c.total)
			}
		})
	}
}

// TestSchemaPackUnpack verifies schema pack/unpack round trip and that
// layout ranges agree with the underlying bits packing (invariant 1 at
// the schema layer), plus no-trace rejection.
func TestSchemaPackUnpack(t *testing.T) {
	s, err := NewSchema([]bits.Field{{Width: 3, Value: 0, Signed: false}, {Width: 5, Value: 0, Signed: false}, {Width: 4, Value: 0, Signed: true}, {Width: 2, Value: 0, Signed: false}})
	if err != nil {
		t.Fatal(err)
	}
	word, err := s.Pack([]int64{5, 18, -3, 1})
	if err != nil || word != 7573 {
		t.Fatalf("pack=%d,%v want 7573", word, err)
	}
	got := s.Unpack(word)
	want := []int64{5, 18, -3, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unpack[%d]=%d want %d", i, got[i], want[i])
		}
	}
	if _, err := s.Pack([]int64{9, 18, -3, 1}); !errors.Is(err, bits.ErrValueOverflow) {
		t.Fatalf("f0=9 must overflow, got %v", err)
	}
	if w, err := s.Pack([]int64{5, 18}); err == nil || w != 0 {
		t.Fatalf("value-count mismatch must fail, got %d %v", w, err)
	}
}

// TestBitLocationConstantTime proves locating bit m-1 does not scan from
// bit 0: the examined-bit count stays a constant (1) as m grows 100 ->
// 10000, i.e. O(1) via i/8 and i%8, not O(m).
func TestBitLocationConstantTime(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := NewBitmap(m)
		if _, err := b.Test(m - 1); err != nil {
			t.Fatal(err)
		}
		p := b.probe.Load()
		if p > 1 { // constant independent of m; a scan would give ~m
			t.Fatalf("m=%d examined %d bits, want constant 1", m, p)
		}
	}
}

// TestBitmapNoTrace: out-of-range set/clear/test fail distinctly and the
// backing bytes never change; bitmap stays usable.
func TestBitmapNoTrace(t *testing.T) {
	b := NewBitmap(10)
	for _, i := range []int{-1, 10, 100} {
		if err := b.Set(i); !errors.Is(err, bits.ErrBitIndex) {
			t.Fatalf("Set %d err=%v", i, err)
		}
		if err := b.Clear(i); !errors.Is(err, bits.ErrBitIndex) {
			t.Fatalf("Clear %d err=%v", i, err)
		}
		if _, err := b.Test(i); !errors.Is(err, bits.ErrBitIndex) {
			t.Fatalf("Test %d err=%v", i, err)
		}
	}
	for _, x := range b.bm {
		if x != 0 {
			t.Fatal("bitmap changed on rejected operations")
		}
	}
	if err := b.Set(9); err != nil {
		t.Fatalf("must stay usable: %v", err)
	}
	if ok, _ := b.Test(9); !ok {
		t.Fatal("bit 9 not set after valid Set")
	}
}

// TestBitmapConcurrent: distinct bits set by distinct goroutines end
// exactly set under -race.
func TestBitmapConcurrent(t *testing.T) {
	for _, n := range []int{100, 1000, 9999} {
		b := NewBitmap(n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); _ = b.Set(i) }(i)
		}
		wg.Wait()
		for i := 0; i < n; i++ {
			if ok, _ := b.Test(i); !ok {
				t.Fatalf("n=%d bit %d not set", n, i)
			}
		}
	}
}
