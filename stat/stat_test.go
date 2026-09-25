package stat_test

import (
	"math"
	"testing"
	"time"

	"ontology/stat"
)

func TestSharesAndDeviation(t *testing.T) {
	cases := []struct {
		name     string
		executed map[string]int64
		weights  map[string]float64
		wantDev  map[string]float64
	}{
		{"exact", map[string]int64{"a": 10, "b": 30, "c": 60},
			map[string]float64{"a": 1, "b": 3, "c": 6},
			map[string]float64{"a": 0, "b": 0, "c": 0}},
		{"off by 4%", map[string]int64{"a": 104, "b": 296, "c": 600},
			map[string]float64{"a": 1, "b": 3, "c": 6},
			map[string]float64{"a": 0.04, "b": 0.013333, "c": 0}},
		{"missing tenant", map[string]int64{"b": 50, "c": 50},
			map[string]float64{"a": 1, "b": 1, "c": 1},
			map[string]float64{"a": 1, "b": 0.5, "c": 0.5}},
		{"empty", map[string]int64{}, map[string]float64{}, map[string]float64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dev := stat.RelDeviation(stat.Shares(tc.executed), stat.Expected(tc.weights))
			if len(dev) != len(tc.wantDev) {
				t.Fatalf("dev = %v, want %v", dev, tc.wantDev)
			}
			for k, want := range tc.wantDev {
				if math.Abs(dev[k]-want) > 1e-4 {
					t.Errorf("dev[%s] = %.5f, want %.5f", k, dev[k], want)
				}
			}
		})
	}
}

func TestTrackerClock(t *testing.T) {
	now := time.Unix(1000, 0)
	tr := stat.NewTracker(func() time.Time { return now })
	tr.Record("a", 5)
	now = now.Add(time.Minute)
	tr.Record("a", 7)
	tr.Record("b", 3)
	exec := tr.Executed()
	if exec["a"] != 12 || exec["b"] != 3 {
		t.Fatalf("executed = %v", exec)
	}
	if got := tr.LastServed("a"); !got.Equal(time.Unix(1060, 0)) {
		t.Fatalf("last served a = %v, want injected clock time", got)
	}
	if got := tr.LastServed("b"); !got.Equal(time.Unix(1060, 0)) {
		t.Fatalf("last served b = %v, want injected clock time", got)
	}
	exec["a"] = 999 // mutating the copy must not affect the tracker
	if tr.Executed()["a"] != 12 {
		t.Fatal("Executed leaks internal state")
	}
}
