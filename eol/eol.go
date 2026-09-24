// Package eol recognizes the three line endings ("\r\n", lone "\r", "\n"),
// including a pending "\r" at a stream/split boundary.
package eol

// Decoder is a one-byte-at-a-time line-ending classifier. It is not safe
// for concurrent use; the caller serializes writes.
type Decoder struct {
	pendingCR bool
}

// Result tells the caller what the byte b contributes.
type Result int

const (
	// Other: b is not part of a line ending; consume normally.
	Other Result = iota
	// PendingCR: b=='\r' is buffered pending the next byte; emit nothing.
	PendingCR
	// LoneLF: b=='\n' with no preceding pending '\r'.
	LoneLF
	// CRBeforeLF: pending '\r' and b=='\n' form one CRLF ending.
	CRBeforeLF
	// LFLongPendingCR: pending '\r' was a lone ending; b=='\n' is another.
	LFLongPendingCR
	// OtherLongPendingCR: pending '\r' was a lone ending; b is ordinary.
	OtherLongPendingCR
	// CROverCR: pending '\r' was a lone ending; b=='\r' becomes a new pending.
	CROverCR
)

// New returns a fresh decoder.
func New() *Decoder { return &Decoder{} }

// Step feeds one byte. '\r' never resolves immediately: it stays pending
// until the next byte or Flush.
func (d *Decoder) Step(b byte) Result {
	if b == '\r' {
		if d.pendingCR {
			d.pendingCR = true
			return CROverCR
		}
		d.pendingCR = true
		return Other
	}
	if b == '\n' {
		if d.pendingCR {
			d.pendingCR = false
			return CRBeforeLF
		}
		return LoneLF
	}
	if d.pendingCR {
		d.pendingCR = false
		return OtherLongPendingCR
	}
	return Other
}

// Pending reports whether a '\r' is awaiting resolution.
func (d *Decoder) Pending() bool { return d.pendingCR }

// Flush reports whether the pending '\r' becomes a lone ending at EOF and
// clears the pending state.
func (d *Decoder) Flush() bool {
	if d.pendingCR {
		d.pendingCR = false
		return true
	}
	return false
}
