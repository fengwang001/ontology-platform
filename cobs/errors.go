package cobs

import "errors"

var (
	// ErrDataTooLong means a payload exceeds MaxData.
	ErrDataTooLong = errors.New("cobs: data exceeds MaxData")
	// ErrFrameTooLong means encoded frame bytes exceed MaxFrame.
	ErrFrameTooLong = errors.New("cobs: frame exceeds MaxFrame")
	// ErrTruncatedBlock means a code byte requests bytes past frame end.
	ErrTruncatedBlock = errors.New("cobs: block is truncated by frame end")
	// ErrNonCanonical means decoded data does not re-encode to the frame.
	ErrNonCanonical = errors.New("cobs: frame is not canonical COBS")
)
