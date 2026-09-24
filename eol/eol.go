// Package eol identifies line endings across stream boundaries.
// A trailing CR stays pending until the next byte arrives.
package eol

// Kind classifies a resolved byte.
type Kind uint8

const (
	// Other is a content byte emitted verbatim.
	Other Kind = iota
	// LF is a normalized line ending emitted as '\n'.
	LF
)

// Decision is the result of feeding one byte to a Cursor.
type Decision struct {
	// Out is the resolved byte, or 0 when the byte remains pending.
	Out byte
	// Kind is Other or LF; it is Other when Pending is true.
	Kind Kind
	// Pending reports that the input byte (a CR) waits for the next byte.
	Pending bool
	// DelStart/DelEnd describe original bytes consumed but not present in Out.
	// A lone CR keeps length one (Out='\n', del empty); CRLF deletes the CR.
	DelStart, DelEnd int
	// Consumed is how many original bytes this decision accounts for.
	Consumed int
}

// PendingCR reports whether c waits on a CR.
func (c Cursor) PendingCR() bool { return c.cr }

// ResyncStart rewinds a cut point so a fresh cursor can replay from there.
// Only a trailing CR (optionally preceded through whitespace by the caller) is
// ambiguous, so at most one byte is reclaimed here.
func ResyncStart(data []byte) int {
	if len(data) > 0 && data[len(data)-1] == '\r' {
		return len(data) - 1
	}
	return len(data)
}

// Cursor is a streaming line-ending recognizer.
type Cursor struct {
	cr bool
}

// Step feeds one byte at original offset off and returns a decision.
// A returned Pending decision must be followed by another Step or End.
func (c *Cursor) Step(b byte, off int) Decision {
	if c.cr {
		c.cr = false
		if b == '\n' {
			return Decision{Out: '\n', Kind: LF, DelStart: off - 1, DelEnd: off, Consumed: 1}
		}
		if b == '\r' {
			c.cr = true
			return Decision{Out: '\n', Kind: LF, Consumed: 1}
		}
		return Decision{Out: b, Kind: Other, Consumed: 1}
	}
	if b == '\r' {
		c.cr = true
		return Decision{Pending: true, Consumed: 1}
	}
	if b == '\n' {
		return Decision{Out: '\n', Kind: LF, Consumed: 1}
	}
	return Decision{Out: b, Kind: Other, Consumed: 1}
}

// End resolves a still-pending CR as a lone CR line ending.
func (c *Cursor) End(lastOff int) (Decision, bool) {
	if !c.cr {
		return Decision{}, false
	}
	c.cr = false
	return Decision{Out: '\n', Kind: LF, Consumed: 1}, true
}
