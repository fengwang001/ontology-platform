package spill

import (
	"encoding/binary"
	"hash/crc32"
	"io"

	"ontology/record"
)

// RunReader streams records from a run file one at a time, verifying each
// record's CRC32. It implements merge.Source without loading the run.
type RunReader struct {
	r         io.Reader
	remaining uint64
}

// NewRunReader reads and validates the run header from r.
func NewRunReader(r io.Reader) (*RunReader, error) {
	hdr := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, ErrHeaderIncomplete
	}
	if string(hdr[:4]) != magic || binary.LittleEndian.Uint16(hdr[4:6]) != version {
		return nil, ErrBadMagic
	}
	return &RunReader{r: r, remaining: binary.LittleEndian.Uint64(hdr[6:14])}, nil
}

// Next returns the next record, or io.EOF when the run is exhausted.
func (rr *RunReader) Next() (record.Record, error) {
	if rr.remaining == 0 {
		return record.Record{}, io.EOF
	}
	var pre [prefixSize]byte
	if _, err := io.ReadFull(rr.r, pre[:]); err != nil {
		return record.Record{}, ErrLengthPrefixIncomplete
	}
	plen := binary.LittleEndian.Uint32(pre[:])
	body := make([]byte, int(plen)+crcSize)
	if _, err := io.ReadFull(rr.r, body); err != nil {
		return record.Record{}, ErrRecordBodyIncomplete
	}
	payload, crc := body[:plen], binary.LittleEndian.Uint32(body[plen:])
	if crc32.ChecksumIEEE(payload) != crc {
		return record.Record{}, ErrCRCMismatch
	}
	rec, _, err := record.Decode(payload)
	if err != nil {
		return record.Record{}, err
	}
	rr.remaining--
	return rec, nil
}
