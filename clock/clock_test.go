package clock_test

import (
	"errors"
	"testing"

	"ontology/clock"
)

func TestClock(t *testing.T) {
	tests := []struct {
		name string
		run  func() (int64, error)
		want int64
		err  error
	}{
		{"fake initial", func() (int64, error) { return clock.NewFake(10).Now() }, 10, nil},
		{"fake advance", func() (int64, error) {
			c := clock.NewFake(10)
			c.Advance(5)
			return c.Now()
		}, 15, nil},
		{"fake rollback", func() (int64, error) {
			c := clock.NewFake(10)
			c.Now()
			c.Set(9)
			return c.Now()
		}, 0, clock.ErrClockMovedBack},
		{"real monotonic", func() (int64, error) {
			v := int64(100)
			c := clock.NewReal(func() int64 { return v })
			c.Now()
			v = 200
			return c.Now()
		}, 200, nil},
		{"real rollback", func() (int64, error) {
			v := int64(100)
			c := clock.NewReal(func() int64 { return v })
			c.Now()
			v = 50
			return c.Now()
		}, 0, clock.ErrClockMovedBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.run()
			if !errors.Is(err, tt.err) || got != tt.want {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, err, tt.want, tt.err)
			}
		})
	}
}
