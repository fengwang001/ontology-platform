package budget

import (
	"errors"
	"testing"
)

func TestCounterStepsAndLimit(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		ticks int
		want  bool
	}{
		{"unlimited never fails", 0, 1000, false},
		{"under limit", 5, 5, false},
		{"over limit fails", 3, 4, true},
		{"limit one first tick ok", 1, 1, false},
		{"limit one second tick fails", 1, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(tc.limit)
			var failed bool
			for i := 0; i < tc.ticks; i++ {
				if err := c.Tick(); err != nil {
					if !errors.Is(err, ErrBudget) {
						t.Fatalf("tick %d error %v is not ErrBudget", i, err)
					}
					failed = true
				}
			}
			if failed != tc.want {
				t.Fatalf("failed=%v want %v, steps=%d", failed, tc.want, c.Steps())
			}
			if c.Steps() != tc.ticks {
				t.Fatalf("steps=%d want %d", c.Steps(), tc.ticks)
			}
		})
	}
}

func TestNilCounter(t *testing.T) {
	var c *Counter
	for i := 0; i < 10; i++ {
		if err := c.Tick(); err != nil {
			t.Fatalf("nil Tick returned %v", err)
		}
	}
	if c.Steps() != 0 {
		t.Fatalf("nil Steps = %d, want 0", c.Steps())
	}
}
