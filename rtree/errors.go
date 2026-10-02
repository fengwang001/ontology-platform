package rtree

import "errors"

var (
	ErrInvalid   = errors.New("invalid argument")
	ErrDuplicate = errors.New("id already exists")
	ErrNotFound  = errors.New("object not found")
	ErrFull      = errors.New("capacity reached")
)

const coordLimit = 1_000_000_000

func validateRect(r Rect) bool {
	return r.X1 >= -coordLimit && r.X2 <= coordLimit &&
		r.Y1 >= -coordLimit && r.Y2 <= coordLimit &&
		r.X1 <= r.X2 && r.Y1 <= r.Y2
}
