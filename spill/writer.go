package spill

import (
	"os"
	"path/filepath"

	"ontology/record"
)

// Writer serializes one sorted batch into a run file.
//
// Records are written to path+".tmp" first; Close renames the file
// atomically, so a crash during a spill can only ever leave either the
// previous complete run (absent for a new one) or a detachable temp
// file — never a half-written run under the final name.
type Writer struct {
	path string
	h    Header
	f    *os.File
	buf  []byte
}

// Create starts a new run file. records must already be sorted in
// record.Less order; count is captured up front so the header is exact.
func Create(dir string, runID uint64, records []record.Record) (*Writer, error) {
	path := RunPath(dir, runID)
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return nil, err
	}
	w := &Writer{
		path: path,
		f:    f,
		h:    Header{RunID: runID, Count: uint64(len(records))},
	}
	w.buf = encodeHeader(w.buf[:0], w.h)
	for _, r := range records {
		w.buf = appendRecord(w.buf, r)
	}
	return w, nil
}

func appendRecord(dst []byte, r record.Record) []byte {
	frame := record.AppendFrame(nil, r)
	// Record frame on disk: uint32 bodyLen | frame bytes | uint32 crc.
	dst = appendU32(dst, uint32(len(frame))+crcLen)
	dst = append(dst, frame...)
	dst = crc32Append(dst, frame)
	return dst
}

// BytesWritten reports how many bytes have been buffered for the run
// (header + all record frames). Used by tests to enumerate truncation
// points before Close.
func (w *Writer) BytesWritten() int { return len(w.buf) }

// Close flushes buffered bytes, truncating at cut when cut >= 0 (test
// fault injection: cut is an absolute byte offset in the final file),
// then atomically renames into place. cut == 0 writes an empty file.
func (w *Writer) Close(cut int) error {
	data := w.buf
	if cut >= 0 && cut < len(data) {
		data = data[:cut]
	}
	if _, err := w.f.Write(data); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	if err := w.f.Close(); err != nil {
		return err
	}
	return os.Rename(w.path+".tmp", w.path)
}

// RunPath returns the canonical run file name for a run id.
func RunPath(dir string, runID uint64) string {
	return filepath.Join(dir, runFileName(runID))
}
