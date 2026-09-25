package classify_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/classify"
	"ontology/timeout"
)

func TestSentinelErrorsDistinguishable(t *testing.T) {
	sentinels := []error{breaker.ErrOpen, breaker.ErrClockBackward, bulkhead.ErrFull, classify.ErrTimeout}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if (i == j) != errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) 判定错误", a, b)
			}
		}
	}
}

func TestOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want classify.Kind
	}{
		{"generic is retryable", errors.New("boom"), classify.KindRetryable},
		{"wrapped non-retryable", fmt.Errorf("bad arg: %w", classify.ErrNonRetryable), classify.KindNonRetryable},
		{"wrapped timeout", fmt.Errorf("rpc: %w", classify.ErrTimeout), classify.KindTimeout},
		{"context deadline", context.DeadlineExceeded, classify.KindTimeout},
	}
	for _, tc := range cases {
		if got := classify.Of(tc.err); got != tc.want {
			t.Errorf("%s: Of() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestKindTrips(t *testing.T) {
	cases := []struct {
		kind  classify.Kind
		trips bool
	}{
		{classify.KindRetryable, true},
		{classify.KindNonRetryable, false},
		{classify.KindTimeout, true},
	}
	for _, tc := range cases {
		if got := tc.kind.Trips(); got != tc.trips {
			t.Errorf("%v.Trips() = %v, want %v", tc.kind, got, tc.trips)
		}
	}
}

func TestDo(t *testing.T) {
	cases := []struct {
		name string
		fn   func(context.Context) error
		want error // 期望 errors.Is 命中的哨兵；nil 表示期望成功
		kind classify.Kind
	}{
		{"success", func(context.Context) error { return nil }, nil, classify.KindRetryable},
		{"failure passthrough", func(context.Context) error { return fmt.Errorf("x: %w", classify.ErrNonRetryable) }, classify.ErrNonRetryable, classify.KindNonRetryable},
		{"slow call times out", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, classify.ErrTimeout, classify.KindTimeout},
		{"panic converted to error", func(context.Context) error { panic("bang") }, nil, classify.KindRetryable},
	}
	for _, tc := range cases {
		err := timeout.Do(context.Background(), 20*time.Millisecond, tc.fn)
		switch tc.name {
		case "success":
			if err != nil {
				t.Errorf("%s: got %v, want nil", tc.name, err)
			}
		case "panic converted to error":
			if err == nil {
				t.Errorf("%s: got nil, want panic error", tc.name)
			}
		default:
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: got %v, want errors.Is %v", tc.name, err, tc.want)
			}
		}
		if err != nil {
			if got := classify.Of(err); got != tc.kind {
				t.Errorf("%s: Of() = %v, want %v", tc.name, got, tc.kind)
			}
		}
	}
}
