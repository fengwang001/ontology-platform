package labrule

import (
	"errors"
	"testing"
)

func TestCriticalAndSeverity(t *testing.T) {
	// 血钾按 0.1 计：low=25, high=65, step=5
	r := NewRules()
	if err := r.AddTest("K", 25, 65, 5); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		v    int64
		crit bool
		sev  int
	}{
		{"normal-mid", 40, false, 0},
		{"normal-near-high", 64, false, 0},
		{"normal-near-low", 26, false, 0},
		{"high-equal-is-critical", 65, true, 1}, // 取等即危急，x=0 -> sev1
		{"low-equal-is-critical", 25, true, 1},
		{"66 sev1", 66, true, 1}, // x=1, floor(1/5)=0
		{"69 sev1", 69, true, 1}, // x=4
		{"70 sev2", 70, true, 2}, // x=5
		{"74 sev2", 74, true, 2}, // x=9
		{"75 sev3", 75, true, 3}, // x=10
		{"24 sev1", 24, true, 1}, // x=1
		{"20 sev2", 20, true, 2}, // x=5
		{"15 sev3", 15, true, 3}, // x=10
		{"0 sev3", 0, true, 3},   // x=25 封顶 3
		{"1000 sev3", 1000, true, 3},
		{"neg", -1000, true, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sev, crit := r.Severity("K", c.v)
			if crit != c.crit || sev != c.sev {
				t.Fatalf("v=%d got crit=%v sev=%d, want crit=%v sev=%d", c.v, crit, sev, c.crit, c.sev)
			}
		})
	}
}

func TestAddTestValidation(t *testing.T) {
	cases := []struct {
		name      string
		code      string
		low, high int64
		step      int64
		want      error
	}{
		{"ok", "A", 0, 10, 1, nil},
		{"empty code", "", 0, 10, 1, ErrInvalid},
		{"low==high", "B", 5, 5, 1, ErrInvalid},
		{"low>high", "C", 9, 8, 1, ErrInvalid},
		{"step 0", "D", 0, 10, 0, ErrInvalid},
		{"step too big", "E", 0, 10, 1_000_001, ErrInvalid},
		{"duplicate", "A", 0, 10, 1, ErrDuplicateTest},
	}
	r := NewRules()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := r.AddTest(c.code, c.low, c.high, c.step)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
	if r.Critical("missing", 0) {
		t.Fatal("unknown code must not be critical")
	}
}
