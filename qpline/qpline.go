// Package qpline implements single-line Quoted-Printable encoding decisions:
// which bytes must be escaped, trailing-whitespace handling, and where soft
// line breaks are inserted. It has no dependencies on other packages.
package qpline

// Builder accumulates the encoded form of one logical line at a time.
// Content bytes are fed with Write; line boundaries are signalled with
// Newline; Finish flushes the final line.
type Builder struct {
	out      []byte // complete encoded output
	line     []byte // current physical line, excluding its CRLF
	pending  []byte // spaces/tabs since the last confirmed content token
	examined int    // total number of input bytes inspected
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder { return &Builder{} }

// Write feeds one content byte (never a line-break byte).
func (b *Builder) Write(c byte) {
	b.examined++
}

// Newline ends the current logical line, emitting CRLF.
func (b *Builder) Newline() {}

// Finish ends the encoded stream, escaping any trailing whitespace.
func (b *Builder) Finish() {}

// Bytes returns the encoded output accumulated so far.
func (b *Builder) Bytes() []byte { return b.out }

// Examined reports how many input bytes have been inspected in total.
func (b *Builder) Examined() int { return b.examined }
