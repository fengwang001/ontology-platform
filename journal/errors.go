package journal

import "errors"

// Tail errors. They describe the first damaged position encountered during
// replay; every earlier frame is intact and has been delivered.
var (
	// ErrBadMagic means the file does not begin with the journal magic.
	ErrBadMagic = errors.New("journal: bad magic")
	// ErrUnsupportedVersion means the header version is not understood.
	ErrUnsupportedVersion = errors.New("journal: unsupported format version")
	// ErrHeaderIncomplete means fewer than HeaderLen bytes are present.
	ErrHeaderIncomplete = errors.New("journal: header incomplete")
	// ErrLengthPrefixIncomplete means fewer than 4 bytes remain for a
	// frame length prefix.
	ErrLengthPrefixIncomplete = errors.New("journal: length prefix incomplete")
	// ErrBodyIncomplete means fewer than length bytes remain for the
	// record body (checksum bytes therefore absent as well).
	ErrBodyIncomplete = errors.New("journal: record body incomplete")
	// ErrCRCMismatch means the full body is present but the trailing
	// checksum is either partially present or does not match.
	ErrCRCMismatch = errors.New("journal: crc mismatch")
)

// Classify returns the tail-damage category of err, or "" if err is nil.
func Classify(err error) string {
	switch {
	case errors.Is(err, ErrHeaderIncomplete):
		return "header-incomplete"
	case errors.Is(err, ErrLengthPrefixIncomplete):
		return "length-prefix-incomplete"
	case errors.Is(err, ErrBodyIncomplete):
		return "body-incomplete"
	case errors.Is(err, ErrCRCMismatch):
		return "crc-mismatch"
	default:
		return ""
	}
}
