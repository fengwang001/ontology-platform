package classify

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name      string
		err       error
		want      Category
		sentinel  error
		preserves bool
	}{
		{"raw default retryable", boom, Retryable, nil, false},
		{"wrapped non-retryable", Wrap(NonRetryable, boom), NonRetryable, ErrNonRetryable, true},
		{"wrapped timeout", Wrap(Timeout, boom), Timeout, ErrTimeout, true},
		{"wrapped panic", PanicError("kaboom"), PanicKind, ErrPanic, false},
		{"wrapped retryable", Wrap(Retryable, boom), Retryable, ErrRetryable, true},
		{"context deadline", context.DeadlineExceeded, Timeout, nil, false},
		{"context canceled", context.Canceled, Timeout, nil, false},
		{"panic error value", PanicError(boom), PanicKind, ErrPanic, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Fatalf("category = %d, want %d", got, tc.want)
			}
			if tc.sentinel != nil && !errors.Is(tc.err, tc.sentinel) {
				t.Fatalf("errors.Is failed for %v", tc.sentinel)
			}
			if tc.preserves && !errors.Is(tc.err, boom) {
				t.Fatalf("original error lost")
			}
		})
	}
	if got := Classify(nil); got != Retryable {
		t.Fatalf("nil classified as %d", got)
	}
	if s := PanicError(fmt.Errorf("e")).Error(); s == "" {
		t.Fatal("empty panic message")
	}
}
