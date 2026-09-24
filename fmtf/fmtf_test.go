package fmtf

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{0.1, "0.1"},
		{1.0 / 3.0, "0.3333333333333333"},
		{0.0, "0"},
		{math.Copysign(0, -1), "-0"},
		{1.0, "1"},
		{-2.5, "-2.5"},
		{100, "100"},
		{math.MaxFloat64, "1.7976931348623157e+308"},
		{math.SmallestNonzeroFloat64, "5e-324"},
		{1 << 53, "9007199254740992"},
		{1<<53 - 1, "9007199254740991"},
		{1<<53 + 2, "9007199254740994"},
	}
	for _, c := range cases {
		got, err := Format(c.x)
		if err != nil {
			t.Fatalf("Format(%v): %v", c.x, err)
		}
		if got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

// TestExpBoundary 断言指数形式边界（E=16/17 与 E=-4/-5）两侧各两值的形式。
func TestExpBoundary(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{1e16, "10000000000000000"},   // E=16 定点
		{1.5e16, "15000000000000000"}, // E=16 定点
		{1e17, "1e+17"},               // E=17 指数
		{1.5e17, "1.5e+17"},           // E=17 指数
		{1e-4, "0.0001"},              // E=-4 定点
		{1.5e-4, "0.00015"},           // E=-4 定点
		{1e-5, "1e-05"},               // E=-5 指数
		{1.5e-5, "1.5e-05"},           // E=-5 指数
	}
	for _, c := range cases {
		got, err := Format(c.x)
		if err != nil {
			t.Fatalf("Format(%v): %v", c.x, err)
		}
		if got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestNonFinite(t *testing.T) {
	for _, x := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Format(x); !errors.Is(err, ErrNonFinite) {
			t.Errorf("Format(%v) 错误 = %v，应可判定为 ErrNonFinite", x, err)
		}
	}
}

func TestDeterministic(t *testing.T) {
	for _, x := range []float64{0.1, math.MaxFloat64, math.SmallestNonzeroFloat64, 1.0 / 3.0} {
		first, err := Format(x)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 1000; i++ {
			got, _ := Format(x)
			if got != first {
				t.Fatalf("Format(%v) 第 %d 次结果 %q 与首次 %q 不同", x, i, got, first)
			}
		}
	}
}

func TestConcurrent(t *testing.T) {
	vals := []float64{0.1, -2.5, 1e17, 1e-5, math.MaxFloat64, math.SmallestNonzeroFloat64}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				x := vals[(g+i)%len(vals)]
				s, err := Format(x)
				if err != nil {
					t.Error(err)
					return
				}
				again, _ := Format(x)
				if s != again {
					t.Errorf("并发下 Format(%v) 结果不稳定", x)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
