package classify_test

import (
	"context"
	"errors"
	"testing"

	"ontology/classify"
)

type markedTimeout struct{}

func (markedTimeout) Error() string { return "boom-timeout" }
func (markedTimeout) Timeout() bool { return true }

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		want   classify.Class
		brk    bool
		marker error
	}{
		{"nil", nil, classify.ClassSuccess, false, nil},
		{"plain", errors.New("x"), classify.ClassRetryable, true, nil},
		{"retryable", classify.Retryable(errors.New("x")), classify.ClassRetryable, true, classify.ErrRetryable},
		{"nonretryable", classify.NonRetryable(errors.New("x")), classify.ClassNonRetryable, false, classify.ErrNonRetryable},
		{"timeout-marker", classify.Timeout(errors.New("x")), classify.ClassTimeout, true, classify.ErrTimeout},
		{"deadline", context.DeadlineExceeded, classify.ClassTimeout, true, nil},
		{"canceled", context.Canceled, classify.ClassNonRetryable, false, nil},
		{"timeout-interface", markedTimeout{}, classify.ClassTimeout, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify.Of(tc.err); got != tc.want {
				t.Fatalf("Of=%d want %d", got, tc.want)
			}
			if got := classify.CountsBreaker(tc.err); got != tc.brk {
				t.Fatalf("CountsBreaker=%v want %v", got, tc.brk)
			}
			if tc.marker != nil && !errors.Is(tc.err, tc.marker) {
				t.Fatalf("errors.Is(%v, %v)=false", tc.err, tc.marker)
			}
		})
	}
}
