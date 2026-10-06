package matching

import "errors"

func errorAs(err error, target **Error) bool {
	return errors.As(err, target)
}
