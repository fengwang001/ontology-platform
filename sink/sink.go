// Package sink writes records to a self-describing length-prefixed, CRC32
// protected binary file and classifies corruption on recovery.
package sink

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"

	"ontology/record"
)

var (
	magic    = [5]byte{'M', 'L', 'O', 'G', '1'}
	footerV1 = [8]byte{0xFF, 0xFF, 0xFF, 0xFF, 'E', 'N', 'D', '!'}
	ieee     = crc32.IEEETable
)

// Corruption errors, distinguishable with errors.Is.
var (
	// ErrHeader means the self-describing header is missing/truncated/bad.
	ErrHeader = errors.New("sink: header incomplete or invalid")
	// ErrLengthPrefix means a 4-byte length (or the end marker) is truncated.
	ErrLengthPrefix = errors.New("sink: length prefix incomplete")
	// ErrBodyTruncated means the declared record body or its CRC is cut short.
	ErrBodyTruncated = errors.New("sink: record body truncated")
	// ErrCRC means a complete frame whose checksum does not match.
	ErrCRC = errors.New("sink: crc mismatch")
)

const headerLen = 8 // magic(5) + version(1) + depth(2)

// Writer appends framed records to an underlying writer.
type Writer struct {
	w      io.Writer
	closed bool
}

// NewWriter writes the self-describing header and returns a Writer.
func NewWriter(w io.Writer) (*Writer, error) {
	hdr := make([]byte, headerLen)
	copy(hdr, magic[:])
	hdr[5] = 1
	binary.BigEndian.PutUint16(hdr[6:], uint16(record.MaxDepth))
	if _, err := w.Write(hdr); err != nil {
		return nil, err
	}
	return &Writer{w: w}, nil
}

// Write appends one framed record: u32BE(len) | body | u32BE(crc32(body)).
func (w *Writer) Write(r *record.Record) error {
	body, err := r.Encode()
	if err != nil {
		return err
	}
	frame := make([]byte, 4+len(body)+4)
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	sum := crc32.Checksum(body, ieee)
	binary.BigEndian.PutUint32(frame[4+len(body):], sum)
	_, err = w.w.Write(frame)
	return err
}

// Close writes the end marker so a frame-boundary cut is detectable.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	_, err := w.w.Write(footerV1[:])
	return err
}

// WriteFile writes records to a path.
func WriteFile(path string, recs []*record.Record) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w, err := NewWriter(f)
	if err != nil {
		f.Close()
		return err
	}
	for _, r := range recs {
		if err := w.Write(r); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadAll recovers every intact prefix frame from data. It returns recovered
// records plus the first corruption error encountered (classified).
func ReadAll(data []byte) ([]*record.Record, error) {
	if len(data) < headerLen || [5]byte(data[0:5]) != magic || data[5] != 1 {
		return nil, fmt.Errorf("%w: need %d valid header bytes", ErrHeader, headerLen)
	}
	pos := headerLen
	var out []*record.Record
	for {
		if pos == len(data) {
			return out, fmt.Errorf("%w: cut at frame boundary, end marker absent", ErrLengthPrefix)
		}
		if len(data)-pos < 4 {
			return out, fmt.Errorf("%w: %d trailing byte(s) at offset %d",
				ErrLengthPrefix, len(data)-pos, pos)
		}
		prefix := binary.BigEndian.Uint32(data[pos : pos+4])
		if prefix == 0xFFFFFFFF {
			tail := data[pos+4:]
			if len(tail) >= 4 && string(tail[:4]) == "END!" {
				return out, nil
			}
			return out, fmt.Errorf("%w: end marker truncated (%d/4 bytes)",
				ErrLengthPrefix, len(tail))
		}
		n := int(prefix)
		bodyStart := pos + 4
		if len(data)-bodyStart < n+4 {
			have := len(data) - bodyStart
			if have < n {
				return out, fmt.Errorf("%w: have %d/%d body bytes at offset %d",
					ErrBodyTruncated, max(have, 0), n, bodyStart)
			}
			return out, fmt.Errorf("%w: crc trailer cut at offset %d",
				ErrBodyTruncated, bodyStart+n)
		}
		body := data[bodyStart : bodyStart+n]
		wantCRC := binary.BigEndian.Uint32(data[bodyStart+n : bodyStart+n+4])
		if crc32.Checksum(body, ieee) != wantCRC {
			return out, fmt.Errorf("%w: frame at offset %d", ErrCRC, pos)
		}
		r, err := record.Decode(body)
		if err != nil {
			return out, fmt.Errorf("%w: frame at offset %d: %v", ErrCRC, pos, err)
		}
		out = append(out, r)
		pos = bodyStart + n + 4
	}
}

// ReadFile loads a file written by WriteFile.
func ReadFile(path string) ([]*record.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadAll(data)
}
