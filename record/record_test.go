package record

import (
	"errors"
	"testing"

	"ontology/interval"
)

func iv(start, end int64) interval.Interval {
	return interval.Interval{Start: start, End: end}
}

func TestNew(t *testing.T) {
	cases := []struct {
		name      string
		valid, tx interval.Interval
		wantErr   error
	}{
		{"both open", iv(2020, 2021), interval.Forever(1), nil},
		{"empty valid", iv(2020, 2020), interval.Forever(1), interval.ErrEmpty},
		{"empty tx", iv(2020, 2021), iv(3, 3), interval.ErrEmpty},
		{"both empty", iv(0, 0), iv(0, 0), interval.ErrEmpty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New("k", 1, c.valid, c.tx)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err=%v want %v", err, c.wantErr)
			}
		})
	}
}

func TestCovers(t *testing.T) {
	r, err := New("k", 7, iv(10, 20), iv(100, 200))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		validAt, txAt int64
		want          bool
	}{
		{10, 100, true},   // both start points hit
		{19, 199, true},   // interior
		{20, 150, false},  // valid end excluded
		{15, 200, false},  // tx end excluded
		{9, 150, false},   // before valid start
		{15, 99, false},   // before tx start
		{10, 200, false},  // valid hits but tx misses
		{20, 100, false},  // tx hits but valid misses
	}
	for _, c := range cases {
		if got := r.Covers(c.validAt, c.txAt); got != c.want {
			t.Errorf("Covers(%d,%d)=%v want %v", c.validAt, c.txAt, got, c.want)
		}
	}
}
