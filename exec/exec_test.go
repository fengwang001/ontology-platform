package exec

import (
	"context"
	"errors"
	"testing"

	"ontology/fail"
)

func TestRun(t *testing.T) {
	cases := []struct {
		name string
		fn   Func
		want error
	}{
		{"success", func(context.Context) error { return nil }, nil},
		{"error-passthrough", func(context.Context) error { return fail.ErrCanceled }, fail.ErrCanceled},
		{"panic-string", func(context.Context) error { panic("kaboom") }, fail.ErrPanic},
		{"panic-error-value", func(context.Context) error { panic(fail.ErrSkipped) }, fail.ErrPanic},
	}
	for _, c := range cases {
		if got := Run(context.Background(), c.fn).Err; !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	res := Run(context.Background(), func(context.Context) error { panic("kaboom") })
	var pe *fail.PanicError
	if !errors.As(res.Err, &pe) || pe.Value != "kaboom" {
		t.Fatalf("panic payload lost: %v", res.Err)
	}
}
