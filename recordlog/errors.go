package recordlog

import (
	"errors"
	"fmt"
)

// Construction-time, distinguishable error causes.
var (
	// ErrInvalidBlockSize is returned when the block size is <= HeaderSize
	// or > MaxBlockSize.
	ErrInvalidBlockSize = errors.New("recordlog: invalid block size")

	// ErrRecordTooLarge is returned by Append when the record length
	// exceeds MaxRecord.
	ErrRecordTooLarge = errors.New("recordlog: record exceeds MaxRecord")
)

// Read-side, distinguishable error causes.
var (
	// ErrLengthOutOfBlock means a fragment's declared data length runs
	// past the end of its block. This is checked from the header alone,
	// before any data bytes are read.
	ErrLengthOutOfBlock = errors.New("recordlog: fragment length out of block")

	// ErrChecksum means the CRC32 checksum over type+data did not match
	// the header.
	ErrChecksum = errors.New("recordlog: checksum mismatch")

	// ErrTypeSequence means an illegal fragment type or fragment ordering
	// was observed (type not in 1..4, middle/last without a preceding
	// first, or a new full/first while a record is unfinished).
	ErrTypeSequence = errors.New("recordlog: illegal fragment type sequence")

	// ErrTruncated means the stream ended inside a fragment header,
	// inside fragment data, or while a multi-fragment record was not
	// closed by a last fragment.
	ErrTruncated = errors.New("recordlog: stream truncated")
)

// CorruptError wraps a read-side cause and reports the start offset of
// the fragment header at which the error was detected.
type CorruptError struct {
	Offset int64
	Err    error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("recordlog: corrupt at offset %d: %s", e.Offset, e.Err)
}

func (e *CorruptError) Unwrap() error {
	return e.Err
}
