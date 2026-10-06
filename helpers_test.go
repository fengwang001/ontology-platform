package store

import (
	"errors"
	"testing"
)

func strPtr(value string) *string {
	return &value
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireSecondary(t *testing.T, row Row, want string) {
	t.Helper()
	if row.Secondary == nil || *row.Secondary != want {
		t.Fatalf("secondary = %v, want %q", row.Secondary, want)
	}
}

func requireNilSecondary(t *testing.T, row Row) {
	t.Helper()
	if row.Secondary != nil {
		t.Fatalf("secondary = %q, want nil", *row.Secondary)
	}
}
