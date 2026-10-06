package fontcore

import (
	"errors"
	"fmt"
)

// 可区分的六类错误。拒绝次序（参数非法最先）由各入口保证。
var (
	ErrInvalidArgument   = errors.New("fontcore: invalid argument")
	ErrClockRewound      = errors.New("fontcore: clock rewound")
	ErrFamilyNotFound    = errors.New("fontcore: family not found")
	ErrFaceNotFound      = errors.New("fontcore: face not found")
	ErrDuplicateRegister = errors.New("fontcore: duplicate face registration")
	ErrInvalidLoadState  = errors.New("fontcore: load report not allowed")
)

func invalidf(format string, args ...any) error {
	return wrapErr(ErrInvalidArgument, sprintf(format, args...))
}

func wrapErr(base error, msg string) error {
	return &fcError{base: base, msg: msg}
}

type fcError struct {
	base error
	msg  string
}

func (e *fcError) Error() string { return e.base.Error() + ": " + e.msg }
func (e *fcError) Unwrap() error { return e.base }

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
