package spill

import (
	"encoding/binary"
	"hash/crc32"
	"io"

	"ontology/record"
)

// RunWriter streams records into a run file. The header count is patched
// on Close, so the underlying writer must support WriteSeeker semantics.
type RunWriter struct {
	ws    io.WriteSeeker
	count uint64
}

// NewRunWriter writes a placeholder header and returns the writer.
func NewRunWriter(ws io.WriteSeeker) *RunWriter {
	hdr := make([]byte, 0, HeaderSize)
	hdr = append(hdr, magic...)
	hdr = binary.LittleEndian.AppendUint16(hdr, version)
	hdr = binary.LittleEndian.AppendUint64(hdr, 0)
	ws.Write(hdr) //nolint:errcheck // surfaced on Close
	return &RunWriter{ws: ws}
}

// Add appends one record with its length prefix and CRC32.
func (w *RunWriter) Add(r record.Record) error {
	payload := r.Encode(nil)
	var pre [prefixSize]byte
	binary.LittleEndian.PutUint32(pre[:], uint32(len(payload)))
	if _, err := w.ws.Write(pre[:]); err != nil {
		return err
	}
	if _, err := w.ws.Write(payload); err != nil {
		return err
	}
	var crc [crcSize]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
	if _, err := w.ws.Write(crc[:]); err != nil {
		return err
	}
	w.count++
	return nil
}

// Close patches the header count and restores the write position.
func (w *RunWriter) Close() error {
	end, err := w.ws.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := w.ws.Seek(6, io.SeekStart); err != nil {
		return err
	}
	var cnt [8]byte
	binary.LittleEndian.PutUint64(cnt[:], w.count)
	if _, err := w.ws.Write(cnt[:]); err != nil {
		return err
	}
	_, err = w.ws.Seek(end, io.SeekStart)
	return err
}
