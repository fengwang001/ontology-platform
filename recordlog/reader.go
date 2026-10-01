package recordlog

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"sync"
)

// Reader reads records sequentially from an io.Reader.
//
// On a corrupt fragment Next returns a *CorruptError whose cause is one
// of ErrLengthOutOfBlock, ErrChecksum, ErrTypeSequence or ErrTruncated.
// The partially assembled record (including the offending fragment) is
// discarded and reading resumes at the start of the block following the
// offending fragment's block. After a stream-end truncation the next
// call returns io.EOF.
type Reader struct {
	mu        sync.Mutex
	r         io.Reader
	blockSize int

	off      int64 // absolute bytes consumed from the stream
	eof      bool
	inRecord bool
	buf      []byte // fragments of the record being assembled
	recStart int64  // offset of the first fragment of the record
}

// NewReader creates a Reader over r with blocks of blockSize bytes.
func NewReader(r io.Reader, blockSize int) (*Reader, error) {
	if blockSize <= HeaderSize || blockSize > MaxBlockSize {
		return nil, &invalidSizeError{blockSize: blockSize}
	}
	return &Reader{r: r, blockSize: blockSize}, nil
}

// Next returns the next record, the start offset of its first fragment
// header, and an error. A clean end of stream is reported as io.EOF.
func (r *Reader) Next() (record []byte, offset int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.eof {
		return nil, 0, io.EOF
	}

	for {
		fragOff, typ, data, perr := r.readFragment()
		if perr != nil {
			if perr == io.EOF {
				if r.inRecord {
					r.inRecord = false
					r.buf = r.buf[:0]
					r.eof = true
					return nil, r.recStart, &CorruptError{Offset: r.recStart, Err: ErrTruncated}
				}
				r.eof = true
				return nil, 0, io.EOF
			}
			if ce, ok := perr.(*CorruptError); ok && ce.Err == ErrTruncated {
				// The stream ended mid-fragment; no recovery possible.
				r.inRecord = false
				r.buf = r.buf[:0]
				r.eof = true
			}
			return nil, fragOff, perr
		}

		switch typ {
		case TypeFull:
			if r.inRecord {
				r.fail(fragOff)
				return nil, fragOff, &CorruptError{Offset: fragOff, Err: ErrTypeSequence}
			}
			return data, fragOff, nil
		case TypeFirst:
			if r.inRecord {
				r.fail(fragOff)
				return nil, fragOff, &CorruptError{Offset: fragOff, Err: ErrTypeSequence}
			}
			r.inRecord = true
			r.recStart = fragOff
			r.buf = append(r.buf[:0], data...)
		case TypeMiddle:
			if !r.inRecord {
				r.fail(fragOff)
				return nil, fragOff, &CorruptError{Offset: fragOff, Err: ErrTypeSequence}
			}
			r.buf = append(r.buf, data...)
		case TypeLast:
			if !r.inRecord {
				r.fail(fragOff)
				return nil, fragOff, &CorruptError{Offset: fragOff, Err: ErrTypeSequence}
			}
			r.buf = append(r.buf, data...)
			rec := r.buf
			off := r.recStart
			r.buf = nil
			r.inRecord = false
			return rec, off, nil
		default:
			r.fail(fragOff)
			return nil, fragOff, &CorruptError{Offset: fragOff, Err: ErrTypeSequence}
		}
	}
}

// fail discards the record being assembled (including the offending
// fragment) and skips to the start of the next block.
func (r *Reader) fail(fragOff int64) {
	r.inRecord = false
	r.buf = r.buf[:0]
	blockStart := fragOff - fragOff%int64(r.blockSize)
	nextBlock := blockStart + int64(r.blockSize)
	// Only skip within bytes actually present. If the stream ends
	// before the next block boundary, the end-of-block scan must still
	// observe the missing fragment header/data (truncation); therefore
	// never discard past the available tail.
	r.discard(nextBlock - r.off)
}

// readFragment reads the next fragment, honoring block padding.
// A returned io.EOF means a clean boundary (no more fragments at all).
func (r *Reader) readFragment() (fragOff int64, typ byte, data []byte, err error) {
	rem := r.blockSize - int(r.off%int64(r.blockSize))

	// Less than a header left in the block: padding. Skip it.
	if rem < HeaderSize {
		if n := r.discard(int64(rem)); n < int64(rem) {
			// Ending inside a padding region is a clean end of stream.
			return 0, 0, nil, io.EOF
		}
	}

	fragOff = r.off

	var header [HeaderSize]byte
	if err := r.readFull(header[:]); err != nil {
		if err == io.EOF {
			return 0, 0, nil, io.EOF
		}
		return fragOff, 0, nil, &CorruptError{Offset: fragOff, Err: ErrTruncated}
	}

	crc := binary.LittleEndian.Uint32(header[0:4])
	length := int(binary.LittleEndian.Uint16(header[4:6]))
	typ = header[6]

	blockRem := r.blockSize - int(r.off%int64(r.blockSize))
	if length > blockRem {
		// Header-only verdict; do not touch the data area.
		r.fail(fragOff)
		return fragOff, 0, nil, &CorruptError{Offset: fragOff, Err: ErrLengthOutOfBlock}
	}

	data = make([]byte, length)
	if err := r.readFull(data); err != nil {
		return fragOff, 0, nil, &CorruptError{Offset: fragOff, Err: ErrTruncated}
	}

	got := crc32.ChecksumIEEE(append([]byte{typ}, data...))
	if got != crc {
		r.fail(fragOff)
		return fragOff, 0, nil, &CorruptError{Offset: fragOff, Err: ErrChecksum}
	}

	return fragOff, typ, data, nil
}

func (r *Reader) readFull(p []byte) error {
	n, err := io.ReadFull(r.r, p)
	r.off += int64(n)
	return err
}

// discard skips up to n bytes and reports how many were skipped.
func (r *Reader) discard(n int64) int64 {
	if n <= 0 {
		return 0
	}
	var skip [512]byte
	remaining := n
	skipped := int64(0)
	for remaining > 0 {
		m := remaining
		if m > int64(len(skip)) {
			m = int64(len(skip))
		}
		nr, err := io.ReadFull(r.r, skip[:m])
		r.off += int64(nr)
		remaining -= int64(nr)
		skipped += int64(nr)
		if err != nil {
			return skipped
		}
	}
	return skipped
}
