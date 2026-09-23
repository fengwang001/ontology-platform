package tally

import (
	"errors"
	"testing"

	"ontology/outcome"
)

func TestNewInvalidCapacity(t *testing.T) {
	for _, cap := range []int{0, -1, -100} {
		tal, err := New(cap)
		if !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("New(%d) err = %v, want ErrInvalidCapacity", cap, err)
		}
		if tal != nil {
			t.Errorf("New(%d) returned non-nil Tally", cap)
		}
	}
}

func TestRecordAndRate(t *testing.T) {
	cases := []struct {
		name         string
		capacity     int
		seq          []outcome.Kind
		wantTotal    int
		wantFailures int
		wantRate     float64
	}{
		{"empty", 4, nil, 0, 0, 0},
		{"all success", 4, []outcome.Kind{outcome.Success, outcome.Success}, 2, 0, 0},
		{"mixed", 4, []outcome.Kind{outcome.Success, outcome.Failure, outcome.Failure, outcome.Success}, 4, 2, 0.5},
		{"rejected ignored", 4, []outcome.Kind{outcome.Success, outcome.Rejected, outcome.Rejected}, 1, 0, 0},
		{"eviction keeps last N", 3, []outcome.Kind{outcome.Failure, outcome.Failure, outcome.Failure, outcome.Success, outcome.Success}, 3, 1, 1.0 / 3.0},
		{"overwrite wraps", 2, []outcome.Kind{outcome.Failure, outcome.Failure, outcome.Success}, 2, 1, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tal, err := New(c.capacity)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range c.seq {
				tal.Record(o)
			}
			if got := tal.Total(); got != c.wantTotal {
				t.Errorf("Total() = %d, want %d", got, c.wantTotal)
			}
			if got := tal.Failures(); got != c.wantFailures {
				t.Errorf("Failures() = %d, want %d", got, c.wantFailures)
			}
			if got := tal.Rate(); got != c.wantRate {
				t.Errorf("Rate() = %v, want %v", got, c.wantRate)
			}
		})
	}
}

func TestReset(t *testing.T) {
	tal, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	tal.Record(outcome.Failure)
	tal.Record(outcome.Failure)
	tal.Reset()
	if tal.Total() != 0 || tal.Failures() != 0 || tal.Rate() != 0 {
		t.Errorf("after Reset: total=%d failures=%d rate=%v, want all zero",
			tal.Total(), tal.Failures(), tal.Rate())
	}
}

// TestRateVisitsBound pins the complexity constraint: computing the
// failure rate must not traverse the window, however large it is.
func TestRateVisitsBound(t *testing.T) {
	const maxVisits = 4
	visits := make([]int, 0, 2)
	for _, capacity := range []int{100, 10000} {
		tal, err := New(capacity)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < capacity; i++ {
			tal.Record(outcome.Failure)
		}
		tal.Rate()
		got := LastRateVisits(tal)
		if got != tal.lastVisits {
			t.Errorf("capacity %d: LastRateVisits = %d, field = %d", capacity, got, tal.lastVisits)
		}
		if got > maxVisits {
			t.Errorf("capacity %d: Rate visited %d buckets, want <= %d", capacity, got, maxVisits)
		}
		visits = append(visits, got)
	}
	if visits[0] != visits[1] {
		t.Errorf("bucket visits grow with capacity: %v", visits)
	}
}
