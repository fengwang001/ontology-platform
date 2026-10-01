package fragmentlog

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
)

// Reader reads records back from a fragmented, block-aligned stream.
// Next is safe for concurrent use; concurrent calls behave as if executed
// in some serial order.
type Reader struct {
	mu        sync.Mutex
	r         io.Reader
	blockSize int
	pos       int64 // absolute offset of the next unread byte

	inRecord bool   // a first/middle fragment was seen without its last
	startOff int64  // header offset of the in-progress record's first fragment
	asm      []byte // data assembled so far for the in-progress record

	eof bool // stream exhausted (cleanly or after a reported truncation)
}

// NewReader returns a Reader for streams written with the given block size.
func NewReader(r io.Reader, blockSize int) (*Reader, error) {
	if blockSize <= HeaderSize {
		return nil, ErrBlockSizeTooSmall
	}
	if blockSize > MaxBlockSize {
		return nil, ErrBlockSizeTooLarge
	}
	return &Reader{r: r, blockSize: blockSize}, nil
}

// Next returns the next complete record, the offset of its first fragment
// header, and a nil error. On a clean end of stream it returns io.EOF.
//
// On corruption it returns a *CorruptError carrying the offset of the
// offending fragment header; the partially assembled record (including the
// offending fragment) is discarded and reading resumes at the next block
// boundary after that fragment's block. After a truncation error the
// following Next returns io.EOF.
func (rd *Reader) Next() (rec []byte, off int64, err error) {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	if rd.eof {
		return nil, 0, io.EOF
	}
	for {
		rem := rd.blockSize - int(rd.pos%int64(rd.blockSize))
		if rem < HeaderSize {
			// Fewer than a header's bytes left in the block: padding.
			n, _ := io.CopyN(io.Discard, rd.r, int64(rem))
			rd.pos += n
			if n < int64(rem) {
				// Stream ends inside padding: no fragment is lost.
				return rd.endOfStream()
			}
			continue
		}

		hdrOff := rd.pos
		hdr := make([]byte, HeaderSize)
		n, err := io.ReadFull(rd.r, hdr)
		rd.pos += int64(n)
		if err != nil {
			if n == 0 {
				return rd.endOfStream()
			}
			// Partial header: the stream ends mid-fragment.
			rd.eof = true
			return nil, hdrOff, &CorruptError{ErrTruncated, hdrOff,
				fmt.Sprintf("only %d of %d header bytes available", n, HeaderSize)}
		}

		length := int(binary.LittleEndian.Uint16(hdr[4:6]))
		typ := hdr[6]

		// 1. Length reaching past the block is judged from the header
		//    alone, before any data byte is read.
		if HeaderSize+length > rem {
			rd.resync()
			return nil, hdrOff, &CorruptError{ErrLength, hdrOff,
				fmt.Sprintf("data length %d with only %d bytes left in block", length, rem-HeaderSize)}
		}

		data := make([]byte, length)
		n, err = io.ReadFull(rd.r, data)
		rd.pos += int64(n)
		if err != nil {
			// 2. Data bytes missing: truncated stream.
			rd.eof = true
			return nil, hdrOff, &CorruptError{ErrTruncated, hdrOff,
				fmt.Sprintf("only %d of %d data bytes available", n, length)}
		}

		// 3. Checksum over the type byte followed by the data.
		want := binary.LittleEndian.Uint32(hdr[0:4])
		got := crc32.ChecksumIEEE(append([]byte{typ}, data...))
		if want != got {
			rd.resync()
			return nil, hdrOff, &CorruptError{ErrChecksum, hdrOff,
				fmt.Sprintf("header crc %#08x, computed %#08x", want, got)}
		}

		// 4. Fragment type sequence.
		if typ < TypeFull || typ > TypeLast {
			rd.resync()
			return nil, hdrOff, &CorruptError{ErrSequence, hdrOff,
				fmt.Sprintf("unknown fragment type %d", typ)}
		}
		if rd.inRecord && (typ == TypeFull || typ == TypeFirst) {
			rd.resync()
			return nil, hdrOff, &CorruptError{ErrSequence, hdrOff,
				"new record starts while the previous one is unfinished"}
		}
		if !rd.inRecord && (typ == TypeMiddle || typ == TypeLast) {
			rd.resync()
			return nil, hdrOff, &CorruptError{ErrSequence, hdrOff,
				"middle/last fragment without a preceding first fragment"}
		}

		switch typ {
		case TypeFull:
			return data, hdrOff, nil
		case TypeFirst:
			rd.inRecord = true
			rd.startOff = hdrOff
			rd.asm = append(rd.asm[:0], data...)
		case TypeMiddle:
			rd.asm = append(rd.asm, data...)
		case TypeLast:
			rec := append(rd.asm, data...)
			off := rd.startOff
			rd.inRecord = false
			rd.asm = nil
			return rec, off, nil
		}
	}
}

// endOfStream handles a stream end at a fragment boundary or inside
// padding: clean io.EOF, or ErrTruncated when a record is unfinished.
func (rd *Reader) endOfStream() ([]byte, int64, error) {
	rd.eof = true
	if rd.inRecord {
		rd.inRecord = false
		rd.asm = nil
		return nil, rd.pos, &CorruptError{ErrTruncated, rd.pos,
			"stream ends with a record still unfinished"}
	}
	return nil, 0, io.EOF
}

// resync discards the in-progress record and skips to the next block
// boundary after the current position (which still lies in the offending
// fragment's block).
func (rd *Reader) resync() {
	rd.inRecord = false
	rd.asm = nil
	skip := rd.blockSize - int(rd.pos%int64(rd.blockSize))
	if skip == rd.blockSize {
		return
	}
	n, _ := io.CopyN(io.Discard, rd.r, int64(skip))
	rd.pos += n
}
