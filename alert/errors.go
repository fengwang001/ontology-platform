package alert

import "errors"

var (
	ErrInvalid = errors.New("alert: invalid argument")
	ErrNotDue  = errors.New("alert: threshold not due")
)
