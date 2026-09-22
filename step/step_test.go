package step

import (
	"errors"
	"testing"
)

func TestOutcomeClassification(t *testing.T) {
	base := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", base, false},
		{"definite", NewDefiniteFailure(base), false},
		{"unknown", NewUnknownOutcome(base), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnknown(tc.err); got != tc.want {
				t.Fatalf("IsUnknown=%v want %v", got, tc.want)
			}
		})
	}
}
