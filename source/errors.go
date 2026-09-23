package source

import (
	"errors"
	"fmt"
)

var (
	ErrEmptyKey      = errors.New("source: key is empty")
	ErrEmptySegment  = errors.New("source: key path has empty segment")
	ErrDuplicateKey  = errors.New("source: duplicate key within one layer")
	ErrTruncated     = errors.New("source: configuration file truncated")
	ErrBadFile       = errors.New("source: malformed configuration file")
	ErrAmbiguousEnv  = errors.New("source: ambiguous environment variable names")
)

type TruncationClass int

const (
	TruncLine TruncationClass = iota + 1
	TruncKey
	TruncValue
)

func (c TruncationClass) String() string {
	switch c {
	case TruncLine:
		return "line incomplete"
	case TruncKey:
		return "key incomplete"
	case TruncValue:
		return "value incomplete"
	default:
		return "unknown"
	}
}

type TruncationError struct {
	Class TruncationClass
	Line  int
	Byte  int
}

func (e *TruncationError) Error() string {
	return fmt.Sprintf("%s: %s at line %d, byte %d",
		ErrTruncated, e.Class, e.Line, e.Byte)
}

func (e *TruncationError) Unwrap() error { return ErrTruncated }

type FileError struct {
	Line int
	Msg  string
}

func (e *FileError) Error() string {
	return fmt.Sprintf("%s: line %d: %s", ErrBadFile, e.Line, e.Msg)
}

func (e *FileError) Unwrap() error { return ErrBadFile }

type AmbiguousEnvError struct {
	Names []string
}

func (e *AmbiguousEnvError) Error() string {
	return fmt.Sprintf("%s: %v map to the same key", ErrAmbiguousEnv, e.Names)
}

func (e *AmbiguousEnvError) Unwrap() error { return ErrAmbiguousEnv }
