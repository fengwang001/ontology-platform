package gate

import (
	"errors"

	"ontology/cluster"
)

func gateErr(op string, base error, detail string) error {
	return &Error{Op: op, Base: base, Detail: detail}
}

func mapErr(op string, err error, detail string) error {
	switch {
	case errors.Is(err, cluster.ErrInvalidArg):
		return gateErr(op, ErrInvalidArg, detail)
	case errors.Is(err, cluster.ErrExists):
		return gateErr(op, ErrExists, detail)
	case errors.Is(err, cluster.ErrNotExist):
		return gateErr(op, ErrNotExist, detail)
	default:
		return err
	}
}
