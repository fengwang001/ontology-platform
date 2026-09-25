// Package wrap adds object/property context without changing error identity.
package wrap

import (
	"fmt"

	"ontology/errdef"
)

// Ctx is the optional context attached by Wrap.
type Ctx struct {
	Object   string
	Property string
}

// isSteps counts identity comparisons at the wrapping layer.
var isSteps int

// ResetSteps zeroes the comparison counter (test hook).
func ResetSteps() { isSteps = 0 }

// Steps returns comparisons since ResetSteps.
func Steps() int { return isSteps }

type wrapped struct {
	code     errdef.Code
	message  string
	object   string
	property string
	cause    error
}

// Wrap decorates err with context. It copies the cause's code so the
// wrapper answers the same typed question; matching is then confirmed by
// traversing the cause chain via Unwrap.
func Wrap(err error, ctx ...Ctx) error {
	if err == nil {
		return nil
	}
	w := &wrapped{message: err.Error(), cause: err, code: errdef.CodeInternal}
	if cp, ok := err.(errdef.CodeProvider); ok {
		w.code = cp.ErrCode()
	}
	if len(ctx) > 0 {
		w.object = ctx[0].Object
		w.property = ctx[0].Property
	}
	return w
}

func (w *wrapped) Error() string {
	switch {
	case w.object != "" && w.property != "":
		return fmt.Sprintf("%s: %s.%s", w.message, w.object, w.property)
	case w.object != "":
		return fmt.Sprintf("%s: %s", w.message, w.object)
	default:
		return w.message
	}
}

// ErrCode keeps the wrapped error's type identity readable on the outer layer.
func (w *wrapped) ErrCode() errdef.Code { return w.code }

func (w *wrapped) Object() string   { return w.object }
func (w *wrapped) Property() string { return w.property }

// Message returns the raw message without the context suffix.
func (w *wrapped) Message() string { return w.message }

// Unwrap exposes the cause: errors.Is/As pierce every wrapping layer.
func (w *wrapped) Unwrap() error { return w.cause }

// Is short-circuits on equal code (one comparison regardless of depth)
// and lets the wide base target match; other questions fall through to
// the cause chain via errors.Is(w.Unwrap(), target).
func (w *wrapped) Is(target error) bool {
	isSteps++
	if tp, ok := target.(errdef.CodeProvider); ok {
		c := tp.ErrCode()
		if c == w.code || c == errdef.CodeBase {
			return true
		}
	}
	return false
}

// Rebuild constructs a wrapping node from decoded fields.
func Rebuild(code errdef.Code, msg, object, property string, cause error) error {
	return &wrapped{code: code, message: msg, object: object, property: property, cause: cause}
}
