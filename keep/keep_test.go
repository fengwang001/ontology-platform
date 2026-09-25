package keep

import (
	"math/rand"
	"testing"
)

func TestNewInvalid(t *testing.T) {
	for _, c := range [][3]int64{{0, 1, 1}, {-1, 1, 1}, {1, 0, 1}, {1, -2, 1}, {1, 1, 0}, {1, 1, -3}} {
		if s, err := New(c[0], c[1], c[2]); err != ErrInvalidParam || s != nil {
			t.Fatalf("New%v = %v,%v", c, s, err)
		}
	}
}

// TestTickInspectionBound 连续发出 m 次探测，每次 Tick 检查记录数 <= 1（O(1)）。
func TestTickInspectionBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		st, _ := New(1, 1, int64(m)+1)
		_ = st.Activity(0)
		for at := int64(1); at <= int64(m); at++ {
			sent, became, err := st.Tick(at)
			if err != nil || !sent || became || st.inspected > 1 {
				t.Fatalf("m=%d at=%d: sent=%v became=%v inspected=%d err=%v",
					m, at, sent, became, st.inspected, err)
			}
		}
		if st.probes != m {
			t.Fatalf("m=%d probes=%d", m, st.probes)
		}
	}
}

// TestRejectedNoTrace 同包核验被拒操作不改动任何字段，之后仍可正常使用。
func TestRejectedNoTrace(t *testing.T) {
	st, _ := New(100, 30, 3)
	_ = st.Activity(0)
	for _, at := range []int64{100, 130, 160, 190} {
		_, _, _ = st.Tick(at)
	}
	if !st.dead || st.probes != 3 {
		t.Fatal("setup not dead")
	}
	if err := st.Activity(191); err != ErrDead || st.probes != 3 || st.lastActive != 0 {
		t.Fatalf("dead activity left trace: %v", err)
	}
	if err := st.Activity(189); err != ErrClockBack {
		t.Fatalf("back after dead: %v", err)
	}
	s2, _ := New(100, 30, 3)
	_ = s2.Activity(10)
	_, _, _ = s2.Tick(50)
	before := *s2
	if _, _, err := s2.Tick(49); err != ErrClockBack {
		t.Fatalf("tick back: %v", err)
	}
	if err := s2.Activity(48); err != ErrClockBack {
		t.Fatalf("act back: %v", err)
	}
	if *s2 != before {
		t.Fatal("clock-back left a trace")
	}
	if _, _, err := s2.Tick(110); err != nil || s2.probes != 1 {
		t.Fatalf("use after reject: %v p=%d", err, s2.probes)
	}
}

// shadow 是与规格同规则、独立手写的朴素参照，逐步与 State 比对。
type shadow struct {
	la, lp, now  int64
	p            int
	dead, seen   bool
	idle, iv, mx int64
}

func (s *shadow) act(t int64) error {
	if s.seen && t < s.now {
		return ErrClockBack
	}
	s.seen, s.now = true, t
	if s.dead {
		return ErrDead
	}
	s.la, s.p = t, 0
	return nil
}

func (s *shadow) tick(t int64) (bool, bool, error) {
	if s.seen && t < s.now {
		return false, false, ErrClockBack
	}
	s.seen, s.now = true, t
	if s.dead || t-s.la < s.idle {
		return false, false, nil
	}
	switch {
	case s.p == 0:
		s.p, s.lp = 1, t
		return true, false, nil
	case int64(s.p) < s.mx && t-s.lp >= s.iv:
		s.p, s.lp = s.p+1, t
		return true, false, nil
	case int64(s.p) == s.mx && t-s.lp >= s.iv:
		s.dead = true
		return false, true, nil
	}
	return false, false, nil
}

// TestNaiveReference 多档参数 + 随机活动/空闲单调交错，状态与返回值逐拍一致。
func TestNaiveReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	params := [][3]int64{{100, 30, 3}, {1, 1, 1}, {5, 10, 2}, {50, 7, 5}}
	for trial := 0; trial < 60; trial++ {
		p := params[trial%len(params)]
		st, _ := New(p[0], p[1], p[2])
		sh := &shadow{idle: p[0], iv: p[1], mx: p[2]}
		clock := int64(rng.Intn(5))
		for i := 0; i < 120; i++ {
			clock += int64(rng.Intn(40))
			isAct := rng.Intn(10) < 3
			var s1, s2, b1, b2 bool
			var e1, e2 error
			if isAct {
				e1, e2 = st.Activity(clock), sh.act(clock)
			} else {
				s1, b1, e1 = st.Tick(clock)
				s2, b2, e2 = sh.tick(clock)
			}
			if e1 != e2 || s1 != s2 || b1 != b2 ||
				st.probes != sh.p || st.dead != sh.dead || st.lastActive != sh.la {
				t.Fatalf("trial %d op %d act=%v t=%d: impl(%v,%v,p=%d,d=%v,la=%d,%v) ref(%v,%v,%d,%v,%d,%v)",
					trial, i, isAct, clock, s1, b1, st.probes, st.dead, st.lastActive, e1,
					s2, b2, sh.p, sh.dead, sh.la, e2)
			}
			if st.dead {
				break
			}
		}
	}
}
