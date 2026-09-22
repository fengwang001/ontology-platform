package spill

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"ontology/record"
)

// Reader streams one run file record by record, validating every frame.
type Reader struct {
	f     *os.File
	r     *bufio.Reader
	h     Header
	index uint64 // records already emitted
}

// Open opens and validates the header of a run file. A zero-length file
// is reported with ErrEmptyFile so callers can tell "no run" apart from
// a valid zero-record run (which has a 32-byte header and Count == 0).
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(f, 1<<16)
	head := make([]byte, HeaderSize)
	n, err := io.ReadFull(br, head)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		f.Close()
		if n == 0 && errors.Is(err, io.EOF) {
			return nil, ErrEmptyFile
		}
		return nil, ErrHeaderIncomplete
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	h, err := decodeHeader(head)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Reader{f: f, r: br, h: h}, nil
}

// Header returns the validated run header.
func (rd *Reader) Header() Header { return rd.h }

// Close releases the underlying file.
func (rd *Reader) Close() error { return rd.f.Close() }

// ReadRecord returns the next record or io.EOF after the announced
// record count has been delivered. Any other error is one of the
// definitive classes in errors.go; records already delivered before
// such an error form the maximum recoverable prefix.
func (rd *Reader) ReadRecord() (record.Record, error) {
	if rd.index >= rd.h.Count {
		if _, err := rd.r.ReadByte(); err == io.EOF {
			return record.Record{}, io.EOF
		} else if err != nil {
			return record.Record{}, err
		}
		return record.Record{}, ErrTrailingData
	}
	var lenBuf [4]byte
	n, err := io.ReadFull(rd.r, lenBuf[:])
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		if n == 0 && errors.Is(err, io.EOF) {
			return record.Record{}, ErrLengthPrefixIncomplete
		}
		return record.Record{}, ErrLengthPrefixIncomplete
	}
	if err != nil {
		return record.Record{}, err
	}
	bodyLen := binary.BigEndian.Uint32(lenBuf[:])
	if bodyLen < crcLen {
		return record.Record{}, ErrCRC
	}
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(rd.r, body); err != nil {
		return record.Record{}, ErrRecordIncomplete
	}
	frame := body[:len(body)-crcLen]
	wantCRC := binary.BigEndian.Uint32(body[len(body)-crcLen:])
	if crc32Value(frame) != wantCRC {
		return record.Record{}, ErrCRC
	}
	rec, consumed, err := record.DecodeFrame(frame)
	if err != nil || consumed != len(frame) {
		return record.Record{}, ErrCRC
	}
	rd.index++
	return rec, nil
}
