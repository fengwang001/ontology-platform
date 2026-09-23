package clock

import (
	"testing"
	"time"
)

func TestClock(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		op   func(f *Fake)
		want time.Time
	}{
		{"初始", func(f *Fake) {}, t0},
		{"推进", func(f *Fake) { f.Advance(5 * time.Second) }, t0.Add(5 * time.Second)},
		{"回拨", func(f *Fake) { f.Advance(-time.Second) }, t0.Add(-time.Second)},
		{"设定", func(f *Fake) { f.Set(t0.Add(time.Hour)) }, t0.Add(time.Hour)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFake(t0)
			tc.op(f)
			if got := f.Now(); !got.Equal(tc.want) {
				t.Fatalf("Now()=%v, want %v", got, tc.want)
			}
		})
	}
	if (Real{}).Now().IsZero() {
		t.Fatal("真实时钟不应为零值")
	}
}
