package fragmentlog

import (
	"errors"
	"fmt"
)

// Construction and append-time rejection reasons.
var (
	// ErrBlockSizeTooSmall is returned when the block size is not greater
	// than the fragment header size.
	ErrBlockSizeTooSmall = errors.New("fragmentlog: block size must be greater than 7")
	// ErrBlockSizeTooLarge is returned when the block size exceeds 65535.
	ErrBlockSizeTooLarge = errors.New("fragmentlog: block size must not exceed 65535")
	// ErrRecordTooLarge is returned by Append when the record exceeds
	// MaxRecord bytes; the append is rejected as a whole.
	ErrRecordTooLarge = errors.New("fragmentlog: record length exceeds MaxRecord")
)

// Read-side corruption classes. Use errors.Is to distinguish them.
var (
	// ErrLength: the fragment data length runs past the end of its block
	// (judged from the header alone, before any data byte is read).
	ErrLength = errors.New("fragmentlog: fragment data length exceeds its block")
	// ErrChecksum: the crc32 of type byte plus data does not match.
	ErrChecksum = errors.New("fragmentlog: fragment checksum mismatch")
	// ErrSequence: illegal fragment type sequence (type outside 1..4, a
	// middle/last fragment without a preceding first fragment, or a
	// first/full fragment while a record is still unfinished).
	ErrSequence = errors.New("fragmentlog: invalid fragment type sequence")
	// ErrTruncated: the stream ends in the middle of a fragment or with a
	// record still unfinished.
	ErrTruncated = errors.New("fragmentlog: stream ends mid-fragment or mid-record")
)

// CorruptError describes a recoverable corruption found by Reader.Next.
// The Kind field is one of ErrLength, ErrChecksum, ErrSequence or
// ErrTruncated, so errors.Is(err, fragmentlog.ErrChecksum) etc. work.
type CorruptError struct {
	Kind   error
	Offset int64 // offset of the offending fragment header (stream end for an unfinished record)
	Detail string
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("%v (fragment header at offset %d: %s)", e.Kind, e.Offset, e.Detail)
}

// Unwrap returns the corruption class so errors.Is matches the sentinels.
func (e *CorruptError) Unwrap() error { return e.Kind }
