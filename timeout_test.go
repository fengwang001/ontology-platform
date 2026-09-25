package ontology_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/classify"
	"ontology/timeout"
)

func TestTimeout(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		fn   func(context.Context) error
		want func(error) bool
	}{
		{
			"success", 50 * time.Millisecond,
			func(context.Context) error { return nil },
			func(err error) bool { return err == nil },
		},
		{
			"passthrough retryable", 50 * time.Millisecond,
			func(context.Context) error { return classify.ErrRetryable },
			func(err error) bool { return errors.Is(err, classify.ErrRetryable) },
		},
		{
			"deadline", time.Millisecond,
			func(ctx context.Context) error {
				<-ctx.Done()
				time.Sleep(5 * time.Millisecond)
				return nil
			},
			func(err error) bool {
				return errors.Is(err, timeout.ErrTimedOut) &&
					errors.Is(err, classify.ErrTimeout) &&
					classify.Of(err) == classify.Timeout
			},
		},
		{
			"panic converted", 50 * time.Millisecond,
			func(context.Context) error { panic("boom") },
			func(err error) bool {
				return errors.Is(err, classify.ErrRetryable) &&
					classify.Of(err) == classify.Retryable
			},
		},
		{
			"parent canceled", 50 * time.Millisecond,
			func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			func(err error) bool { return errors.Is(err, context.Canceled) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.name == "parent canceled" {
				go func() { time.Sleep(2 * time.Millisecond); cancel() }()
			}
			if err := timeout.Do(ctx, tc.d, tc.fn); !tc.want(err) {
				t.Fatalf("err=%v", err)
			}
		})
	}

	// 非法时限报错。
	for _, d := range []time.Duration{0, -time.Second} {
		if err := timeout.Do(context.Background(), d, func(context.Context) error { return nil }); err == nil {
			t.Fatalf("d=%v should error", d)
		}
	}
}
