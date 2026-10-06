package blockstore

import "fmt"

type ErrorKind int

const (
	InvalidArgument ErrorKind = iota
	OutOfRange
	DecompressFailed
	LengthMismatch
	ChecksumMismatch
)

type Error struct {
	Kind       ErrorKind
	BlockIndex int
	Op         string
	Err        error
}

func (e *Error) Error() string {
	block := ""
	if e.BlockIndex >= 0 {
		block = fmt.Sprintf(" block %d", e.BlockIndex)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s:%s: %v", e.Op, block, e.Err)
	}
	return fmt.Sprintf("%s:%s: %s", e.Op, block, e.Kind)
}

func (k ErrorKind) String() string {
	switch k {
	case InvalidArgument:
		return "invalid argument"
	case OutOfRange:
		return "offset out of range"
	case DecompressFailed:
		return "decompression failed"
	case LengthMismatch:
		return "length mismatch"
	case ChecksumMismatch:
		return "checksum mismatch"
	default:
		return "unknown error"
	}
}

func blockError(op string, index int, kind ErrorKind) *Error {
	return &Error{Kind: kind, BlockIndex: index, Op: op}
}

func earlierBlockError(current *Error, candidate *Error) *Error {
	if current == nil || candidate.BlockIndex < current.BlockIndex {
		return candidate
	}
	return current
}
func (e *Error) Unwrap() error { return e.Err }
