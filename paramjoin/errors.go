package paramjoin

import (
	"errors"
	"fmt"

	"ontology/paramlex"
)

var (
	ErrSyntax     = paramlex.ErrSyntax
	ErrIncomplete = errors.New("incomplete continuation")
	ErrCharset    = errors.New("unsupported charset")
	ErrEncoding   = errors.New("invalid character encoding")
)

type IncompleteError struct{ Missing int }

func (e IncompleteError) Error() string {
	return fmt.Sprintf("missing segment %d", e.Missing)
}

func (e IncompleteError) Is(target error) bool { return target == ErrIncomplete }

type CharsetError struct{ Charset string }

func (e CharsetError) Error() string { return "unsupported charset: " + e.Charset }

func (e CharsetError) Is(target error) bool { return target == ErrCharset }
