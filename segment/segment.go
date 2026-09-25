// Package segment implements append-only segment files: a
// self-describing header followed by length-prefixed, CRC32-protected
// event records.
package segment

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Sentinel errors classifying segment read failures; use errors.Is.
var (
	ErrHeaderIncomplete       = errors.New("segment: header incomplete")
	ErrLengthPrefixIncomplete = errors.New("segment: length prefix incomplete")
	ErrLengthPrefixInvalid    = errors.New("segment: length prefix invalid")
	ErrEventBodyIncomplete    = errors.New("segment: event body incomplete")
	ErrCRCMismatch            = errors.New("segment: crc mismatch")
	ErrBadMagic               = errors.New("segment: bad magic")
)

const (
	// HeaderSize is the byte length of the segment header.
	HeaderSize = 28
	// MaxRecordSize caps a single encoded event to reject garbage
	// length prefixes without huge allocations.
	MaxRecordSize = 1 << 26

	magic       = "OSEG"
	lenSize     = 4
	crcSize     = 4
	countOffset = 20 // magic(4) + indexEvery(8) + firstSeq(8)
)

// Header is the self-describing segment header.
type Header struct {
	IndexEvery uint64
	FirstSeq   uint64
	Count      uint64
}

// RecordSize returns the on-disk size of a record wrapping an encoded
// event of the given length.
func RecordSize(encodedLen int) int64 {
	return int64(lenSize + encodedLen + crcSize)
}

// Writer appends records to a segment file.
type Writer struct {
	f   *os.File
	hdr Header
	end int64
}

// Create starts a new segment file at path.
func Create(path string, indexEvery, firstSeq uint64) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, hdr: Header{IndexEvery: indexEvery, FirstSeq: firstSeq}, end: HeaderSize}
	if err := w.writeHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *Writer) writeHeader() error {
	buf := make([]byte, HeaderSize)
	copy(buf, magic)
	binary.BigEndian.PutUint64(buf[4:], w.hdr.IndexEvery)
	binary.BigEndian.PutUint64(buf[12:], w.hdr.FirstSeq)
	binary.BigEndian.PutUint64(buf[countOffset:], w.hdr.Count)
	_, err := w.f.WriteAt(buf, 0)
	return err
}

// Append writes one encoded event and updates the header count.
func (w *Writer) Append(encoded []byte) error {
	rec := make([]byte, RecordSize(len(encoded)))
	binary.BigEndian.PutUint32(rec, uint32(len(encoded)))
	copy(rec[lenSize:], encoded)
	binary.BigEndian.PutUint32(rec[len(rec)-crcSize:], crc32.ChecksumIEEE(encoded))
	if _, err := w.f.WriteAt(rec, w.end); err != nil {
		return err
	}
	w.end += int64(len(rec))
	w.hdr.Count++
	return w.writeHeader()
}

// Header returns the current header.
func (w *Writer) Header() Header { return w.hdr }

// Close flushes and closes the file.
func (w *Writer) Close() error { return w.f.Close() }

// SetCount rewrites the count field of an open segment file; repair
// uses it to correct the header after truncation.
func SetCount(f *os.File, count uint64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], count)
	_, err := f.WriteAt(buf[:], countOffset)
	return err
}

// ReadHeader reads and validates the segment header.
func ReadHeader(r io.ReaderAt) (Header, error) {
	buf := make([]byte, HeaderSize)
	if n, err := r.ReadAt(buf, 0); n < HeaderSize {
		return Header{}, fmt.Errorf("%w: %d/%d bytes", ErrHeaderIncomplete, n, HeaderSize)
	} else if err != nil {
		return Header{}, err
	}
	if string(buf[:4]) != magic {
		return Header{}, ErrBadMagic
	}
	return Header{
		IndexEvery: binary.BigEndian.Uint64(buf[4:]),
		FirstSeq:   binary.BigEndian.Uint64(buf[12:]),
		Count:      binary.BigEndian.Uint64(buf[countOffset:]),
	}, nil
}

// ReadRecordAt reads the record starting at off, returning the encoded
// event and the offset of the next record.
func ReadRecordAt(r io.ReaderAt, off int64) ([]byte, int64, error) {
	lenBuf := make([]byte, lenSize)
	if n, _ := r.ReadAt(lenBuf, off); n < lenSize {
		return nil, off, fmt.Errorf("%w at %d: %d/%d bytes", ErrLengthPrefixIncomplete, off, n, lenSize)
	}
	n := binary.BigEndian.Uint32(lenBuf)
	if n == 0 || n > MaxRecordSize {
		return nil, off, fmt.Errorf("%w at %d: %d", ErrLengthPrefixInvalid, off, n)
	}
	body := make([]byte, int64(n)+crcSize)
	if got, _ := r.ReadAt(body, off+lenSize); got < len(body) {
		return nil, off, fmt.Errorf("%w at %d: %d/%d bytes", ErrEventBodyIncomplete, off, got, len(body))
	}
	encoded := body[:n]
	if crc32.ChecksumIEEE(encoded) != binary.BigEndian.Uint32(body[n:]) {
		return nil, off, fmt.Errorf("%w at %d", ErrCRCMismatch, off)
	}
	return encoded, off + RecordSize(int(n)), nil
}

// Scan reads hdr.Count records from the segment at path, invoking visit
// with each record's offset and encoded event. It returns the header,
// the number of complete records read, and the offset where scanning
// stopped (clean end on nil error, failing record start otherwise).
func Scan(path string, visit func(off int64, encoded []byte) error) (Header, uint64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, 0, 0, err
	}
	defer f.Close()
	hdr, err := ReadHeader(f)
	if err != nil {
		return hdr, 0, 0, err
	}
	off := int64(HeaderSize)
	var good uint64
	for good < hdr.Count {
		encoded, next, err := ReadRecordAt(f, off)
		if err != nil {
			return hdr, good, off, err
		}
		if visit != nil {
			if err := visit(off, encoded); err != nil {
				return hdr, good, off, err
			}
		}
		off = next
		good++
	}
	return hdr, good, off, nil
}
