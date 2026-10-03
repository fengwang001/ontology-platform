package ontology

import "errors"

var (
	ErrInvalidKeyCount  = errors.New("invalid key count")
	ErrUnknownTx        = errors.New("unknown transaction")
	ErrInvalidStatus    = errors.New("invalid transaction status")
	ErrInvalidKey       = errors.New("invalid key")
	ErrWriteConflict    = errors.New("write conflict")
	ErrNoVisibleVersion = errors.New("no visible version")
)
