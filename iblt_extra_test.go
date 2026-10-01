package ontology

import (
	"errors"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

func TestSubtractErrorsAndSelfSubtract(t *testing.T) {
	a, _ := New(300)
	b, _ := New(303)
	if _, err := Subtract(a, b); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("size mismatch err = %v", err)
	}

	a.Add(1)
	a.Add(2)
	a.Remove(99)
	zero, err := Subtract(a, a)
	if err != nil {
		t.Fatal(err)
	}
	if cs := cellsOf(t, zero); !slices.Equal(cs, make([]cell, a.M())) {
		t.Fatalf("self subtract not zero: %+v", cs)
	}
	t.Log("s-s: judgment=success all-zero; basis=identical operands use one snapshot")
}

func TestAddThenRemoveRestores(t *testing.T) {
	s, _ := New(300)
	s.Add(5)
	s.Remove(8)
	before := cellsOf(t, s)
	s.Add(123456)
	s.Remove(123456)
	if after := cellsOf(t, s); !slices.Equal(before, after) {
		t.Fatalf("add-then-remove did not restore:\nbefore=%+v\nafter =%+v", before, after)
	}
	t.Log("Add(x) then Remove(x) for an unwritten key restores the prior state")
}

func TestDecodeSlicesNoAlias(t *testing.T) {
	a, _ := New(300)
	b, _ := New(300)
	a.Add(11)
	b.Add(22)
	diff, _ := Subtract(a, b)

	firstA, firstB, err := diff.Decode(100)
	if err != nil {
		t.Fatal(err)
	}
	firstA[0] = 999
	firstB[0] = 999
	secondA, secondB, err := diff.Decode(100)
	if err != nil {
		t.Fatal(err)
	}
	if secondA[0] != 11 || secondB[0] != 22 {
		t.Fatalf("returned slices alias reusable state: %v %v", secondA, secondB)
	}
	t.Log("mutating returned slices does not affect later Decode output")
}

func TestCommutativeBatchSameCells(t *testing.T) {
	keys := []uint64{3, 0, 77, 1 << 63, 12345, 9, 55, 2}

	build := func(order []int) []cell {
		s, _ := New(300)
		for _, i := range order {
			if i%2 == 0 {
				s.Add(keys[i])
			} else {
				s.Remove(keys[i])
			}
		}
		return cellsOf(t, s)
	}
	base := build([]int{0, 1, 2, 3, 4, 5, 6, 7})
	for _, order := range [][]int{
		{7, 6, 5, 4, 3, 2, 1, 0},
		{3, 0, 5, 7, 1, 6, 2, 4},
	} {
		if got := build(order); !slices.Equal(got, base) {
			t.Fatalf("order %v produced different cells", order)
		}
	}
	t.Log("mixed Add/Remove in arbitrary orders yield cell-wise identical sketches")
}

func TestConcurrentAccess(t *testing.T) {
	s, _ := New(300)
	var wg sync.WaitGroup

	// Writers: every added key is removed by the same goroutine, so the
	// final sketch must be all zero.
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(g), uint64(g*7+1)))
			keys := make([]uint64, 500)
			for i := range keys {
				keys[i] = rng.Uint64()
				s.Add(keys[i])
			}
			for _, x := range keys {
				s.Remove(x)
			}
		}(g)
	}

	// Readers hammer Decode and self-subtract concurrently.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_, _, _ = s.Decode(10)
				if z, err := Subtract(s, s); err != nil {
					t.Error(err)
				} else if cs := cellsOf(t, z); !slices.Equal(cs, make([]cell, s.M())) {
					t.Error("concurrent self-subtract was non-zero")
				}
			}
		}()
	}

	wg.Wait()
	if cs := cellsOf(t, s); !slices.Equal(cs, make([]cell, 300)) {
		t.Fatalf("concurrent add/remove did not cancel: non-zero cells remain")
	}
	t.Log("8 add/remove goroutines + 4 decode/subtract readers; final sketch all zero")
}
