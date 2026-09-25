package check_test

import (
	"errors"
	"math/bits"
	"sync"
	"testing"

	"ontology/check"
	"ontology/egcd"
	"ontology/num"
)

var egcdCases = []struct{ a, b, g int }{
	{240, 46, 2}, {0, 0, 0}, {7, 0, 7}, {0, -9, 9}, {-12, 18, 6}, {-8, -20, 4},
	{17, 31, 1}, {1, 1, 1}, {1 << 40, 1 << 30, 1 << 30}, {999999999999999989, 999983, 1},
}

func TestExtendedGCDBezout(t *testing.T) {
	for _, c := range egcdCases {
		g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil || g != c.g || g < 0 || c.a*x+c.b*y != g {
			t.Errorf("ExtendedGCD(%d,%d) = (%d,%d,%d,%v)", c.a, c.b, g, x, y, err)
		}
		if g != check.NaiveGCD(c.a, c.b) {
			t.Errorf("gcd(%d,%d): got %d, naive %d", c.a, c.b, g, check.NaiveGCD(c.a, c.b))
		}
		if c.a >= 0 && c.b >= 0 {
			bg, bx, by := check.BigBezout(c.a, c.b)
			if bg != g || c.a*bx+c.b*by != bg {
				t.Errorf("big (%d,%d): g %d vs %d", c.a, c.b, g, bg)
			}
		}
	}
}

func TestModInverse(t *testing.T) {
	for _, c := range []struct{ a, m, want int }{{3, 11, 4}, {10, 17, 12}, {1, 1, 0}, {0, 1, 0}} {
		r, err := num.ModInverse(c.a, c.m)
		if err != nil || r != c.want || (c.a*r)%c.m != 1%c.m {
			t.Errorf("ModInverse(%d,%d) = (%d,%v)", c.a, c.m, r, err)
		}
	}
}

func TestModInverseErrors(t *testing.T) {
	for _, c := range [][2]int{{-1, 5}, {3, 0}, {3, -7}} {
		if _, err := num.ModInverse(c[0], c[1]); !errors.Is(err, num.ErrBadArg) {
			t.Errorf("ModInverse(%d,%d): want ErrBadArg, got %v", c[0], c[1], err)
		}
	}
	for _, c := range [][2]int{{2, 4}, {6, 9}, {0, 5}} {
		if _, err := num.ModInverse(c[0], c[1]); !errors.Is(err, num.ErrNoInverse) {
			t.Errorf("ModInverse(%d,%d): want ErrNoInverse, got %v", c[0], c[1], err)
		}
	}
}

func TestBuggyImplFailsIdentity(t *testing.T) {
	var buggy func(a, b int) (g, x, y int)
	buggy = func(a, b int) (g, x, y int) {
		if b == 0 {
			return a, 1, 0
		}
		g, x1, y1 := buggy(b, a%b)
		return g, y1, x1 // BUG: dropped -(a/b)*y1 term (the incident)
	}
	g, x, y := buggy(240, 46)
	if g == 2 && 240*x+46*y == g {
		t.Fatalf("buggy satisfies identity: x=%d y=%d", x, y)
	}
}

func TestRecursionDepthBound(t *testing.T) {
	cases := [][2]int{{1e18, 999999999999999989}, {1100087778366101931, 679891637638612258}}
	for _, c := range cases { // the Fib(88)/Fib(87) pair is the worst case
		if d, bound := egcd.Depth(c[0], c[1]), 2*bits.Len(uint(max(c[0], c[1])))+2; d > bound {
			t.Errorf("depth(%d,%d) = %d > bound %d", c[0], c[1], d, bound)
		}
	}
}

func TestConcurrentPure(t *testing.T) {
	g0, x0, y0, _ := egcd.ExtendedGCD(240, 46)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if g, x, y, _ := egcd.ExtendedGCD(240, 46); g != g0 || x != x0 || y != y0 {
					t.Error("nondeterministic result")
					return
				}
			}
		}()
	}
	wg.Wait()
}
