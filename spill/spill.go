// Package spill writes sorted record batches to run files and reads them
// back with per-record CRC32 verification.
package spill

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"

	"ontology/record"
)

// Recover classifies a truncated/corrupt run file into one of these.
var (
	ErrHeaderIncomplete       = errors.New("spill: header incomplete")
	ErrLengthPrefixIncomplete = errors.New("spill: length prefix incomplete")
	ErrRecordBodyIncomplete   = errors.New("spill: record body incomplete")
	ErrCRCMismatch            = errors.New("spill: crc mismatch")
	ErrBadMagic               = errors.New("spill: bad magic or version")
)

const (
	magic      = "OSRT"
	version    = 1
	HeaderSize = 4 + 2 + 8 // magic + version + count
	prefixSize = 4         // uint32 LE payload length
	crcSize    = 4         // CRC32-IEEE of payload
)

// WriteRun writes recs (already sorted) as one run file to w.
func WriteRun(w io.Writer, recs []record.Record) error {
	hdr := make([]byte, 0, HeaderSize)
	hdr = append(hdr, magic...)
	hdr = binary.LittleEndian.AppendUint16(hdr, version)
	hdr = binary.LittleEndian.AppendUint64(hdr, uint64(len(recs)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, r := range recs {
		payload := r.Encode(nil)
		var pre [prefixSize]byte
		binary.LittleEndian.PutUint32(pre[:], uint32(len(payload)))
		if _, err := w.Write(pre[:]); err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
		var crc [crcSize]byte
		binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
		if _, err := w.Write(crc[:]); err != nil {
			return err
		}
	}
	return nil
}

// ReadRun reads a complete run file, verifying count and every CRC.
func ReadRun(r io.Reader) ([]record.Record, error) {
	recs, err := Recover(r)
	if err != nil {
		return nil, err
	}
	return recs, nil
}

// Recover reads the maximal recoverable prefix of a (possibly truncated)
// run file. Complete, CRC-valid records are returned; the first failure is
// classified as one of the package errors. A nil error means the file was
// complete and consistent with its header count.
func Recover(r io.Reader) ([]record.Record, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < HeaderSize {
		return nil, ErrHeaderIncomplete
	}
	if string(data[:4]) != magic || binary.LittleEndian.Uint16(data[4:6]) != version {
		return nil, ErrBadMagic
	}
	count := binary.LittleEndian.Uint64(data[6:14])
	off := HeaderSize
	recs := make([]record.Record, 0, count)
	for i := uint64(0); i < count; i++ {
		if len(data)-off < prefixSize {
			return recs, ErrLengthPrefixIncomplete
		}
		plen := int(binary.LittleEndian.Uint32(data[off : off+prefixSize]))
		off += prefixSize
		if len(data)-off < plen+crcSize {
			return recs, ErrRecordBodyIncomplete
		}
		payload := data[off : off+plen]
		crc := binary.LittleEndian.Uint32(data[off+plen : off+plen+crcSize])
		if crc32.ChecksumIEEE(payload) != crc {
			return recs, ErrCRCMismatch
		}
		rec, _, err := record.Decode(payload)
		if err != nil {
			return recs, fmt.Errorf("%w: %v", ErrRecordBodyIncomplete, err)
		}
		recs = append(recs, rec)
		off += plen + crcSize
	}
	return recs, nil
}

// WriteRunTruncated is a test-only fault-injection helper: it encodes recs
// as a run file and returns only the first cutAt bytes.
func WriteRunTruncated(recs []record.Record, cutAt int) ([]byte, error) {
	buf := &truncBuf{}
	if err := WriteRun(buf, recs); err != nil {
		return nil, err
	}
	if cutAt > len(buf.b) {
		cutAt = len(buf.b)
	}
	return buf.b[:cutAt], nil
}

type truncBuf struct{ b []byte }

func (t *truncBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	return len(p), nil
}
