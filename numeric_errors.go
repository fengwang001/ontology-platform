package coercion

import (
	"errors"
	"strconv"
)

func isRangeError(err error) bool {
	var numeric *strconv.NumError
	return errors.As(err, &numeric) && errors.Is(numeric.Err, strconv.ErrRange)
}
