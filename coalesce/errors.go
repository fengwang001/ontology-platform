package coalesce

import "errors"

// UnsatisfiableError reports that every requested interval starts at/past EOF.
type UnsatisfiableError struct {
	ResourceLength int64
}

func (e UnsatisfiableError) Error() string {
	return "coalesce: requested range is not satisfiable"
}

func (e UnsatisfiableError) Is(target error) bool {
	_, ok := target.(UnsatisfiableError)
	return ok
}

var ErrUnsatisfiable = errors.New("coalesce: range not satisfiable")

// IsUnsatisfiable reports err and the resource length used during normalization.
func IsUnsatisfiable(err error) (int64, bool) {
	var target UnsatisfiableError
	if errors.As(err, &target) {
		return target.ResourceLength, true
	}
	return 0, false
}
