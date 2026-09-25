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

var gcdCases = []struct{ a, b, g int }{
	{240, 46, 2}, {0, 0, 0}, {7, 0, 7}, {0, 9, 9}, {-12, 0, 12}, {1, 1, 1},
	{-12, 18, 6}, {12, -18, 6}, {17, 5, 1}, {48, 36, 12}, {1071, 462, 21},
	{679891637638612258, 420196140727489673, 1},
}

func TestExtendedGCDIdentity(t *testing.T) {
	for _, c := range gcdCases {
		g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil || g != c.g || g < 0 {
			t.Fatalf("(%d,%d): g=%d err=%v, want g=%d", c.a, c.b, g, err, c.g)
		}
		if c.a*x+c.b*y != g {
			t.Errorf("(%d,%d): bezout violated, x=%d y=%d", c.a, c.b, x, y)
		}
		if rg, _, _ := check.RefGCD(c.a, c.b); rg != g {
			t.Errorf("(%d,%d): g=%d, math/big says %d", c.a, c.b, g, rg)
		}
		if max(c.a, c.b) <= 1<<20 && check.NaiveGCD(c.a, c.b) != g {
			t.Errorf("(%d,%d): g=%d, naive says %d", c.a, c.b, g, check.NaiveGCD(c.a, c.b))
		}
		if g2, x2, y2, _ := egcd.ExtendedGCD(c.a, c.b); g2 != g || x2 != x || y2 != y {
			t.Errorf("(%d,%d): non-deterministic coefficients", c.a, c.b)
		}
	}
}

// buggy 复刻线上事故：回代时漏掉 (a/b)·y' 项。
func buggy(a, b int) (g, x, y int) {
	if b == 0 {
		return a, 1, 0
	}
	g, x1, y1 := buggy(b, a%b)
	return g, y1, x1 - y1 // 正确应为 x1 - (a/b)*y1
}

func TestDroppedQuotientTermFails(t *testing.T) {
	g, x, y := buggy(240, 46)
	if g != 2 || 240*x+46*y == g {
		t.Errorf("buggy impl: g=%d x=%d y=%d unexpectedly satisfies bezout", g, x, y)
	}
}

func TestModInverse(t *testing.T) {
	for _, c := range []struct{ a, m, inv int }{{3, 11, 4}, {10, 17, 12}, {42, 2017, 1969}} {
		if inv, err := num.ModInverse(c.a, c.m); err != nil || inv != c.inv || (c.a*inv)%c.m != 1 {
			t.Errorf("ModInverse(%d,%d)=%d,%v", c.a, c.m, inv, err)
		}
	}
	for _, c := range []struct {
		a, m int
		want error
	}{{3, 0, num.ErrBadArg}, {3, -7, num.ErrBadArg}, {-1, 5, num.ErrBadArg},
		{2, 4, num.ErrNoInverse}, {12, 18, num.ErrNoInverse}} {
		if _, err := num.ModInverse(c.a, c.m); !errors.Is(err, c.want) {
			t.Errorf("ModInverse(%d,%d): err=%v, want %v", c.a, c.m, err, c.want)
		}
	}
}

func TestDepthBound(t *testing.T) {
	for _, c := range [][2]int{{240, 46}, {1, 1}, {1e18, 999999999999999999}, {679891637638612258, 420196140727489673}} {
		depth, bound := check.RefDepth(c[0], c[1]), 2*bits.Len64(uint64(max(c[0], c[1])))+2
		if _, _, _, err := egcd.ExtendedGCD(c[0], c[1]); err != nil || depth > bound {
			t.Errorf("(%d,%d): depth=%d bound=%d err=%v", c[0], c[1], depth, bound, err)
		}
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if g, x, y, err := egcd.ExtendedGCD(a, 46); err != nil || a*x+46*y != g {
					t.Errorf("concurrent ExtendedGCD(%d,46) broken", a)
					return
				}
			}
		}(240 + i)
	}
	wg.Wait()
}
