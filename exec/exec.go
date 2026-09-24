// Package exec runs a single task function and captures its outcome,
// turning panics into values instead of crashes.
package exec

import "context"

// Result is the outcome of one task invocation.
type Result struct {
	// Err is the error returned by the task, nil on success.
	Err error
	// Panic is the recovered panic value, valid only if Panicked is true.
	Panic    any
	Panicked bool
}

// Run invokes fn, recovering any panic into the returned Result.
func Run(ctx context.Context, fn func(context.Context) error) (res Result) {
	defer func() {
		if v := recover(); v != nil {
			res = Result{Panic: v, Panicked: true}
		}
	}()
	res.Err = fn(ctx)
	return res
}
