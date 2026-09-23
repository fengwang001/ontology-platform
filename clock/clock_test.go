package clock_test

import (
	"testing"
	"time"

	"ontology/clock"
)

func TestClock(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		run  func(c *clock.FakeClock) time.Time
		want time.Time
	}{
		{"start", func(c *clock.FakeClock) time.Time { return c.Now() }, base},
		{"advance", func(c *clock.FakeClock) time.Time {
			c.Advance(5 * time.Second)
			return c.Now()
		}, base.Add(5 * time.Second)},
		{"advance more", func(c *clock.FakeClock) time.Time {
			c.Advance(2 * time.Second)
			return c.Now()
		}, base.Add(7 * time.Second)},
		{"rollback", func(c *clock.FakeClock) time.Time {
			c.Advance(-3 * time.Second)
			return c.Now()
		}, base.Add(4 * time.Second)},
		{"set", func(c *clock.FakeClock) time.Time {
			c.Set(base)
			return c.Now()
		}, base},
	}
	fc := clock.NewFakeClock(base)
	for _, tc := range cases {
		if got := tc.run(fc); !got.Equal(tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
	if rc := (clock.RealClock{}).Now(); rc.IsZero() {
		t.Fatal("real clock returned zero time")
	}
}
