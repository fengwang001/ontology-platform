package snapshot

import (
	"hash/crc32"
	"io"
	"sort"
)

// countWriter tracks total bytes successfully written.
type countWriter struct {
	w     io.Writer
	count int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.count += int64(n)
	return n, err
}

// Write serializes records deterministically to w.
//
// Records are sorted by primary key, so shuffling the input slice does not
// change the produced bytes. An empty set still produces a complete file with
// a header. Writer errors are wrapped in *WriteError carrying bytes written.
func Write(w io.Writer, records []Record) error {
	sorted := make([]Record, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		if sorted[i].Value != sorted[j].Value {
			return sorted[i].Value < sorted[j].Value
		}
		return sorted[i].Note < sorted[j].Note
	})

	cw := &countWriter{w: w}
	fail := func(err error) error {
		return &WriteError{BytesWritten: cw.count, Err: err}
	}

	frames := make([][]byte, len(sorted))
	regionCRC := crc32.NewIEEE()
	for i, r := range sorted {
		body := encodeBody(r, CurrentVersion)
		frame := make([]byte, recFrameOver+len(body))
		putUint32(frame, uint32(len(body)))
		copy(frame[lenPrefixLen:lenPrefixLen+len(body)], body)
		putUint32(frame[lenPrefixLen+len(body):], crc32.ChecksumIEEE(body))
		frames[i] = frame
		regionCRC.Write(frame)
	}

	header := make([]byte, headerSize)
	copy(header[0:magicLen], Magic)
	putUint16(header[magicLen:magicLen+versionLen], CurrentVersion)
	putUint32(header[magicLen+versionLen:magicLen+versionLen+countLen], uint32(len(sorted)))

	putUint32(header[magicLen+versionLen+countLen:], regionCRC.Sum32())

	if _, err := cw.Write(header); err != nil {
		return fail(err)
	}
	for _, frame := range frames {
		if _, err := cw.Write(frame); err != nil {
			return fail(err)
		}
	}
	return nil
}
