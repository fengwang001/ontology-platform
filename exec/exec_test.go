package exec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ontology/fail"
)

func TestRunCases(t *testing.T) {
	cases := []struct {
		name        string
		ctx         func() (context.Context, context.CancelFunc)
		fn          Func
		cancelAfter time.Duration
		want        func(t *testing.T, err error)
	}{
		{
			name: "success returns nil",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			fn:   func(context.Context) error { return nil },
			want: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("err=%v", err)
				}
			},
		},
		{
			name: "ordinary error is preserved",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			fn:   func(context.Context) error { return errors.New("boom") },
			want: func(t *testing.T, err error) {
				if err == nil || err.Error() != "boom" {
					t.Fatalf("err=%v", err)
				}
			},
		},
		{
			name: "panic is captured with original value",
			ctx:  func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			fn:   func(context.Context) error { panic("kaboom") },
			want: func(t *testing.T, err error) {
				var pe *fail.PanicError
				if !errors.As(err, &pe) || !strings.Contains(pe.Error(), "kaboom") {
					t.Fatalf("err=%v", err)
				}
			},
		},
		{
			name: "pre-canceled ctx yields CanceledError",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			fn: func(context.Context) error { return nil },
			want: func(t *testing.T, err error) {
				if !errors.Is(err, &fail.CanceledError{}) {
					t.Fatalf("err=%v", err)
				}
			},
		},
		{
			name: "cancel while running yields CanceledError",
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			fn: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
			cancelAfter: 10 * time.Millisecond,
			want: func(t *testing.T, err error) {
				if !errors.Is(err, &fail.CanceledError{}) {
					t.Fatalf("err=%v", err)
				}
			},
		},
		{
			name:        "task ignoring cancel still reports canceled",
			ctx:         func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			fn:          func(context.Context) error { time.Sleep(20 * time.Millisecond); return errors.New("late") },
			cancelAfter: 5 * time.Millisecond,
			want: func(t *testing.T, err error) {
				if !errors.Is(err, &fail.CanceledError{}) {
					t.Fatalf("err=%v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.ctx()
			if tc.cancelAfter > 0 {
				go func() { time.Sleep(tc.cancelAfter); cancel() }()
			} else {
				defer cancel()
			}
			tc.want(t, Run(ctx, tc.fn))
		})
	}
}
