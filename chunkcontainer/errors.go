package chunkcontainer

import (
	"errors"
	"strconv"
)

type ErrorKind string

const (
	InvalidArgument  ErrorKind = "invalid argument"
	OutOfBounds      ErrorKind = "out of bounds"
	Decompression    ErrorKind = "decompression failed"
	LengthMismatch   ErrorKind = "length mismatch"
	ChecksumMismatch ErrorKind = "checksum mismatch"
)

var (
	ErrInvalidArgument = errors.New(string(InvalidArgument))
	ErrOutOfBounds     = errors.New(string(OutOfBounds))
)

type BlockError struct {
	Kind       ErrorKind
	BlockIndex int
}

func (e BlockError) Error() string {
	return string(e.Kind) + " at block " + strconv.Itoa(e.BlockIndex)
}
