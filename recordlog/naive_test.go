package recordlog

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

var ioEOF = io.EOF

// naiveWriter is a line-by-line transcription of the spec, used only
// by tests to cross-check the real Writer byte-for-byte.
type naiveWriter struct {
	buf       []byte
	blockSize int
}

func newNaiveWriter(blockSize int) *naiveWriter {
	return &naiveWriter{blockSize: blockSize}
}

func (nw *naiveWriter) rem() int {
	return nw.blockSize - len(nw.buf)%nw.blockSize
}

func (nw *naiveWriter) appendRecord(record []byte) int64 {
	remaining := record
	first := true
	var start int64
	for {
		if nw.rem() < 7 {
			nw.buf = append(nw.buf, make([]byte, nw.rem())...)
		}
		if first {
			start = int64(len(nw.buf))
		}
		n := len(remaining)
		if avail := nw.rem() - 7; n > avail {
			n = avail
		}
		data := remaining[:n]
		var typ byte
		switch {
		case first && n == len(remaining):
			typ = TypeFull
		case first:
			typ = TypeFirst
		case n == len(remaining):
			typ = TypeLast
		default:
			typ = TypeMiddle
		}
		h := make([]byte, 7)
		binary.LittleEndian.PutUint32(h[0:4], naiveCRC(typ, data))
		binary.LittleEndian.PutUint16(h[4:6], uint16(len(data)))
		h[6] = typ
		nw.buf = append(nw.buf, h...)
		nw.buf = append(nw.buf, data...)
		remaining = remaining[n:]
		first = false
		if len(remaining) == 0 {
			return start
		}
	}
}

func naiveCRC(typ byte, data []byte) uint32 {
	return crc32.ChecksumIEEE(append([]byte{typ}, data...))
}

// naiveReadResult is one outcome of naiveReader.next.
type naiveReadResult struct {
	record []byte
	offset int64
	err    error // io.EOF or *CorruptError
}

// naiveReader is a spec-transcribing sequential reader. It works on a
// fixed byte slice so truncation is expressed by slicing the input.
type naiveReader struct {
	data      []byte
	blockSize int
	pos       int

	inRecord bool
	buf      []byte
	recStart int
	dead     bool
}

func newNaiveReader(data []byte, blockSize int) *naiveReader {
	return &naiveReader{data: data, blockSize: blockSize}
}

func (nr *naiveReader) next() naiveReadResult {
	if nr.dead {
		return naiveReadResult{err: ioEOF}
	}
	for {
		fragOff, typ, data, err := nr.readFragment()
		if err != nil {
			if err == ioEOF {
				if nr.inRecord {
					off := int64(nr.recStart)
					nr.inRecord = false
					nr.buf = nil
					nr.dead = true
					return naiveReadResult{offset: off, err: &CorruptError{Offset: off, Err: ErrTruncated}}
				}
				nr.dead = true
				return naiveReadResult{err: ioEOF}
			}
			var ce *CorruptError
			if asCorrupt(err, &ce) && ce.Err == ErrTruncated {
				nr.inRecord = false
				nr.buf = nil
				nr.dead = true
			}
			return naiveReadResult{offset: int64(fragOff), err: err}
		}

		switch typ {
		case TypeFull:
			if nr.inRecord {
				nr.fail(fragOff)
				return naiveReadResult{offset: int64(fragOff), err: &CorruptError{Offset: int64(fragOff), Err: ErrTypeSequence}}
			}
			return naiveReadResult{record: data, offset: int64(fragOff)}
		case TypeFirst:
			if nr.inRecord {
				nr.fail(fragOff)
				return naiveReadResult{offset: int64(fragOff), err: &CorruptError{Offset: int64(fragOff), Err: ErrTypeSequence}}
			}
			nr.inRecord = true
			nr.recStart = fragOff
			nr.buf = append(nr.buf[:0], data...)
		case TypeMiddle:
			if !nr.inRecord {
				nr.fail(fragOff)
				return naiveReadResult{offset: int64(fragOff), err: &CorruptError{Offset: int64(fragOff), Err: ErrTypeSequence}}
			}
			nr.buf = append(nr.buf, data...)
		case TypeLast:
			if !nr.inRecord {
				nr.fail(fragOff)
				return naiveReadResult{offset: int64(fragOff), err: &CorruptError{Offset: int64(fragOff), Err: ErrTypeSequence}}
			}
			nr.buf = append(nr.buf, data...)
			rec := nr.buf
			off := int64(nr.recStart)
			nr.buf = nil
			nr.inRecord = false
			return naiveReadResult{record: rec, offset: off}
		default:
			nr.fail(fragOff)
			return naiveReadResult{offset: int64(fragOff), err: &CorruptError{Offset: int64(fragOff), Err: ErrTypeSequence}}
		}
	}
}

func (nr *naiveReader) fail(fragOff int) {
	nr.inRecord = false
	nr.buf = nr.buf[:0]
	nextBlock := (fragOff/nr.blockSize + 1) * nr.blockSize
	nr.pos = nextBlock
	if nr.pos > len(nr.data) {
		nr.pos = len(nr.data)
	}
}

func (nr *naiveReader) readFragment() (fragOff int, typ byte, data []byte, err error) {
	rem := nr.blockSize - nr.pos%nr.blockSize
	if rem < 7 {
		end := nr.pos + rem
		if end >= len(nr.data) {
			// Stream ends inside the trailing padding region: clean.
			nr.pos = len(nr.data)
			return 0, 0, nil, ioEOF
		}
		nr.pos = end
	}
	fragOff = nr.pos
	if nr.pos == len(nr.data) {
		return 0, 0, nil, ioEOF
	}
	if len(nr.data)-nr.pos < 7 {
		// A fragment header position with fewer than 7 bytes present:
		// the stream ended mid-header => truncated.
		nr.pos = len(nr.data)
		return fragOff, 0, nil, &CorruptError{Offset: int64(fragOff), Err: ErrTruncated}
	}
	h := nr.data[nr.pos : nr.pos+7]
	crc := binary.LittleEndian.Uint32(h[0:4])
	length := int(binary.LittleEndian.Uint16(h[4:6]))
	typ = h[6]
	nr.pos += 7
	blockRem := nr.blockSize - nr.pos%nr.blockSize
	if length > blockRem {
		nr.fail(fragOff)
		return fragOff, 0, nil, &CorruptError{Offset: int64(fragOff), Err: ErrLengthOutOfBlock}
	}
	if len(nr.data)-nr.pos < length {
		nr.pos = len(nr.data)
		return fragOff, 0, nil, &CorruptError{Offset: int64(fragOff), Err: ErrTruncated}
	}
	data = nr.data[nr.pos : nr.pos+length]
	nr.pos += length
	if naiveCRC(typ, data) != crc {
		nr.fail(fragOff)
		return fragOff, 0, nil, &CorruptError{Offset: int64(fragOff), Err: ErrChecksum}
	}
	return fragOff, typ, data, nil
}

// drainNaiveReader consumes all outcomes for cross-checking.
func drainNaiveReader(data []byte, blockSize int) []naiveReadResult {
	nr := newNaiveReader(data, blockSize)
	var results []naiveReadResult
	for {
		res := nr.next()
		results = append(results, res)
		if res.err != nil {
			if res.err == ioEOF {
				break
			}
			var ce *CorruptError
			if asCorrupt(res.err, &ce) && ce.Err == ErrTruncated {
				// Truncation is terminal; next() must be EOF.
				results = append(results, nr.next())
				break
			}
		}
	}
	return results
}

func asCorrupt(err error, target **CorruptError) bool {
	if ce, ok := err.(*CorruptError); ok {
		*target = ce
		return true
	}
	return false
}

func formatResults(rs []naiveReadResult) string {
	var b bytes.Buffer
	for i, res := range rs {
		fmt.Fprintf(&b, "  [%d] off=%d recLen=%d err=%v\n", i, res.offset, len(res.record), res.err)
	}
	return b.String()
}
