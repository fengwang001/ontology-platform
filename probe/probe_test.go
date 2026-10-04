package probe

import (
	"errors"
	"testing"
)

func mustStore(t *testing.T, cfg Config) *Store {
	t.Helper()
	s, err := NewStore(&Clock{}, cfg)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// 阶梯读数、缺口边界（恰等 G / 大 1）、温度边界（恰等 Hi / Hi+Δ）、最后一条读数后的缺口。
func TestWeightBoundaries(t *testing.T) {
	// Hi=80, Delta=20, W=3, G=10
	// t=0: 80（恰等 Hi，正常）；t=10: 100（恰等 Hi+Δ，轻度）；t=20: 101（重度）
	cfg := Config{Hi: 80, Delta: 20, W: 3, G: 10}
	cases := []struct {
		name string
		x    int64
		want int64
	}{
		{"起点", 0, 0},
		{"恰等Hi正常", 10, 0},
		{"恰等Hi+Δ轻度_间隔恰等G无缺口", 15, 5},
		{"轻度覆盖满G", 20, 10},
		{"重度读数", 25, 10 + 15},
		{"最后读数覆盖端", 30, 10 + 30},
		{"最后读数后缺口1分钟", 31, 10 + 30 + 3},
		{"最后读数后缺口持续", 45, 10 + 30 + 45},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustStore(t, cfg)
			for _, r := range []struct{ temp, now int64 }{{80, 0}, {100, 10}, {101, 20}} {
				if err := s.Reading("D", r.temp, r.now); err != nil {
					t.Fatalf("Reading: %v", err)
				}
			}
			if got := s.Weight("D", tc.x); got != tc.want {
				t.Fatalf("Weight(D, %d) = %d, want %d", tc.x, got, tc.want)
			}
		})
	}
}

// 间隔比 G 大 1 时缺口恰为 1 分钟。
func TestWeightGapOneOver(t *testing.T) {
	cfg := Config{Hi: 80, Delta: 20, W: 3, G: 10}
	s := mustStore(t, cfg)
	if err := s.Reading("D", 50, 0); err != nil { // 正常档
		t.Fatal(err)
	}
	if err := s.Reading("D", 50, 11); err != nil { // 间隔 11 = G+1
		t.Fatal(err)
	}
	// [0,10) 正常，[10,11) 缺口重度 3，之后正常
	if got := s.Weight("D", 11); got != 3 {
		t.Fatalf("Weight(D, 11) = %d, want 3", got)
	}
	if got := s.Weight("D", 20); got != 3 {
		t.Fatalf("Weight(D, 20) = %d, want 3", got)
	}
}

func TestReadingRejections(t *testing.T) {
	cfg := Config{Hi: 80, Delta: 20, W: 3, G: 10}
	s := mustStore(t, cfg)
	if err := s.Reading("", 50, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty device: %v", err)
	}
	if err := s.Reading("D", MinTemp-1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("temp low: %v", err)
	}
	if err := s.Reading("D", 50, MaxNow+1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("now high: %v", err)
	}
	if err := s.Reading("D", 50, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Reading("D", 60, 10); !errors.Is(err, ErrState) {
		t.Fatalf("equal time: %v", err)
	}
	if err := s.Reading("D", 60, 9); !errors.Is(err, ErrClockBack) {
		t.Fatalf("clock back: %v", err)
	}
	// 被拒绝的操作不推进时钟：t=10 仍因相等报状态不符而非时钟回退。
	if err := s.Reading("D", 60, 10); !errors.Is(err, ErrState) {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
	if err := s.Reading("D", 60, 11); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidate(t *testing.T) {
	if _, err := NewStore(&Clock{}, Config{Hi: 80, Delta: 0, W: 3, G: 10}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Delta=0: %v", err)
	}
	if _, err := NewStore(&Clock{}, Config{Hi: 80, Delta: 20, W: 1, G: 10}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("W=1: %v", err)
	}
	if _, err := NewStore(&Clock{}, Config{Hi: 80, Delta: 20, W: 3, G: 0}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("G=0: %v", err)
	}
}

// touched 证明：模拟只有一段装载的单元的 Evaluate 访问模式
// （Weight 起点 + Weight 终点 + CrossAfter），记录数 100 与 1e5 两档
// 分别不超过 30 与 80；Reading 本身触碰常数条记录。
func TestTouchedBounds(t *testing.T) {
	cfg := Config{Hi: 80, Delta: 20, W: 3, G: 10}
	for _, tc := range []struct {
		n     int
		bound int64
	}{{100, 30}, {100000, 80}} {
		s := mustStore(t, cfg)
		for i := 0; i < tc.n; i++ {
			if err := s.Reading("D", 90, int64(2*i)); err != nil { // 轻度，每分钟 1
				t.Fatal(err)
			}
		}
		end := int64(2 * tc.n)
		s.touched = 0
		wa := s.Weight("D", 0)
		we := s.Weight("D", end)
		if _, ok := s.CrossAfter("D", end, (wa+we)/2); !ok {
			t.Fatalf("n=%d: CrossAfter not found", tc.n)
		}
		if s.touched > tc.bound {
			t.Fatalf("n=%d: touched=%d exceeds bound %d", tc.n, s.touched, tc.bound)
		}
		t.Logf("n=%d: Evaluate 模式触碰读数记录 %d 条（上界 %d）", tc.n, s.touched, tc.bound)

		// Reading 触碰常数条记录，与已有记录数无关。
		s.touched = 0
		if err := s.Reading("D", 90, end); err != nil {
			t.Fatal(err)
		}
		if s.touched > 3 {
			t.Fatalf("n=%d: Reading touched=%d, want constant", tc.n, s.touched)
		}
	}
}
