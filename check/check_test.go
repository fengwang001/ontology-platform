package check_test

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/check"
	"ontology/egcd"
	"ontology/num"
)

func TestExtendedGCD(t *testing.T) {
	cases := []struct{ a, b int }{
		{240, 46}, {46, 240}, {0, 0}, {7, 0}, {0, -9}, {-240, 46}, {1, 1}, {17, 5},
		{1_000_000_000_000_000_000, 3}, {679891637638612258, 420196140727489673},
	}
	for _, c := range cases {
		g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil {
			t.Fatalf("ExtendedGCD(%d, %d) err = %v", c.a, c.b, err)
		}
		rg, _, _ := check.RefGCD(c.a, c.b)
		if g != rg || g < 0 || !check.BezoutOK(c.a, c.b, x, y, g) {
			t.Errorf("ExtendedGCD(%d, %d) = g:%d x:%d y:%d, want g=%d and identity", c.a, c.b, g, x, y, rg)
		}
	}
}

func TestPinned240_46(t *testing.T) {
	g, x, y, err := egcd.ExtendedGCD(240, 46)
	if err != nil || g != 2 || !check.BezoutOK(240, 46, x, y, g) {
		t.Fatalf("g=%d x=%d y=%d err=%v, want g=2 with 240x+46y==2", g, x, y, err)
	}
}

func TestWrongBackSubstitution(t *testing.T) {
	var buggy func(a, b int) (g, x, y int)
	buggy = func(a, b int) (g, x, y int) {
		if b == 0 {
			return a, 1, 0
		}
		g, x1, y1 := buggy(b, a%b)
		return g, y1, x1 // BUG: dropped the (a/b)*y1 term
	}
	g, x, y := buggy(240, 46)
	if g != 2 || check.BezoutOK(240, 46, x, y, g) {
		t.Fatalf("buggy result g=%d x=%d y=%d should violate the identity", g, x, y)
	}
}

func TestModInverse(t *testing.T) {
	valid := []struct{ a, m int }{{3, 11}, {10, 17}, {42, 2017}, {123456789, 1_000_000_007}}
	for _, c := range valid {
		if inv, err := num.ModInverse(c.a, c.m); err != nil || (c.a*inv)%c.m != 1 {
			t.Errorf("ModInverse(%d, %d) = %d, %v", c.a, c.m, inv, err)
		}
	}
	errs := []struct {
		a, m int
		want error
	}{{-1, 7, num.ErrBadArg}, {3, 0, num.ErrBadArg}, {3, -5, num.ErrBadArg},
		{2, 4, num.ErrNoInverse}, {6, 9, num.ErrNoInverse}, {0, 5, num.ErrNoInverse}}
	for _, c := range errs {
		if _, err := num.ModInverse(c.a, c.m); !errors.Is(err, c.want) {
			t.Errorf("ModInverse(%d, %d) err = %v, want %v", c.a, c.m, err, c.want)
		}
	}
}

func TestRecursionDepthBound(t *testing.T) {
	a, b := 679891637638612258, 420196140727489673 // worst case: consecutive Fibonacci
	if _, _, _, err := egcd.ExtendedGCD(a, b); err != nil {
		t.Fatal(err)
	}
	if d, bound := float64(egcd.MaxDepth()), 2*math.Log2(float64(a))+2; d > bound {
		t.Errorf("recursion depth %v exceeds bound %v", d, bound)
	}
}

func TestConcurrentDeterministic(t *testing.T) {
	g0, x0, y0, _ := egcd.ExtendedGCD(240, 46)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				g, x, y, err := egcd.ExtendedGCD(240, 46)
				if err != nil || g != g0 || x != x0 || y != y0 {
					t.Error("non-deterministic result under concurrency")
					return
				}
			}
		}()
	}
	wg.Wait()
}
