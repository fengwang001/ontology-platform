package ontology

import (
	"errors"
	"strconv"
)

func asRejectError(err error, target **RejectError) bool {
	return errors.As(err, target)
}

func uitoa(v uint64) string { return strconv.FormatUint(v, 10) }
