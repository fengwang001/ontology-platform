package ontology

import (
	"errors"
	"testing"
	"time"
)

func ts(n int64) time.Time { return time.Unix(n, 0) }

func TestIntervalHalfOpenBoundaries(t *testing.T) {
	iv := Interval{From: ts(10), To: ts(20)}
	for _, c := range []struct {
		at   int64
		want bool
	}{
		{9, false},
		{10, true},  // From is inclusive.
		{19, true},  // last point before To.
		{20, false}, // To is exclusive.
		{21, false},
	} {
		if got := iv.Contains(ts(c.at)); got != c.want {
			t.Fatalf("Contains(%d) = %v, want %v", c.at, got, c.want)
		}
	}

	open := Interval{From: ts(10), To: time.Time{}}
	if !open.Contains(ts(1_000_000)) {
		t.Fatal("zero To must mean infinity")
	}
}

func TestIntervalOverlapTouching(t *testing.T) {
	a := Interval{From: ts(0), To: ts(10)}
	b := Interval{From: ts(10), To: ts(20)}
	if a.Overlaps(b) {
		t.Fatal("half-open intervals touching at a boundary must not overlap")
	}
	c := Interval{From: ts(5), To: ts(15)}
	if !a.Overlaps(c) || !c.Overlaps(b) {
		t.Fatal("real overlap not detected")
	}
}

func TestValidateIntervalErrorClasses(t *testing.T) {
	cases := []struct {
		name string
		iv   Interval
		want error
	}{
		{"zero from", Interval{From: time.Time{}, To: ts(5)}, ErrZeroFrom},
		{"empty", Interval{From: ts(5), To: ts(5)}, ErrEmptyInterval},
		{"reversed", Interval{From: ts(6), To: ts(5)}, ErrReversedInterval},
		{"open to is fine", Interval{From: ts(5), To: time.Time{}}, nil},
	}
	for _, c := range cases {
		err := ValidateValidInterval(c.iv)
		if c.want == nil {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}

	if errors.Is(ErrEmptyInterval, ErrReversedInterval) {
		t.Fatal("empty and reversed intervals must be distinct error classes")
	}
}
