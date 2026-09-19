package snapshot

import (
	"errors"
	"fmt"
)

// Error categories. Use errors.Is to distinguish them.
var (
	// ErrBadMagic means the file does not start with the snapshot magic.
	ErrBadMagic = errors.New("bad magic number")
	// ErrVersionTooNew means the file version is above CurrentVersion.
	ErrVersionTooNew = errors.New("format version is newer than supported")
	// ErrVersionTooOld means the file version is below MinVersion.
	ErrVersionTooOld = errors.New("format version is below minimum compatible")
	// ErrLengthOverflow means a record length prefix exceeds maxRecordSize.
	ErrLengthOverflow = errors.New("record length prefix exceeds maximum record size")
	// ErrTruncated means the file ends in the middle of a record.
	ErrTruncated = errors.New("file ends in the middle of a record")
	// ErrRecordChecksum means a record's own checksum did not match.
	ErrRecordChecksum = errors.New("record checksum mismatch")
	// ErrRecordParse means a record body could not be decoded.
	ErrRecordParse = errors.New("record content cannot be parsed")
	// ErrRegionChecksum means the whole-record-region checksum did not match.
	ErrRegionChecksum = errors.New("record region checksum mismatch")
	// ErrCountMismatch means the header count disagrees with the actual records.
	ErrCountMismatch = errors.New("header record count does not match actual records")
)

// RecordError pinpoints a single bad record by index and byte offset.
type RecordError struct {
	Index  int   // 0-based record index
	Offset int64 // absolute byte offset of the record in the file
	Err    error // one of the record-level categories above
}

func (e *RecordError) Error() string {
	return fmt.Sprintf("snapshot: record %d at byte offset %d: %v", e.Index, e.Offset, e.Err)
}

func (e *RecordError) Unwrap() error { return e.Err }

// CountError reports a header record count that disagrees with the file.
type CountError struct {
	Claimed uint32 // count stored in the header
	Actual  int    // actual records found, -1 when not fully determinable
	Excess  bool   // true when the header claims more records than present
}

func (e *CountError) Error() string {
	direction := "fewer"
	if e.Excess {
		direction = "more"
	}
	actual := "unknown"
	if e.Actual >= 0 {
		actual = fmt.Sprintf("%d", e.Actual)
	}
	return fmt.Sprintf("snapshot: header claims %d records, %s than actually present (%s)",
		e.Claimed, direction, actual)
}

func (e *CountError) Unwrap() error { return ErrCountMismatch }

// WriteError reports an io.Writer failure together with progress made.
type WriteError struct {
	Written int64 // bytes successfully written before the failure
	Err     error // the underlying writer error, never swallowed
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("snapshot: write failed after %d bytes: %v", e.Written, e.Err)
}

func (e *WriteError) Unwrap() error { return e.Err }
