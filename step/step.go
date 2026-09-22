package step

import "fmt"

type Outcome int

const (
	Fail Outcome = iota
	Success
	Unknown
)

type Func func() (Outcome, error)

type Step struct {
	ID         string
	IdemKey    string
	Retryable  bool
	Forward    Func
	Compensate Func
}

var (
	ErrEmptySteps    = sentinel("step: empty step list")
	ErrDuplicateKey  = sentinel("step: duplicate idempotency key")
	ErrNilForward    = sentinel("step: forward action is nil")
	ErrNilCompensate = sentinel("step: compensate action is nil but step may succeed")
)

type Error struct{ msg string }

func (e Error) Error() string { return e.msg }

func sentinel(msg string) error { return Error{msg: msg} }

func Validate(steps []Step) error {
	if len(steps) == 0 {
		return ErrEmptySteps
	}
	seen := make(map[string]struct{}, len(steps))
	for i := range steps {
		if steps[i].Forward == nil {
			return fmt.Errorf("%w (index %d)", ErrNilForward, i)
		}
		if steps[i].IdemKey == "" {
			continue
		}
		if _, dup := seen[steps[i].IdemKey]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateKey, steps[i].IdemKey)
		}
		seen[steps[i].IdemKey] = struct{}{}
	}
	return nil
}

// NeedsCompensate reports whether a step whose forward call returned res
// produced (or may have produced) an effect that requires compensation.
func NeedsCompensate(res Outcome) bool {
	return res == Success || res == Unknown
}
