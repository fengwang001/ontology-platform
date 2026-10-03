package gcra

import "testing"

func TestPossible(t *testing.T) {
	if !Possible(2, 2) {
		t.Error("cost==B should be possible")
	}
	if Possible(3, 2) {
		t.Error("cost>B should be impossible")
	}
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		name       string
		tat        int64
		hasTAT     bool
		now        int64
		cost, tt   int64
		bb         int64
		allowed    bool
		limit      int64
		remaining  int64
		reset      int64
		retryAfter int64
		newTAT     int64
	}{
		{"first call no TAT", 0, false, 0, 1, 1000, 2, true, 2, 1, 1, 0, 1000},
		{"second same now", 1000, true, 0, 1, 1000, 2, true, 2, 0, 2, 0, 2000},
		{"limited at burst boundary+1", 2000, true, 0, 1, 1000, 2, false, 2, 0, 2, 1, 3000},
		{"new-now exactly B*T allowed", 9, true, 0, 1, 1, 10, true, 10, 0, 1, 0, 10},
		{"new-now B*T+1 limited", 10, true, 0, 1, 1, 10, false, 10, 0, 1, 1, 11},
		{"cost==B allowed", 0, false, 0, 10, 1, 10, true, 10, 0, 1, 0, 10},
		{"idle TAT<now uses now", 1000, true, 5000, 1, 1000, 2, true, 2, 1, 1, 0, 6000},
		{"remaining floor", 150, true, 60, 1, 100, 5, true, 5, 3, 1, 0, 250},
		{"reset exact second no round up", 1000, true, 0, 1, 1000, 2, true, 2, 0, 2, 0, 2000},
		{"reset rounds up", 150, true, 60, 1, 100, 5, true, 5, 3, 1, 0, 250},
		{"retry-after exact second", 2000, true, 0, 1, 1000, 2, false, 2, 0, 2, 1, 3000},
		{"retry-after rounds up", 2000, true, 0, 1, 100, 5, false, 5, 0, 2, 2, 2100},
		{"remaining clamped at zero", 2000, true, 0, 1, 100, 5, false, 5, 0, 2, 2, 2100},
		{"debt kept after tier change", 2000, true, 1600, 1, 100, 5, true, 5, 0, 1, 0, 2100},
		{"large values no overflow", 1_000_000_000_000_000, true, 1_000_000_000_000_000, 1_000_000, 1_000_000, 1_000_000, true, 1_000_000, 0, 1_000_000_000, 0, 1_001_000_000_000_000},
	}
	for _, c := range cases {
		d := Evaluate(c.tat, c.hasTAT, c.now, c.cost, c.tt, c.bb)
		got := Decision{
			Allowed: d.Allowed, Limit: d.Limit, Remaining: d.Remaining,
			Reset: d.Reset, RetryAfter: d.RetryAfter, NewTAT: d.NewTAT,
		}
		want := Decision{
			Allowed: c.allowed, Limit: c.limit, Remaining: c.remaining,
			Reset: c.reset, RetryAfter: c.retryAfter, NewTAT: c.newTAT,
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, want)
		}
	}
}
