package probe

import "testing"

// TestTouchBound 证明只有一段装载的单元，Evaluate 所触发的 probe 二分触碰数
// 在 100 条与 1e5 条读数两档下分别不超过 30 与 80。
func TestTouchBound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		n     int
		bound int64
	}{
		{"100 readings", 100, 30},
		{"1e5 readings", 100000, 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(80, 20, 3, 10)
			for i := 0; i < tc.n; i++ {
				temp := int64(50)
				if i%3 == 1 {
					temp = 90
				}
				if err := s.Reading("D", temp, int64(i*5)); err != nil {
					t.Fatalf("reading %d: %v", i, err)
				}
			}
			now := int64(tc.n*5 - 1)
			s.ResetTouch()
			if _, ok := s.Weight("D", 0, now); !ok {
				t.Fatal("weight not ok")
			}
			if got := s.TouchCount(); got > tc.bound {
				t.Fatalf("touched=%d exceeds bound %d", got, tc.bound)
			}
			t.Logf("%s: touched=%d bound=%d", tc.name, s.TouchCount(), tc.bound)
		})
	}
}

// TestReadingTouchesConstant 证明 Reading 本身触碰记录数为常数（0，无查找）。
func TestReadingTouchesConstant(t *testing.T) {
	s := New(80, 20, 3, 10)
	for i := 0; i < 1000; i++ {
		before := s.TouchCount()
		if err := s.Reading("D", 90, int64(i*5)); err != nil {
			t.Fatal(err)
		}
		if s.TouchCount() != before {
			t.Fatalf("reading touched %d records", s.TouchCount()-before)
		}
	}
}

// TestSegmentsBasic 阶梯分段的基本权重与缺口行为（同包可直接检视分段）。
func TestSegmentsBasic(t *testing.T) {
	s := New(80, 20, 3, 10)
	must := func(err error) {
		if err != nil {
			t.Helper()
			t.Fatal(err)
		}
	}
	must(s.Reading("D", 50, 0))
	must(s.Reading("D", 90, 10))
	must(s.Reading("D", 70, 20))
	must(s.Reading("D", 60, 45))
	cases := []struct {
		a, b, want int64
	}{
		{5, 10, 0},   // 正常
		{10, 20, 10}, // 轻度
		{20, 30, 0},  // 正常覆盖
		{30, 36, 18}, // 缺口重度 6*3
		{30, 45, 45}, // 整段缺口 15*3
	}
	for _, c := range cases {
		got, ok := s.Weight("D", c.a, c.b)
		if !ok || got != c.want {
			t.Fatalf("Weight(%d,%d)=%d,%v want %d", c.a, c.b, got, ok, c.want)
		}
	}
}
