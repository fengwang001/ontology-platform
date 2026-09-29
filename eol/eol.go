// Package eol recognizes line endings (\r\n, lone \r, \n) across chunk
// boundaries. A \r at a chunk boundary stays pending until the next byte
// arrives: if that byte is \n they form one CRLF ending, otherwise the \r is
// a standalone ending and the new byte must be re-fed.
package eol

// Kind is the result of feeding one byte to the decoder.
type Kind uint8

const (
	Data    Kind = iota // byte is ordinary content
	LF                  // byte is a standalone \n
	CRLF                // pending \r plus this \n form one ending (N==2)
	CRThenData          // pending \r was a standalone ending; re-feed this byte (N==1)
)

// Step is one decoder step. N is the number of input bytes the step consumed
// counting back from the fed byte (1 normally, 2 for CRLF).
type Step struct {
	Kind Kind
	N    int
}

// Decoder is a stateful, non-concurrency-safe line-ending recognizer.
type Decoder struct {
	heldCR bool
}

// Feed processes b. Returns Data/LF for an ordinary byte, or a two-step
// sequence when a held \r is resolved: the first Step is CRThenData (caller
// must emit the held \r as an ending); the second Step describes b itself.
// When the first Step is CRThenData, len(steps)==2; otherwise len(steps)==1.
func (d *Decoder) Feed(b byte) []Step {
	if !d.heldCR {
		switch b {
		case '\n':
			return []Step{{Kind: LF, N: 1}}
		case '\r':
			d.heldCR = true
			return []Step{{Kind: Data, N: 0}}
		default:
			return []Step{{Kind: Data, N: 1}}
		}
	}
	d.heldCR = false
	if b == '\n' {
		return []Step{{Kind: CRLF, N: 2}}
	}
	if b == '\r' {
		d.heldCR = true
		return []Step{{Kind: CRThenData, N: 1}}
	}
	return []Step{{Kind: CRThenData, N: 1}, {Kind: Data, N: 1}}
}

// End flushes a held \r at stream end. It returns true (with one standalone
// ending, N==1) when a \r was pending, false otherwise.
func (d *Decoder) End() (Step, bool) {
	if d.heldCR {
		d.heldCR = false
		return Step{Kind: CRThenData, N: 1}, true
	}
	return Step{}, false
}
