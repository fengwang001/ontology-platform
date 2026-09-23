package lease

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	exp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		start   uint64
		length  int64
		wantErr error
		wantEnd uint64
	}{
		{"normal", 100, 50, nil, 150},
		{"length one", 0, 1, nil, 1},
		{"zero length", 0, 0, ErrInvalidLength, 0},
		{"negative length", 0, -3, ErrInvalidLength, 0},
		{"overflow at max", math.MaxUint64, 1, ErrOverflow, 0},
		{"overflow near max", math.MaxUint64 - 4, 10, ErrOverflow, 0},
		{"exact fit", math.MaxUint64 - 4, 4, nil, math.MaxUint64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := New(tc.start, tc.length, exp)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && l.End() != tc.wantEnd {
				t.Fatalf("End() = %d, want %d", l.End(), tc.wantEnd)
			}
		})
	}
}

func TestValidAt(t *testing.T) {
	exp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		expiry time.Time
		at     time.Time
		want   bool
	}{
		{"before expiry", exp, exp.Add(-time.Nanosecond), true},
		{"at expiry", exp, exp, false},
		{"after expiry", exp, exp.Add(time.Second), false},
		{"zero expiry (ttl 0)", time.Time{}, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := New(0, 10, tc.expiry)
			if err != nil {
				t.Fatal(err)
			}
			if got := l.ValidAt(tc.at); got != tc.want {
				t.Fatalf("ValidAt = %v, want %v", got, tc.want)
			}
		})
	}
}
