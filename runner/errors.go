package runner

import "errors"

var (
	ErrInvalid    = errors.New("invalid argument")
	ErrPermission = errors.New("permission denied")
	ErrNow        = errors.New("non-monotonic now")
	ErrFailed     = errors.New("failed migration exists")
	ErrChecksum   = errors.New("checksum mismatch")
	ErrOutOfOrder = errors.New("out of order migration")
	ErrNoFailed   = errors.New("no failed migration")
	ErrNoUndo     = errors.New("undo action missing")
	ErrPanic      = errors.New("callback panic")
)

type VersionError struct {
	Err error
	Ver int64
}

func (e *VersionError) Error() string {
	return e.Err.Error()
}

func (e *VersionError) Unwrap() error {
	return e.Err
}

func versionError(err error, ver int64) error {
	return &VersionError{Err: err, Ver: ver}
}
