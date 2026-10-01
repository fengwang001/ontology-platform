package fragmentlog

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"sync"
)

const (
	// HeaderSize is the fragment header length in bytes.
	HeaderSize = 7
	// MaxBlockSize is the largest legal block size.
	MaxBlockSize = 65535
	// MaxRecord is the largest record Append accepts, in bytes.
	MaxRecord = 1048576
)

// Fragment types.
const (
	TypeFull   byte = 1 // the whole record fits in this fragment
	TypeFirst  byte = 2 // first fragment of a multi-fragment record
	TypeMiddle byte = 3 // middle fragment of a multi-fragment record
	TypeLast   byte = 4 // last fragment of a multi-fragment record
)

// Writer appends block-aligned fragmented records to an underlying stream.
// It is safe for concurrent use; concurrent Appends behave as if executed
// in some serial order, and each Append is atomic with respect to others.
type Writer struct {
	mu        sync.Mutex
	w         io.Writer
	blockSize int
	off       int64 // total bytes written so far
	err       error // sticky underlying-write error
}

// NewWriter returns a Writer emitting blocks of blockSize bytes. It fails
// with ErrBlockSizeTooSmall or ErrBlockSizeTooLarge for illegal sizes.
func NewWriter(w io.Writer, blockSize int) (*Writer, error) {
	if blockSize <= HeaderSize {
		return nil, ErrBlockSizeTooSmall
	}
	if blockSize > MaxBlockSize {
		return nil, ErrBlockSizeTooLarge
	}
	return &Writer{w: w, blockSize: blockSize}, nil
}

// Offset reports the number of bytes written so far.
func (w *Writer) Offset() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.off
}

// Append writes rec as one or more block-aligned fragments and returns the
// offset of the record's first fragment header (after any zero padding).
// Records longer than MaxRecord are rejected with ErrRecordTooLarge and
// leave the written bytes and offset untouched.
func (w *Writer) Append(rec []byte) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(rec) > MaxRecord {
		return 0, ErrRecordTooLarge
	}
	if w.err != nil {
		return 0, w.err
	}

	rem := w.blockSize - int(w.off%int64(w.blockSize))
	if rem < HeaderSize {
		if err := w.writeFull(make([]byte, rem)); err != nil {
			return 0, err
		}
		rem = w.blockSize
	}

	start := w.off
	written := 0
	for first := true; first || written < len(rec); {
		isFirst := first
		first = false
		if rem == 0 {
			rem = w.blockSize
		}
		n := rem - HeaderSize
		if left := len(rec) - written; n > left {
			n = left
		}
		var typ byte
		switch {
		case isFirst && written+n == len(rec):
			typ = TypeFull
		case isFirst:
			typ = TypeFirst
		case written+n == len(rec):
			typ = TypeLast
		default:
			typ = TypeMiddle
		}
		data := rec[written : written+n]
		hdr := make([]byte, HeaderSize)
		crc := crc32.ChecksumIEEE(append([]byte{typ}, data...))
		binary.LittleEndian.PutUint32(hdr[0:4], crc)
		binary.LittleEndian.PutUint16(hdr[4:6], uint16(n))
		hdr[6] = typ
		if err := w.writeFull(hdr); err != nil {
			return 0, err
		}
		if err := w.writeFull(data); err != nil {
			return 0, err
		}
		written += n
		rem -= HeaderSize + n
	}
	return start, nil
}

func (w *Writer) writeFull(p []byte) error {
	for len(p) > 0 {
		n, err := w.w.Write(p)
		w.off += int64(n)
		p = p[n:]
		if err != nil {
			w.err = err
			return err
		}
		if n == 0 {
			w.err = io.ErrShortWrite
			return w.err
		}
	}
	return nil
}
