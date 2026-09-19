package snapshot

import (
	"fmt"
	"io"
)

// Report describes the health of a possibly corrupt snapshot file.
type Report struct {
	// Records is the maximal recoverable prefix: the records from the
	// start of the file up to (but excluding) the first bad record.
	Records []Record
	// HeaderErr is set when the header itself is unreadable; in that
	// case no record-level diagnosis was attempted.
	HeaderErr error
	// BadIndex is the 0-based index of the first bad record, -1 if none.
	BadIndex int
	// BadOffset is the absolute byte offset of the first bad record.
	BadOffset int64
	// Reason is the categorized cause of the first bad record
	// (a *RecordError wrapping one of the record-level sentinels).
	Reason error
	// RecoverableAfter is the number of valid records found after the
	// bad point; those records were skipped by the prefix recovery.
	RecoverableAfter int
}

// Inspect is the lenient counterpart of Read: it never fails hard but
// reports the maximal recoverable prefix, the first bad point, and how
// many parseable records can still be found after it.
func Inspect(r io.Reader) Report {
	rep := Report{BadIndex: -1, BadOffset: -1}
	data, err := io.ReadAll(r)
	if err != nil {
		rep.HeaderErr = fmt.Errorf("snapshot: inspect input: %w", err)
		return rep
	}
	h, err := parseHeader(data)
	if err != nil {
		rep.HeaderErr = err
		return rep
	}
	region := data[headerSize:]
	off := 0
	for idx := 0; off < len(region); idx++ {
		rec, next, perr := parseRecordAt(region, off, h.version)
		if perr != nil {
			rep.BadIndex = idx
			rep.BadOffset = int64(headerSize + off)
			rep.Reason = &RecordError{Index: idx, Offset: rep.BadOffset, Err: perr}
			rep.RecoverableAfter = resync(region, off+1, h.version)
			return rep
		}
		rep.Records = append(rep.Records, rec)
		off = next
	}
	return rep
}

// resync scans byte-by-byte from offset from, counting records that
// parse and checksum cleanly. Found records are consumed whole so
// consecutive intact records are each counted once.
func resync(region []byte, from int, version uint16) int {
	found := 0
	for off := from; off+lenSize+crcSize <= len(region); {
		_, next, err := parseRecordAt(region, off, version)
		if err != nil {
			off++
			continue
		}
		found++
		off = next
	}
	return found
}
