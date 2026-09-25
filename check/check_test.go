package check_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/check"
	"ontology/egcd"
	"ontology/num"
)

// 第二节.1/.4：贝祖恒等式、g 非负、边界 (0,0)/(a,0)/负数/大数。
func TestExtendedGCD(t *testing.T) {
	cases := []struct{ a, b int }{
		{240, 46}, {0, 0}, {7, 0}, {0, -9}, {-12, 18}, {17, 5},
		{1_000_000_000_000_000_000, 999_999_999_999_999_993},
		{679_891_637_638_612_258, 420_196_140_727_489_673}, // 斐波那契对：最坏深度
	}
	for _, c := range cases {
		g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil {
			t.Fatalf("ExtendedGCD(%d, %d): %v", c.a, c.b, err)
		}
		if want := check.RefGCD(c.a, c.b); g != want || g < 0 {
			t.Errorf("ExtendedGCD(%d, %d) g = %d, want %d", c.a, c.b, g, want)
		}
		if !check.BezoutOK(c.a, c.b, x, y, g) {
			t.Errorf("ExtendedGCD(%d, %d): x=%d y=%d 不满足恒等式", c.a, c.b, x, y)
		}
	}
}

// 第二节.2/.3：逆元正确性；两类哨兵错误可用 errors.Is 区分。
func TestModInverse(t *testing.T) {
	cases := []struct {
		a, m    int
		wantErr error
	}{
		{3, 11, nil}, {10, 17, nil}, {1, 1, nil},
		{2, 4, num.ErrNoInverse}, {6, 9, num.ErrNoInverse}, {0, 7, num.ErrNoInverse},
		{-1, 5, num.ErrBadArg}, {3, 0, num.ErrBadArg}, {3, -7, num.ErrBadArg},
	}
	for _, c := range cases {
		inv, err := num.ModInverse(c.a, c.m)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("ModInverse(%d, %d) err = %v, want %v", c.a, c.m, err, c.wantErr)
		}
		if err == nil && (c.a*inv)%c.m != 1%c.m {
			t.Errorf("ModInverse(%d, %d) = %d: 不是逆元", c.a, c.m, inv)
		}
	}
}

// 第三节：内联「漏掉 (a/b)*y' 项」的错误回代，断言其破坏恒等式。
func TestWrongBackSubstitution(t *testing.T) {
	var wrong func(a, b int) (g, x, y int)
	wrong = func(a, b int) (g, x, y int) {
		if b == 0 {
			return a, 1, 0
		}
		g, x1, y1 := wrong(b, a%b)
		return g, y1, x1 // 错误：y 漏了 -(a/b)*y1
	}
	cases := []struct{ a, b int }{{240, 46}, {3, 11}}
	for _, c := range cases {
		g, x, y := wrong(c.a, c.b)
		if check.BezoutOK(c.a, c.b, x, y, g) {
			t.Errorf("wrong(%d, %d): 恒等式意外成立", c.a, c.b)
		}
		g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil || !check.BezoutOK(c.a, c.b, x, y, g) {
			t.Errorf("ExtendedGCD(%d, %d): 正确实现反而失败", c.a, c.b)
		}
	}
}

// 第二节.5 + 并发：纯函数并发调用结果确定且互不影响（-race）。
func TestConcurrentDeterministic(t *testing.T) {
	cases := []struct{ a, b int }{{240, 46}, {3, 11}, {679_891_637_638_612_258, 420_196_140_727_489_673}}
	var wg sync.WaitGroup
	for _, c := range cases {
		g0, x0, y0, err := egcd.ExtendedGCD(c.a, c.b)
		if err != nil {
			t.Fatalf("ExtendedGCD(%d, %d): %v", c.a, c.b, err)
		}
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				g, x, y, err := egcd.ExtendedGCD(c.a, c.b)
				if err != nil || g != g0 || x != x0 || y != y0 {
					t.Errorf("ExtendedGCD(%d, %d): 结果不确定", c.a, c.b)
				}
				_, _ = num.ModInverse(c.a, c.b)
			}()
		}
	}
	wg.Wait()
}
