// Package eol identifies line endings: CRLF, lone CR, and LF, including the
// undecided state of a trailing CR at a split point.
package eol

const (
	// CR is carriage return, LF line feed.
	CR = '\r'
	// LF is line feed.
	LF = '\n'
)

// IsCR reports whether b is a carriage return.
func IsCR(b byte) bool { return b == CR }

// IsLF reports whether b is a line feed.
func IsLF(b byte) bool { return b == LF }

// IsEOL reports whether b alone can begin or constitute a line ending.
func IsEOL(b byte) bool { return b == CR || b == LF }

// IsPendingCR reports whether a CR seen at offset crOff is still undecided
// because no following byte has been observed yet.
func IsPendingCR(following []byte, crOff int) bool {
	return crOff >= 0 && len(following) == 0
}

// EndsPendingCR reports whether data ends in a CR that is not yet known to be
// part of a CRLF (a CR followed by another CR is already a settled lone EOL).
func EndsPendingCR(data []byte) bool {
	n := len(data)
	return n >= 1 && data[n-1] == CR && (n < 2 || data[n-2] != CR)
}

// CRBoundary reports whether cut is split between a CR and the LF that may
// follow it: data ends with CR at cut-1 and the byte right at cut (known via
// next) is LF.
func CRBoundary(before []byte, next byte, hasNext bool) bool {
	if len(before) == 0 || before[len(before)-1] != CR {
		return false
	}
	if len(before) >= 2 && before[len(before)-2] == CR {
		return false
	}
	return hasNext && next == LF
}
