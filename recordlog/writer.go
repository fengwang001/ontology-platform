package recordlog

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
)

// Writer appends records to an underlying io.Writer, splitting each
// record into fragments that never cross block boundaries.
//
// All methods are safe for concurrent use; Append calls serialize on
// an internal mutex.
type Writer struct {
	mu        sync.Mutex
	w         io.Writer
	blockSize int
	off       int64
}

// NewWriter creates a Writer writing to w with blocks of blockSize bytes.
// It returns ErrInvalidBlockSize when blockSize <= HeaderSize or
// blockSize > MaxBlockSize.
func NewWriter(w io.Writer, blockSize int) (*Writer, error) {
	if blockSize <= HeaderSize || blockSize > MaxBlockSize {
		return nil, &invalidSizeError{blockSize: blockSize}
	}
	return &Writer{w: w, blockSize: blockSize}, nil
}

type invalidSizeError struct{ blockSize int }

func (e *invalidSizeError) Error() string {
	return fmt.Sprintf("recordlog: invalid block size %d (must be > %d and <= %d)", e.blockSize, HeaderSize, MaxBlockSize)
}

func (e *invalidSizeError) Is(target error) bool { return target == ErrInvalidBlockSize }

// Offset returns the total number of bytes written so far, including
// zero padding between blocks.
func (w *Writer) Offset() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.off
}

// Append writes record as one full fragment or a sequence of
// first/middle/last fragments. It returns the start offset of the
// record's first fragment header (after any padding).
//
// A record longer than MaxRecord is rejected without writing a byte.
func (w *Writer) Append(record []byte) (int64, error) {
	if len(record) > MaxRecord {
		return 0, ErrRecordTooLarge
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	remaining := func() int {
		return w.blockSize - int(w.off%int64(w.blockSize))
	}

	writeBytes := func(p []byte) error {
		n, err := w.w.Write(p)
		if err != nil {
			return err
		}
		if n != len(p) {
			return io.ErrShortWrite
		}
		w.off += int64(n)
		return nil
	}

	writeFragment := func(typ byte, data []byte) error {
		var header [HeaderSize]byte
		binary.LittleEndian.PutUint32(header[0:4], fragmentCRC(typ, data))
		binary.LittleEndian.PutUint16(header[4:6], uint16(len(data)))
		header[6] = typ
		if err := writeBytes(header[:]); err != nil {
			return err
		}
		return writeBytes(data)
	}

	rest := record
	first := true
	var start int64
	for {
		rem := remaining()
		if rem < HeaderSize {
			if err := writeBytes(make([]byte, rem)); err != nil {
				return 0, err
			}
			rem = w.blockSize
		}
		if first {
			start = w.off
		}

		n := len(rest)
		if avail := rem - HeaderSize; n > avail {
			n = avail
		}

		var typ byte
		switch {
		case first && n == len(rest):
			typ = TypeFull
		case first:
			typ = TypeFirst
		case n == len(rest):
			typ = TypeLast
		default:
			typ = TypeMiddle
		}

		if err := writeFragment(typ, rest[:n]); err != nil {
			return 0, err
		}
		rest = rest[n:]
		if first {
			first = false
		}
		if len(rest) == 0 {
			return start, nil
		}
	}
}

func fragmentCRC(typ byte, data []byte) uint32 {
	h := crc32.NewIEEE()
	h.Write([]byte{typ})
	h.Write(data)
	return h.Sum32()
}
