package adjust

import "errors"

var (
	ErrInvalid  = errors.New("adjust: invalid argument")
	ErrNotFound = errors.New("adjust: not found")
	ErrState    = errors.New("adjust: illegal state")
	ErrConflict = errors.New("adjust: conflict")
	ErrStock    = errors.New("adjust: insufficient stock")
)

func validID(id []byte) bool {
	return len(id) >= 1 && len(id) <= 32
}
