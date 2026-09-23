package clock

import (
	"testing"
	"time"
)

func TestClock(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		adv    []time.Duration
		wantEl time.Duration
	}{
		{"no advance", nil, 0},
		{"forward", []time.Duration{time.Second, 2 * time.Second}, 3 * time.Second},
		{"backward allowed", []time.Duration{time.Hour, -time.Minute}, 59 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFake(base)
			for _, d := range tc.adv {
				f.Advance(d)
			}
			if got := f.Now().Sub(base); got != tc.wantEl {
				t.Fatalf("elapsed = %v, want %v", got, tc.wantEl)
			}
		})
	}
	if (Real{}).Now().IsZero() {
		t.Fatal("real clock returned zero time")
	}
}
