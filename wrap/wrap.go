// Package wrap attaches object/property context without changing error type.
package wrap

import (
	"errors"
	"fmt"
)

// ctx is the object/property annotation carried by a wrapper.
type ctx struct {
	object   string
	property string
}

// wrapper adds context on top of an error. It deliberately implements only
// Unwrap (no custom Is/As): wrapping adds context, it never replaces the type,
// so the standard errors.Is/errors.As algorithms traverse straight through.
type wrapper struct {
	cause error
	ctx   ctx
}

// Option annotates a wrapped error.
type Option func(*wrapper)

// WithObject records the affected object name.
func WithObject(object string) Option {
	return func(w *wrapper) { w.ctx.object = object }
}

// WithProperty records the affected property name.
func WithProperty(property string) Option {
	return func(w *wrapper) { w.ctx.property = property }
}

// Wrap returns err annotated with object/property context. A nil err yields nil.
func Wrap(err error, opts ...Option) error {
	if err == nil {
		return nil
	}
	w := &wrapper{cause: err}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Unwrap exposes the cause so errors.Is/errors.As pierce the wrapper.
func (w *wrapper) Unwrap() error { return w.cause }

// Object returns the object name carried by this wrapper ("" when absent).
func (w *wrapper) Object() string { return w.ctx.object }

// Property returns the property name carried by this wrapper ("" when absent).
func (w *wrapper) Property() string { return w.ctx.property }

// Error renders the context prefix and delegates the rest to the cause.
func (w *wrapper) Error() string {
	prefix := ""
	switch {
	case w.ctx.object != "" && w.ctx.property != "":
		prefix = fmt.Sprintf("object=%s, property=%s: ", w.ctx.object, w.ctx.property)
	case w.ctx.object != "":
		prefix = fmt.Sprintf("object=%s: ", w.ctx.object)
	}
	return prefix + w.cause.Error()
}

// Is delegates entirely to cause-chain matching against target.
func Is(err, target error) bool { return errors.Is(err, target) }

// As finds the first error in the chain assignable to target.
func As(err error, target any) bool { return errors.As(err, target) }

// Unwrap returns the immediate cause of err.
func Unwrap(err error) error { return errors.Unwrap(err) }

// Depth returns the number of Unwrap hops reachable from err.
func Depth(err error) int {
	depth := 0
	for err != nil {
		next := errors.Unwrap(err)
		if next == nil {
			return depth
		}
		depth++
		err = next
	}
	return depth
}

// ContextOf reports whether err is a context wrapper and returns its
// object/property annotation. It lets external packages (e.g. serialization)
// recognize wrapper layers without exporting the wrapper type.
func ContextOf(err error) (object, property string, ok bool) {
	w, isWrapper := err.(*wrapper)
	if !isWrapper {
		return "", "", false
	}
	return w.ctx.object, w.ctx.property, true
}
