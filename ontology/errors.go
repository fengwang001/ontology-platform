package ontology

import "errors"

var (
	ErrDuplicateKey       = errors.New("ontology: duplicate primary key")
	ErrUnknownColumn      = errors.New("ontology: reference to unknown column")
	ErrInvalidRange       = errors.New("ontology: range lower bound exceeds upper bound")
	ErrArithmeticOverflow = errors.New("ontology: arithmetic overflow on 64-bit signed integer")
	ErrUniqueViolation    = errors.New("ontology: unique index violation at statement end")
)
