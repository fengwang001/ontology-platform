// Package snapshot writes and reads self-describing, checksummed snapshot
// files containing primary-key ordered records.
package snapshot

import (
	"errors"
	"fmt"
)

// Sentinel error classes. Use errors.Is to classify a failure.
var (
	// ErrBadMagic means the file does not start with the snapshot magic.
	ErrBadMagic = errors.New("snapshot: bad magic number")
	// ErrHeaderTruncated means the fixed-size header is incomplete.
	ErrHeaderTruncated = errors.New("snapshot: header truncated")
	// ErrTrailingBytes means extra bytes follow the declared record area.
	ErrTrailingBytes = errors.New("snapshot: unexpected trailing bytes")
	// ErrRecordTruncated means the file ends in the middle of a record.
	ErrRecordTruncated = errors.New("snapshot: record truncated")
	// ErrLengthTooLarge means a length prefix exceeds the remaining file.
	ErrLengthTooLarge = errors.New("snapshot: record length exceeds remaining bytes")
	// ErrRecordChecksum means a per-record checksum did not verify.
	ErrRecordChecksum = errors.New("snapshot: record checksum mismatch")
	// ErrRecordParse means a record body could not be decoded.
	ErrRecordParse = errors.New("snapshot: record content unparseable")
	// ErrRegionChecksum means the whole record-area checksum did not verify.
	ErrRegionChecksum = errors.New("snapshot: record area checksum mismatch")
)

// VersionError reports an unsupported format version.
type VersionError struct {
	Got       uint16
	Current   uint16
	MinCompat uint16
}

func (e *VersionError) Error() string {
	switch {
	case e.Got > e.Current:
		return fmt.Sprintf("snapshot: version %d is newer than supported %d", e.Got, e.Current)
	default:
		return fmt.Sprintf("snapshot: version %d is below minimum compatible %d", e.Got, e.MinCompat)
	}
}

// Is classifies both "too new" and "too old" as version errors while the
// concrete direction remains available through the VersionError fields.
func (e *VersionError) Is(target error) bool {
	switch target {
	case ErrVersionTooNew:
		return e.Got > e.Current
	case ErrVersionTooOld:
		return e.Got < e.MinCompat
	}
	return false
}

var (
	// ErrVersionTooNew: file version is higher than this library supports.
	ErrVersionTooNew = errors.New("snapshot: file version too new")
	// ErrVersionTooOld: file version is below the minimum compatible version.
	ErrVersionTooOld = errors.New("snapshot: file version too old")
)

// RecordError locates a failure at a specific record.
type RecordError struct {
	Index  int   // zero-based record index
	Offset int64 // byte offset of the record start within the file
	Err    error // wrapped class
}

func (e *RecordError) Error() string {
	return fmt.Sprintf("snapshot: record %d at offset %d: %v", e.Index, e.Offset, e.Err)
}

func (e *RecordError) Unwrap() error { return e.Err }

// CountMismatchError means the header count disagrees with decoded records.
type CountMismatchError struct {
	Claimed int
	Actual  int
}

func (e *CountMismatchError) Error() string {
	switch {
	case e.Claimed > e.Actual:
		return fmt.Sprintf("snapshot: header claims %d records but found %d (claimed more)", e.Claimed, e.Actual)
	default:
		return fmt.Sprintf("snapshot: header claims %d records but found %d (claimed fewer)", e.Claimed, e.Actual)
	}
}

// ClaimedMore reports whether the header count was too large.
func (e *CountMismatchError) ClaimedMore() bool { return e.Claimed > e.Actual }

// WriteError wraps an underlying writer error and reports bytes already written.
type WriteError struct {
	BytesWritten int64
	Err          error
}

func (e *WriteError) Error() string {
	return fmt.Sprintf("snapshot: write failed after %d bytes: %v", e.BytesWritten, e.Err)
}

func (e *WriteError) Unwrap() error { return e.Err }
