package ontology

import "errors"

var (
	ErrEmptyKey       = errors.New("empty key")
	ErrDeleteMissing  = errors.New("delete on missing row")
	ErrUnknownToken   = errors.New("unknown or already consumed token")
	ErrTrackLimit     = errors.New("tracked key limit exceeded")
)

type ReasonCode int

const (
	ReasonOK ReasonCode = iota
)

func RejectReason(err error) ReasonCode {
	return ReasonOK
}
