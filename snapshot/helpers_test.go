package snapshot

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func sampleRecords() []Record {
	return []Record{
		{Key: "alpha", Num: 1, Value: "one"},
		{Key: "bravo", Num: 2, Value: "two"},
		{Key: "charlie", Num: 3, Value: "three"},
		{Key: "delta", Num: 4, Value: "four"},
	}
}

func writeBytes(t *testing.T, records []Record) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, records); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

// recordOffsets returns the absolute file offset of each record.
func recordOffsets(t *testing.T, data []byte) []int {
	t.Helper()
	if len(data) < headerSize {
		t.Fatalf("data too short: %d bytes", len(data))
	}
	count := int(binary.BigEndian.Uint32(data[len(Magic)+2:]))
	offsets := make([]int, 0, count)
	off := headerSize
	for i := 0; i < count; i++ {
		offsets = append(offsets, off)
		bodyLen := int(binary.BigEndian.Uint32(data[off:]))
		off += lenSize + bodyLen + crcSize
	}
	return offsets
}

// buildV1File hand-builds a legacy version-1 snapshot file.
func buildV1File(records []Record) []byte {
	var region []byte
	for _, r := range records {
		region = append(region, frameRecord(encodeBody(VersionV1, r))...)
	}
	h := header{
		version:   VersionV1,
		count:     uint32(len(records)),
		regionCRC: crc32.ChecksumIEEE(region),
	}
	return append(h.marshal(), region...)
}

func assertRecordErr(t *testing.T, err error, sentinel error, index, offset int) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v does not match expected category", err)
	}
	var re *RecordError
	if !errors.As(err, &re) {
		t.Fatalf("error %v is not a *RecordError", err)
	}
	if re.Index != index || re.Offset != int64(offset) {
		t.Fatalf("got record %d at offset %d, want record %d at offset %d",
			re.Index, re.Offset, index, offset)
	}
}

var errWriteBoom = errors.New("simulated write failure")

// failWriter accepts at most limit bytes, then fails every write.
type failWriter struct {
	limit int
	n     int
}

func (f *failWriter) Write(p []byte) (int, error) {
	remaining := f.limit - f.n
	if remaining <= 0 {
		return 0, errWriteBoom
	}
	if len(p) > remaining {
		f.n += remaining
		return remaining, errWriteBoom
	}
	f.n += len(p)
	return len(p), nil
}
