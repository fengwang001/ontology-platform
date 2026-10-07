package tolling

import "errors"

func asError(err error, target **ServiceError) bool {
	return errors.As(err, target)
}

func serviceError(code ErrorCode, msg string) error {
	return &ServiceError{Code: code, Msg: msg}
}
