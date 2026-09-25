// Package eol recognizes line endings across stream split points.
//
// \r\n and a lone \r both normalize to \n; \r\r\n is two endings
// (a lone \r followed by \r\n). A trailing \r is held pending until
// the next byte or Close disambiguates it.
package eol

const (
	CR = '\r'
	LF = '\n'
)

// Result tells the caller what one input byte produces.
type Result struct {
	EmitNL NLSrc // source of the normalized '\n' emitted before the byte
	Pass   bool  // the byte itself passes through unchanged (never CR/LF)
	Hold   bool  // the byte was a CR and is held pending
}

// NLSrc says where an emitted normalized newline comes from.
type NLSrc int

const (
	NLNone NLSrc = iota
	NLLF         // a lone '\n'
	NLCRLF       // a "\r\n" pair
	NLCR         // a flushed lone '\r'
)

// Decoder is a single-user, non-concurrent state machine.
type Decoder struct {
	pendingCR bool
}

// New returns a fresh Decoder.
func New() *Decoder { return &Decoder{} }

// Feed processes one byte. When a pending CR precedes a non-LF byte,
// EmitNL reports the flushed lone-CR newline and Pass reports the
// current byte; the current byte itself never starts a new pending CR
// in that case (a second CR re-arms via EmitNL+Hold instead).
func (d *Decoder) Feed(b byte) Result {
	if d.pendingCR {
		switch b {
		case LF:
			d.pendingCR = false
			return Result{EmitNL: NLCRLF}
		case CR:
			d.pendingCR = true
			return Result{EmitNL: NLCR}
		default:
			d.pendingCR = false
			return Result{EmitNL: NLCR, Pass: true}
		}
	}
	switch b {
	case CR:
		d.pendingCR = true
		return Result{Hold: true}
	case LF:
		return Result{EmitNL: NLLF}
	default:
		return Result{Pass: true}
	}
}

// Close reports whether a lone pending CR must be flushed as a
// newline, and resets the decoder to its initial state.
func (d *Decoder) Close() bool {
	pending := d.pendingCR
	d.pendingCR = false
	return pending
}

// Pending reports whether a CR is currently held.
func (d *Decoder) Pending() bool { return d.pendingCR }
