package check

import (
	"errors"
	"math"
	"sync"
	"testing"

	"ontology/egcd"
	"ontology/num"
)

var gcdCases = []struct{ a, b, g int }{
	{240, 46, 2},
	{0, 0, 0},
	{7, 0, 7},
	{0, -9, 9},
	{-12, 18, 6},
	{-8, -36, 4},
	{17, 5, 1},
	{1, 1, 1},
	{549755813888, 1099511627776, 549755813888},
	{1000000007, 123456789, 1},
	{679891637638612258, 420196140727489673, 1}, // adjacent Fibonacci, worst case
	{1000000000000000000, 999999999999999999, 1},
}

func TestExtendedGCDTable(t *testing.T) {
	for _, c := range gcdCases {
		g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil || g != c.g || g < 0 || c.a*x+c.b*y != g || g != BigGCD(c.a, c.b) {
			t.Errorf("egcd(%d,%d) = g=%d x=%d y=%d err=%v", c.a, c.b, g, x, y, err)
		}
		if max(abs(c.a), abs(c.b)) <= 1_000_000 && g != NaiveGCD(c.a, c.b) {
			t.Errorf("egcd(%d,%d) g=%d, naive=%d", c.a, c.b, g, NaiveGCD(c.a, c.b))
		}
	}
}

func TestBezout240_46Pinned(t *testing.T) {
	g, x, y, _ := egcd.ExtendedGCD(240, 46)
	if g != 2 || 240*x+46*y != 2 {
		t.Fatalf("g=%d x=%d y=%d, want g=2 and 240x+46y=2", g, x, y)
	}
}

// buggyEGCD drops the (a/b) term during back-substitution: the incident bug.
func buggyEGCD(a, b int) (g, x, y int) {
	if b == 0 {
		return a, 1, 0
	}
	g, x1, y1 := buggyEGCD(b, a%b)
	return g, y1, x1 // BUG: correct is x = y1, y = x1-(a/b)*y1
}

func TestBuggyBackSubstitutionFails(t *testing.T) {
	for _, c := range [][2]int{{240, 46}, {3, 11}} { // {3,11}: coprime, wrong inverse
		g, x, y := buggyEGCD(c[0], c[1])
		if c[0]*x+c[1]*y == g {
			t.Errorf("buggy egcd(%d,%d) accidentally satisfied the identity", c[0], c[1])
		}
		if !VerifyBezout(c[0], c[1]) {
			t.Errorf("correct egcd failed VerifyBezout(%d,%d)", c[0], c[1])
		}
	}
}

func TestModInverse(t *testing.T) {
	for _, c := range [][2]int{{3, 11}, {10, 17}, {1, 2}, {123456789, 1000000007}, {7, 1000000000000000000}} {
		inv, err := num.ModInverse(c[0], c[1])
		if err != nil || inv < 0 || inv >= c[1] || c[0]*inv%c[1] != 1%c[1] {
			t.Errorf("ModInverse(%d,%d) = %d, %v", c[0], c[1], inv, err)
		}
	}
	bad := []struct {
		a, m int
		want error
	}{
		{-1, 5, num.ErrBadArg},
		{3, 0, num.ErrBadArg},
		{3, -7, num.ErrBadArg},
		{2, 4, num.ErrNoInverse},
		{0, 5, num.ErrNoInverse},
		{6, 9, num.ErrNoInverse},
	}
	for _, c := range bad {
		if _, err := num.ModInverse(c.a, c.m); !errors.Is(err, c.want) {
			t.Errorf("ModInverse(%d,%d) err = %v, want %v", c.a, c.m, err, c.want)
		}
	}
}

func TestRecursionDepthBound(t *testing.T) {
	for _, c := range gcdCases {
		bound := 1
		if m := max(abs(c.a), abs(c.b)); m > 0 {
			bound = 2*int(math.Log2(float64(m))) + 2
		}
		if d := egcd.RecursionDepth(c.a, c.b); d > bound {
			t.Errorf("depth(%d,%d) = %d > bound %d", c.a, c.b, d, bound)
		}
	}
}

func TestConcurrentDeterministic(t *testing.T) {
	g0, x0, y0, _ := egcd.ExtendedGCD(240, 46)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if g, x, y, _ := egcd.ExtendedGCD(240+2*i, 46); (240+2*i)*x+46*y != g {
				t.Errorf("egcd(%d,46) broke the identity", 240+2*i)
			}
			if g, x, y, _ := egcd.ExtendedGCD(240, 46); g != g0 || x != x0 || y != y0 {
				t.Errorf("egcd(240,46) not deterministic under concurrency")
			}
			if _, err := num.ModInverse(3, 11); err != nil {
				t.Errorf("concurrent ModInverse: %v", err)
			}
		}(i)
	}
	wg.Wait()
}
