package tricolor

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// naive is a reference implementation that refills one millisecond at a
// time. Batch refill in Marker must agree with it step by step.
type naive struct {
	p                Params
	tc, te           int64
	last             int64
	until, lastUntil int64
	s                int
	redQ             []int64
	enteredTe        int64 // tokens actually accepted into Te (excl. dropped)
}

func newNaive(p Params) *naive {
	return &naive{p: p, tc: p.CBS, te: p.EBS}
}

func (n *naive) refill(now int64) {
	for ; n.last < now; n.last++ {
		n.tc += n.p.CIR
		if n.tc > n.p.CBS {
			o := n.tc - n.p.CBS
			n.tc = n.p.CBS
			before := n.te
			if n.te += o; n.te > n.p.EBS {
				n.te = n.p.EBS
			}
			n.enteredTe += n.te - before
		}
	}
}

// mark mirrors Marker.Mark and also reports the decision reason.
func (n *naive) mark(now int64, in Input, b int64) (Color, bool, string, error) {
	if err := validateNow(now); err != nil {
		return Red, false, "", err
	}
	if in < InGreen || in > InBlind {
		return Red, false, "", fmt.Errorf("%w: color %d", ErrInvalidParam, int(in))
	}
	if b < 1 || b > maxB {
		return Red, false, "", fmt.Errorf("%w: b %d", ErrInvalidParam, b)
	}
	if now < n.last {
		return Red, false, "", fmt.Errorf("%w: now %d < last %d", ErrClockBackward, now, n.last)
	}

	n.refill(now)

	if now < n.until {
		return Red, true, "penalty window: now<until, no tokens, no record", nil
	}

	var out Color
	var reason string
	switch in {
	case InGreen, InBlind:
		switch {
		case n.tc >= b:
			n.tc -= b
			out, reason = Green, "Tc>=b: Green"
		case n.te >= b:
			n.te -= b
			out, reason = Yellow, "Tc<b<=Te: Yellow (degraded, excess bucket)"
		default:
			out, reason = Red, "Tc<b and Te<b: Red (rate-red, recorded)"
		}
	case InYellow:
		if n.te >= b {
			n.te -= b
			out, reason = Yellow, "Te>=b: Yellow"
		} else {
			out, reason = Red, "Te<b (Tc untouched): Red (rate-red, recorded)"
		}
	case InRed:
		out, reason = Red, "input Red: Red (no tokens, no record)"
	}

	if out == Red && in != InRed {
		for len(n.redQ) > 0 && n.redQ[0]+n.p.W <= now {
			n.redQ = n.redQ[1:]
		}
		n.redQ = append(n.redQ, now)
		if int64(len(n.redQ)) >= n.p.K {
			n.redQ = n.redQ[:0]
			if n.lastUntil > 0 && now-n.lastUntil < n.p.W {
				if n.s < maxS {
					n.s++
				}
			} else {
				n.s = 0
			}
			n.until = now + n.p.Pn<<n.s
			n.lastUntil = n.until
			reason += fmt.Sprintf("; K reached: penalty s=%d until=%d", n.s, n.until)
		}
	}
	return out, false, reason, nil
}

func (n *naive) reconfigure(now int64, cir, cbs, ebs int64) error {
	p := n.p
	p.CIR, p.CBS, p.EBS = cir, cbs, ebs
	if err := validateParams(p); err != nil {
		return err
	}
	if err := validateNow(now); err != nil {
		return err
	}
	if now < n.last {
		return fmt.Errorf("%w: now %d < last %d", ErrClockBackward, now, n.last)
	}
	n.refill(now)
	if n.tc > cbs {
		n.tc = cbs
	}
	if n.te > ebs {
		n.te = ebs
	}
	n.p = p
	return nil
}

func sameErrClass(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	if errors.Is(a, ErrInvalidParam) || errors.Is(b, ErrInvalidParam) {
		return errors.Is(a, ErrInvalidParam) && errors.Is(b, ErrInvalidParam)
	}
	return errors.Is(a, ErrClockBackward) && errors.Is(b, ErrClockBackward)
}

func compareState(t *testing.T, seq, op int, m *Marker, n *naive) {
	t.Helper()
	s := m.Snapshot()
	q := s.RedQueue
	if len(q) == 0 && len(n.redQ) == 0 {
		q, n.redQ = nil, nil
	}
	if s.Tc != n.tc || s.Te != n.te || s.Last != n.last ||
		s.Until != n.until || s.LastUntil != n.lastUntil || s.S != n.s ||
		fmt.Sprint(q) != fmt.Sprint(n.redQ) {
		t.Fatalf("seq %d op %d: state diverge\n marker: %+v queue=%v\n naive:  %+v queue=%v",
			seq, op, s, q, n, n.redQ)
	}
	if s.Tc < 0 || s.Tc > s.CBS || s.Te < 0 || s.Te > s.EBS {
		t.Fatalf("seq %d op %d: bucket invariant violated: %+v", seq, op, s)
	}
}

// 2000 random operation sequences are replayed against the per-millisecond
// naive simulation; every step logs input, output and the decision reason.
func TestNaiveSimulationComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(1225))

	for seq := 0; seq < 2000; seq++ {
		p := Params{
			CIR: 1 + rng.Int63n(1000),
			CBS: 1 + rng.Int63n(500),
			EBS: rng.Int63n(301),
			W:   1 + rng.Int63n(200),
			K:   1 + rng.Int63n(5),
			Pn:  1 + rng.Int63n(100),
		}
		m := mustNew(t, p)
		n := newNaive(p)

		var now int64
		var greenYellowBytes, yellowBytes int64
		reconfigured := false

		ops := 20 + rng.Intn(21)
		for op := 0; op < ops; op++ {
			now += rng.Int63n(5)
			switch r := rng.Intn(100); {
			case r < 75: // Mark
				in := Input(rng.Intn(4))
				b := int64(1 + rng.Intn(600))
				if rng.Intn(20) == 0 { // invalid call, must be rejected by both
					b = 0
				}
				if rng.Intn(40) == 0 && now > 0 { // clock regression
					now--
				}
				c1, p1, e1 := m.Mark(now, in, b)
				c2, p2, reason, e2 := n.mark(now, in, b)
				if !sameErrClass(e1, e2) {
					t.Fatalf("seq %d op %d: err %v vs %v", seq, op, e1, e2)
				}
				if e1 == nil {
					if c1 != c2 || p1 != p2 {
						t.Fatalf("seq %d op %d: Mark(%d,%v,%d) = (%v,%v) vs naive (%v,%v)",
							seq, op, now, in, b, c1, p1, c2, p2)
					}
					t.Logf("seq %d op %d: Mark(now=%d, in=%v, b=%d) -> %v penalty=%v [%s]",
						seq, op, now, in, b, c1, p1, reason)
					if c1 == Green || c1 == Yellow {
						greenYellowBytes += b
					}
					if c1 == Yellow {
						yellowBytes += b
					}
				} else {
					t.Logf("seq %d op %d: Mark(now=%d, in=%v, b=%d) rejected: %v",
						seq, op, now, in, b, e1)
				}
			case r < 85: // Reconfigure
				cir, cbs, ebs := 1+rng.Int63n(1000), 1+rng.Int63n(500), rng.Int63n(301)
				e1 := m.Reconfigure(now, cir, cbs, ebs)
				e2 := n.reconfigure(now, cir, cbs, ebs)
				if !sameErrClass(e1, e2) {
					t.Fatalf("seq %d op %d: reconfigure err %v vs %v", seq, op, e1, e2)
				}
				if e1 == nil {
					reconfigured = true
					t.Logf("seq %d op %d: Reconfigure(now=%d, CIR=%d, CBS=%d, EBS=%d)",
						seq, op, now, cir, cbs, ebs)
				}
			default: // read-only queries
				_ = m.Snapshot()
				_ = m.Stats()
			}
			compareState(t, seq, op, m, n)
		}

		// Conservation laws on sequences that never reconfigured.
		if !reconfigured {
			s := m.Snapshot()
			if bound := s.CBS + s.EBS + s.CIR*s.Last; greenYellowBytes > bound {
				t.Fatalf("seq %d: green+yellow bytes %d > CBS+EBS+CIR*last=%d",
					seq, greenYellowBytes, bound)
			}
			if bound := s.EBS + n.enteredTe; yellowBytes > bound {
				t.Fatalf("seq %d: yellow bytes %d > EBS+enteredTe=%d",
					seq, yellowBytes, bound)
			}
		}
	}
}
