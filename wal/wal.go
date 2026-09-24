// Package wal persists whole batches with a self describing header, length
// prefixed items and one batch-level CRC32. A batch is all-or-nothing.
package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
)

// Sentinel failures, all distinguishable with errors.Is.
var (
	ErrHeaderPartial      = errors.New("wal: file header incomplete")
	ErrBatchHeaderPartial = errors.New("wal: batch header incomplete")
	ErrItemPartial        = errors.New("wal: batch item incomplete")
	ErrBatchCRC           = errors.New("wal: batch crc mismatch")
	ErrEmpty              = errors.New("wal: empty file")
)

var fileMagic = []byte("WAL1")

const (
	fileHeaderLen = 8 // magic(4) + version(4)
	fileVersion   = 1
)

func fileHeader() []byte {
	h := make([]byte, fileHeaderLen)
	copy(h, fileMagic)
	binary.BigEndian.PutUint32(h[4:], fileVersion)
	return h
}

// Batch is one atomic group of entries with a contiguous seq range.
type Batch struct {
	SeqStart uint64
	Payloads [][]byte
}

// SeqEnd is the last seq occupied by the batch (0 for an empty batch).
func (b Batch) SeqEnd() uint64 { return b.SeqStart + uint64(len(b.Payloads)) - 1 }

// Options inject faults for tests.
type Options struct {
	SyncFail   func(syncNo int) error // returned from Sync on chosen calls
	CrashAfter int                    // >0: stop the first Write after n bytes (0 = off)
}

// Writer appends batches to one log file.
type Writer struct {
	f       *os.File
	opt     Options
	syncNo  int
	written int // bytes emitted by the first (crash) Write
}

// Create makes a fresh file holding only the 8 byte file header.
func Create(path string, opt Options) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(fileHeader()); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, opt: opt}, nil
}

// OpenAt reopens an existing good log and appends after offset bytes.
func OpenAt(path string, offset int64, opt Options) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, opt: opt}, nil
}

func record(b Batch) []byte {
	var body bytes.Buffer
	binary.Write(&body, binary.BigEndian, b.SeqStart)
	binary.Write(&body, binary.BigEndian, uint32(len(b.Payloads)))
	for _, p := range b.Payloads {
		binary.Write(&body, binary.BigEndian, uint32(len(p)))
		body.Write(p)
	}
	rec := make([]byte, 8+body.Len()+4)
	copy(rec[:4], fileMagic)
	binary.BigEndian.PutUint32(rec[4:8], uint32(body.Len()))
	copy(rec[8:], body.Bytes())
	binary.BigEndian.PutUint32(rec[len(rec)-4:], crc32.ChecksumIEEE(rec[:len(rec)-4]))
	return rec
}

// Write persists and fsyncs one batch. MaxBatchBytes reports encoded size.
func (w *Writer) Write(b Batch) (maxBatchBytes int, err error) {
	rec := record(b)
	data := rec
	if w.opt.CrashAfter > 0 {
		if n := w.opt.CrashAfter - w.written; n < len(data) {
			if n < 0 {
				n = 0
			}
			data = rec[:n]
		}
		w.written += len(data)
	}
	if _, err = w.f.Write(data); err != nil {
		return len(rec), err
	}
	if w.opt.CrashAfter > 0 && w.written <= w.opt.CrashAfter {
		return len(rec), errors.New("wal: simulated crash mid batch")
	}
	if err = w.f.Sync(); err != nil {
		return len(rec), err
	}
	w.syncNo++
	if w.opt.SyncFail != nil {
		if e := w.opt.SyncFail(w.syncNo); e != nil {
			return len(rec), e
		}
	}
	return len(rec), nil
}

// Close flushes and closes the file.
func (w *Writer) Close() error { return w.f.Close() }

func u32(p []byte) uint32 { return binary.BigEndian.Uint32(p) }
func u64(p []byte) uint64 { return binary.BigEndian.Uint64(p) }

// Classify reports why a log cannot be fully read, or nil if intact.
func Classify(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return ErrEmpty
	}
	if len(data) < fileHeaderLen {
		return ErrHeaderPartial
	}
	off := fileHeaderLen
	for off < len(data) {
		// Record prefix: magic(4) + bodyLen(4).
		if len(data)-off < 8 || !bytes.Equal(data[off:off+4], fileMagic) {
			return ErrBatchHeaderPartial
		}
		bodyLen := int(u32(data[off+4 : off+8]))
		recEnd := off + 8 + bodyLen + 4
		// Fixed batch header inside the body: seqStart(8) + count(4).
		if len(data)-off < 20 {
			return ErrBatchHeaderPartial
		}
		count := u32(data[off+16 : off+20])
		p := off + 20
		for i := uint32(0); i < count; i++ {
			if len(data) < p+4 {
				return ErrItemPartial
			}
			end := p + 4 + int(u32(data[p:p+4]))
			if len(data) < end {
				return ErrItemPartial
			}
			p = end
		}
		// Items complete; missing CRC tail bytes are an incomplete last record.
		if len(data) < p+4 {
			return ErrItemPartial
		}
		if len(data) < recEnd {
			return ErrItemPartial
		}
		if crc32.ChecksumIEEE(data[off:recEnd-4]) != u32(data[recEnd-4:recEnd]) {
			return ErrBatchCRC
		}
		off = recEnd
	}
	return nil
}

// ReadBatches returns all complete batches up to the first bad tail batch.
func ReadBatches(path string) ([]Batch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	if len(data) < fileHeaderLen {
		return nil, ErrHeaderPartial
	}
	var out []Batch
	off := fileHeaderLen
	for off < len(data) {
		if len(data)-off < 8 {
			return out, ErrBatchHeaderPartial
		}
		if len(data)-off < 20 {
			return out, ErrBatchHeaderPartial
		}
		bodyLen := int(u32(data[off+4 : off+8]))
		recEnd := off + 8 + bodyLen + 4
		if len(data) < recEnd {
			return out, ErrItemPartial
		}
		got := u32(data[recEnd-4 : recEnd])
		if crc32.ChecksumIEEE(data[off:recEnd-4]) != got {
			return out, ErrBatchCRC
		}
		b := Batch{SeqStart: u64(data[off+8 : off+16])}
		count := u32(data[off+16 : off+20])
		p := off + 20
		for i := uint32(0); i < count; i++ {
			n := int(u32(data[p : p+4]))
			p += 4
			b.Payloads = append(b.Payloads, append([]byte(nil), data[p:p+n]...))
			p += n
		}
		out = append(out, b)
		off = recEnd
	}
	return out, nil
}
