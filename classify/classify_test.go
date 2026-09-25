package classify_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ontology/classify"
	"ontology/timeout"
)

func TestOf(t *testing.T) {
	plain := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want classify.Kind
	}{
		{"nil", nil, classify.None},
		{"plain defaults retryable", plain, classify.Retryable},
		{"sentinel timeout", classify.ErrTimeout, classify.Timeout},
		{"wrapped timeout", fmt.Errorf("call: %w", classify.ErrTimeout), classify.Timeout},
		{"deadline exceeded", context.DeadlineExceeded, classify.Timeout},
		{"wrapped deadline", fmt.Errorf("rpc: %w", context.DeadlineExceeded), classify.Timeout},
		{"panic sentinel", fmt.Errorf("%w: x", classify.ErrPanic), classify.NonRetryable},
		{"marked non-retryable", classify.MarkNonRetryable(plain), classify.NonRetryable},
		{"wrapped marked", fmt.Errorf("outer: %w", classify.MarkNonRetryable(plain)), classify.NonRetryable},
		{"mark nil", classify.MarkNonRetryable(nil), classify.None},
	}
	for _, c := range cases {
		if got := classify.Of(c.err); got != c.want {
			t.Errorf("%s: Of()=%v want %v", c.name, got, c.want)
		}
	}
}

func TestKindString(t *testing.T) {
	cases := []struct {
		kind classify.Kind
		want string
	}{
		{classify.None, "none"},
		{classify.Retryable, "retryable"},
		{classify.NonRetryable, "non-retryable"},
		{classify.Timeout, "timeout"},
		{classify.Kind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("String()=%q want %q", got, c.want)
		}
	}
}

func TestTimeoutDo(t *testing.T) {
	block := func(ctx context.Context) error { <-ctx.Done(); return nil }
	cases := []struct {
		name      string
		d         time.Duration
		cancelCtx bool
		fn        func(context.Context) error
		want      classify.Kind
		isErr     error
	}{
		{"success", time.Second, false, func(context.Context) error { return nil }, classify.None, nil},
		{"retryable failure", time.Second, false, func(context.Context) error { return errors.New("x") }, classify.Retryable, nil},
		{"timeout", 5 * time.Millisecond, false, block, classify.Timeout, classify.ErrTimeout},
		{"panic recovered", time.Second, false, func(context.Context) error { panic("boom") }, classify.NonRetryable, classify.ErrPanic},
		{"invalid duration", 0, false, func(context.Context) error { return nil }, classify.Retryable, timeout.ErrInvalidDuration},
		{"parent canceled", time.Hour, true, block, classify.Retryable, context.Canceled},
	}
	for _, c := range cases {
		ctx := context.Background()
		if c.cancelCtx {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		err := timeout.Do(ctx, c.d, c.fn)
		if got := classify.Of(err); got != c.want {
			t.Errorf("%s: Of(err)=%v want %v (err=%v)", c.name, got, c.want, err)
		}
		if c.isErr != nil && !errors.Is(err, c.isErr) {
			t.Errorf("%s: err=%v not errors.Is %v", c.name, err, c.isErr)
		}
	}
}
