package step

import (
	"errors"
	"testing"
)

func ok() (Outcome, error) { return Success, nil }

func TestValidate(t *testing.T) {
	cases := []struct {
		name  string
		steps []Step
		want  error
	}{
		{"empty", nil, ErrEmptySteps},
		{"nil forward", []Step{{Forward: nil}}, ErrNilForward},
		{"duplicate keys", []Step{{IdemKey: "k", Forward: ok}, {IdemKey: "k", Forward: ok}}, ErrDuplicateKey},
		{"empty keys never duplicate", []Step{{Forward: ok}, {Forward: ok}}, nil},
		{"valid", []Step{{IdemKey: "a", Forward: ok}, {IdemKey: "b", Forward: ok, Compensate: ok}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.steps)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNeedsCompensate(t *testing.T) {
	cases := []struct {
		res  Outcome
		want bool
	}{
		{Success, true},
		{Unknown, true},
		{Fail, false},
	}
	for _, tc := range cases {
		if got := NeedsCompensate(tc.res); got != tc.want {
			t.Fatalf("NeedsCompensate(%d) = %v, want %v", tc.res, got, tc.want)
		}
	}
}
