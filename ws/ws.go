// Package ws provides the delayed trailing-whitespace decision used by the
// streaming normalizer. A run of spaces/tabs is only known to be trailing
// once a line ending or stream end arrives; until then it must be buffered
// without being emitted or dropped.
package ws

// Run tracks a pending run of spaces and tabs at the current write position.
// The zero value is an empty run. A Run value is not safe for concurrent use.
type Run struct {
	buf []byte
}

// IsSpace reports whether b is trailing-trimmable whitespace (space or tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Add appends one whitespace byte to the pending run.
func (r *Run) Add(b byte) { r.buf = append(r.buf, b) }

// Len is the number of buffered (not yet classified) bytes.
func (r *Run) Len() int { return len(r.buf) }

// Take returns the buffered bytes and clears the run. The caller emits them
// as ordinary content: a non-whitespace byte arrived, so the run was not
// trailing.
func (r *Run) Take() []byte {
	b := r.buf
	r.buf = nil
	return b
}

// Drop discards the buffered bytes and clears the run: a line ending (or
// stream end) arrived, so the run was trailing whitespace.
func (r *Run) Drop() []byte {
	b := r.buf
	r.buf = nil
	return b
}
