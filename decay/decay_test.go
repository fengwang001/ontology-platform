package decay

import "testing"

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name                  string
		delta, num, den, pmax int64
		ok                    bool
	}{
		{"ok", 10, 3, 4, 6000, true},
		{"delta zero", 0, 3, 4, 6000, false},
		{"delta too big", 1_000_001, 3, 4, 6000, false},
		{"num zero", 10, 0, 4, 6000, false},
		{"num equals den", 10, 4, 4, 6000, false},
		{"num greater den", 10, 5, 4, 6000, false},
		{"den over 1000", 10, 1, 1001, 6000, false},
		{"pmax zero", 10, 3, 4, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.delta, c.num, c.den, c.pmax)
			if c.ok && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestSettle(t *testing.T) {
	d, _ := New(10, 3, 4, 6000)
	// Worked example: p=1750, last=15, settle to 26 -> one step, last=25.
	p, last := d.Settle(1750, 15, 26)
	if p != 1312 || last != 25 {
		t.Fatalf("got p=%d last=%d, want 1312/25; basis: k=floor(11/10)=1, last+=k*delta", p, last)
	}
	// Phase alignment: advancing to 29 changes nothing (still phase 25).
	p, last = d.Settle(1750, 15, 29)
	if p != 1312 || last != 25 {
		t.Fatalf("got p=%d last=%d, want 1312/25", p, last)
	}
	// Stepwise floor differs from a single rounded multiplication.
	// p=13: stepwise is 13->floor(39/4)=9->floor(27/4)=6, whereas one-shot
	// floor(13*9/16)=7; k steps must not be collapsed into one multiplication.
	p2, _ := d.Settle(13, 0, 20)
	oneShot := int64(13 * 3 * 3 / 16)
	if oneShot != 7 {
		t.Fatalf("test premise broken: oneShot=%d", oneShot)
	}
	if p2 != 6 {
		t.Fatalf("stepwise p=%d want 6; one-shot gives %d (must not collapse)", p2, oneShot)
	}
}

func TestSettleZeroStops(t *testing.T) {
	d, _ := New(10, 3, 4, 6000)
	// Idle 10^3 steps versus 10^9 steps: both p=0 and bounded mulSteps.
	_, _ = d.Settle(6000, 0, 10_000)
	if got := d.MulSteps(); got > d.Z()+1 {
		t.Fatalf("10^3 idle: mulSteps %d > Z+1 %d", got, d.Z()+1)
	}
	_, _ = d.Settle(6000, 0, 10_000_000_000)
	if got := d.MulSteps(); got > d.Z()+1 {
		t.Fatalf("10^9 idle: mulSteps %d > Z+1 %d", got, d.Z()+1)
	}
}

func TestStepsToBelowBoundaries(t *testing.T) {
	d, _ := New(10, 3, 4, 6000)
	// From p=2312: sequence 1734,1300,975,731; below 800 only on step 4.
	if j := d.StepsToBelow(2312, 800); j != 4 {
		t.Fatalf("j=%d want 4", j)
	}
	// p already below pr: j must still be the smallest POSITIVE integer.
	if j := d.StepsToBelow(100, 800); j != 1 {
		t.Fatalf("j=%d want 1 (positive minimum)", j)
	}
	if d.MulSteps() > d.Z() {
		t.Fatalf("mulSteps %d exceeds Z %d", d.MulSteps(), d.Z())
	}
}
