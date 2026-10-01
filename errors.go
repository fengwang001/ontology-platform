package ontology

import "errors"

var (
	ErrInvalidWord      = errors.New("invalid word")
	ErrWordExists       = errors.New("word already exists")
	ErrWordMissing      = errors.New("word does not exist")
	ErrInvalidThreshold = errors.New("invalid threshold")
	ErrInvalidLimit     = errors.New("invalid limit")
)
