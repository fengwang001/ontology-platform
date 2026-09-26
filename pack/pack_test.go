package pack

import (
	"errors"
	"sync"
	"testing"

	"ontology/bits"
)

func TestSchemaPackUnpack(t *testing.T) {
	s, err := NewSchema([]int{3, 5, 4, 2}, []bool{false, false, true, false})
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	word, err := s.Pack(5, 18, -3, 1)
	if err != nil || word != 7573 {
		t.Fatalf("pack: got (%d,%v), want (7573,nil)", word, err)
	}
	vals, err := s.Unpack(word)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	for i, want := range []int64{5, 18, -3, 1} {
		if vals[i] != want {
			t.Errorf("field %d: got %d, want %d", i, vals[i], want)
		}
	}
	if _, err := s.Pack(5, 18, -3); !errors.Is(err, ErrArity) {
		t.Errorf("arity: want ErrArity, got %v", err)
	}
	if _, err := s.Pack(5, 18, 8, 1); !errors.Is(err, bits.ErrValueOverflow) {
		t.Errorf("overflow: want ErrValueOverflow, got %v", err)
	}
	if _, err := NewSchema([]int{0}, []bool{false}); !errors.Is(err, bits.ErrBadWidth) {
		t.Errorf("width 0: want ErrBadWidth, got %v", err)
	}
	if _, err := NewSchema([]int{40, 40}, []bool{false, false}); !errors.Is(err, bits.ErrBadWidth) {
		t.Errorf("total 80: want ErrBadWidth, got %v", err)
	}
}

// TestLocateConstantProbes proves bit location is O(1): the number of bits
// inspected to locate the last bit does not grow with the bitmap size.
func TestLocateConstantProbes(t *testing.T) {
	const maxProbed = 4 // small constant, independent of m
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := NewBitmap(m)
		if err := b.Set(m - 1); err != nil {
			t.Fatalf("m=%d set: %v", m, err)
		}
		if b.probed > maxProbed {
			t.Fatalf("m=%d set probed %d bits, want <= %d", m, b.probed, maxProbed)
		}
		if got, err := b.Test(m - 1); err != nil || !got {
			t.Fatalf("m=%d test: got (%v,%v)", m, got, err)
		}
		if b.probed > maxProbed {
			t.Fatalf("m=%d test probed %d bits, want <= %d", m, b.probed, maxProbed)
		}
	}
}

func TestConcurrentSetDistinctBits(t *testing.T) {
	const n = 256
	b := NewBitmap(n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Set(i); err != nil { // distinct bits, same bytes too
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if got, err := b.Test(i); err != nil || !got {
			t.Fatalf("bit %d: got (%v,%v), want (true,nil)", i, got, err)
		}
	}
	if got, err := b.Test(n); got || !errors.Is(err, bits.ErrBitIndex) {
		t.Fatalf("bit %d out of range: got (%v,%v)", n, got, err)
	}
}
