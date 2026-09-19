package ontology

import (
	"errors"
	"testing"
	"time"
)

func testLimiter(t *testing.T, cfg Config) *Limiter {
	t.Helper()
	limiter, err := NewLimiter(cfg)
	if err != nil {
		t.Fatalf("NewLimiter() error = %v", err)
	}
	return limiter
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertInsufficient(t *testing.T, err error) *InsufficientTokensError {
	t.Helper()
	var insufficient *InsufficientTokensError
	if !errors.As(err, &insufficient) {
		t.Fatalf("error = %v, want *InsufficientTokensError", err)
	}
	return insufficient
}

func assertAvailable(t *testing.T, limiter *Limiter, tenant string, now time.Time, want int) {
	t.Helper()
	if got := limiter.Available(tenant, now); got != want {
		t.Fatalf("Available(%q) = %d, want %d", tenant, got, want)
	}
}
