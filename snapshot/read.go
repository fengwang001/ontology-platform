package snapshot

import (
	"fmt"
	"hash/crc32"
	"io"
)

// Read decodes a snapshot file produced by Write (or an older but
// still compatible version). It is strict: any corruption makes the
// whole read fail and no partial data is returned. Files older than
// CurrentVersion but at or above MinVersion are accepted; fields
// missing in the old version are filled with their zero defaults.
func Read(r io.Reader) ([]Record, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read input: %w", err)
	}
	h, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	region := data[headerSize:]
	var records []Record
	off := 0
	for i := 0; i < int(h.count); i++ {
		if off == len(region) {
			return nil, &CountError{Claimed: h.count, Actual: i, Excess: true}
		}
		rec, next, perr := parseRecordAt(region, off, h.version)
		if perr != nil {
			return nil, &RecordError{Index: i, Offset: int64(headerSize + off), Err: perr}
		}
		records = append(records, rec)
		off = next
	}
	if off < len(region) {
		return nil, &CountError{Claimed: h.count, Actual: actualCount(region, off, h), Excess: false}
	}
	if crc32.ChecksumIEEE(region) != h.regionCRC {
		return nil, fmt.Errorf("%w: header stored %#08x", ErrRegionChecksum, h.regionCRC)
	}
	return records, nil
}

// actualCount counts the records remaining from off on a best-effort
// basis, returning the total including h.count, or -1 if the trailing
// bytes do not parse cleanly.
func actualCount(region []byte, off int, h header) int {
	extra := 0
	for off < len(region) {
		_, next, err := parseRecordAt(region, off, h.version)
		if err != nil {
			return -1
		}
		extra++
		off = next
	}
	return int(h.count) + extra
}
