package snapshot

import (
	"hash/crc32"
	"io"
	"sort"
)

// Write serializes records to w in the current format version.
//
// The output is deterministic: records are sorted by primary key
// before encoding, so the same set of records always produces
// identical bytes regardless of input order. An empty record set
// still produces a valid, non-empty file. If w fails, the returned
// *WriteError carries the number of bytes written so far and wraps
// the underlying error.
func Write(w io.Writer, records []Record) error {
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.Num != b.Num {
			return a.Num < b.Num
		}
		return a.Value < b.Value
	})
	var region []byte
	for _, r := range sorted {
		region = append(region, frameRecord(encodeBody(CurrentVersion, r))...)
	}
	h := header{
		version:   CurrentVersion,
		count:     uint32(len(sorted)),
		regionCRC: crc32.ChecksumIEEE(region),
	}
	cw := &countingWriter{w: w}
	if err := writeFull(cw, h.marshal()); err != nil {
		return &WriteError{Written: cw.n, Err: err}
	}
	if err := writeFull(cw, region); err != nil {
		return &WriteError{Written: cw.n, Err: err}
	}
	return nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
