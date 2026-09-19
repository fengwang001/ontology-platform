package ontology

import (
	"errors"
	"testing"
	"time"
)

func TestFractionAccumulatesAcrossCalls(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 100, Rate: 7}, base); err != nil {
		t.Fatal(err)
	}

	ok, err := l.Allow("t", 100, base)
	if !ok || err != nil {
		t.Fatalf("initial allow = (%v, %v)", ok, err)
	}

	for i := 1; i <= 10; i++ {
		now := base.Add(time.Duration(i) * 100 * time.Millisecond)
		got, err := l.AvailableTokens("t", now)
		if err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if i >= 2 { // 0.7 after one interval, 1.4 after two.
			want = int64(i) * 7 / 10
		}
		if got != want {
			t.Fatalf("step %d: got %d tokens, want %d", i, got, want)
		}
	}
}

func TestThousandMicrostepsMatchesOneAdvance(t *testing.T) {
	base := time.Unix(0, 0)

	stepped := NewLimiter(1)
	jumped := NewLimiter(1)
	if err := stepped.AddTenant("t", Config{Capacity: 1000, Rate: 123}, base); err != nil {
		t.Fatal(err)
	}
	if err := jumped.AddTenant("t", Config{Capacity: 1000, Rate: 123}, base); err != nil {
		t.Fatal(err)
	}

	for _, limiter := range []*Limiter{stepped, jumped} {
		ok, err := limiter.Allow("t", 1000, base)
		if !ok || err != nil {
			t.Fatalf("drain = (%v, %v)", ok, err)
		}
	}

	for i := 1; i <= 1000; i++ {
		if _, err := stepped.AvailableTokens("t", base.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := stepped.AvailableTokens("t", base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want, err := jumped.AvailableTokens("t", base.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("microsteps = %d, one jump = %d", got, want)
	}
}

func TestRefillNeverExceedsCapacity(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 5, Rate: 100}, base); err != nil {
		t.Fatal(err)
	}

	got, err := l.AvailableTokens("t", base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Fatalf("got %d tokens, want capacity 5", got)
	}

	if _, err := l.Allow("t", 6, base.Add(time.Hour)); !errors.Is(err, ErrRequestExceedsCapacity) {
		t.Fatalf("oversize error = %v", err)
	}
}
