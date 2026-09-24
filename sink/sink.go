// Package sink writes records to a self-describing length-prefixed
// CRC32-protected file and reads them back with precise corruption
// classification.
//
// Layout: MAGIC(8) | [ payloadLen uint32 LE | crc32 uint32 LE | payload ]*
package sink

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"

	"ontology/record"
)

// Magic is the 8-byte self-describing file header.
var Magic = []byte("ONTSINK1")

// prefixLen is len(4) + crc32(4) preceding each record payload.
const prefixLen = 8

var (
	// ErrHeader: fewer than len(Magic) bytes, or bad magic.
	ErrHeader = errors.New("sink: incomplete header")
	// ErrPrefix: a record started but its length prefix is incomplete.
	ErrPrefix = errors.New("sink: incomplete length prefix")
	// ErrBody: prefix complete but payload shorter than declared.
	ErrBody = errors.New("sink: incomplete record body")
	// ErrCRC: payload complete but CRC32 does not match.
	ErrCRC = errors.New("sink: crc mismatch")
)

// Encode serializes records into the sink byte format.
func Encode(recs []*record.Record) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(Magic)
	for _, r := range recs {
		b, err := r.Encode()
		if err != nil {
			return nil, err
		}
		var p [prefixLen]byte
		binary.LittleEndian.PutUint32(p[0:4], uint32(len(b)))
		binary.LittleEndian.PutUint32(p[4:8], crc32.ChecksumIEEE(b))
		buf.Write(p[:])
		buf.Write(b)
	}
	return buf.Bytes(), nil
}

// WriteFile encodes records and writes them to path.
func WriteFile(path string, recs []*record.Record) error {
	data, err := Encode(recs)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ReadFile reads and parses a sink file.
func ReadFile(path string) ([]*record.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes a sink buffer, returning all records up to the first
// corruption point plus a classifiable error (nil on clean input,
// including truncation exactly at a record boundary).
func Parse(data []byte) ([]*record.Record, error) {
	if len(data) < len(Magic) {
		return nil, ErrHeader
	}
	if !bytes.Equal(data[:len(Magic)], Magic) {
		return nil, ErrHeader
	}
	var recs []*record.Record
	off := len(Magic)
	for off < len(data) {
		if len(data)-off < prefixLen {
			return recs, ErrPrefix
		}
		n := binary.LittleEndian.Uint32(data[off:])
		crc := binary.LittleEndian.Uint32(data[off+4:])
		off += prefixLen
		if uint64(len(data)-off) < uint64(n) {
			return recs, ErrBody
		}
		body := data[off : off+int(n)]
		off += int(n)
		if crc32.ChecksumIEEE(body) != crc {
			return recs, ErrCRC
		}
		r, err := record.Decode(body)
		if err != nil {
			return recs, err
		}
		recs = append(recs, r)
	}
	return recs, nil
}
