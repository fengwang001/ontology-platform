// Package ws tracks the delayed decision for trailing horizontal whitespace:
// a run of spaces/tabs is only known to be trailing once a line ending or the
// end of the stream is reached. The tracker never decides by itself; the
// caller marks each run when its terminator becomes known.
package ws

// Is reports whether b is trailing-eligible whitespace (space or tab).
// Vertical whitespace (\n, \r, \v, \f) is intentionally false.
func Is(b byte) bool { return b == ' ' || b == '\t' }

// Tracker buffers one currently-open run of spaces/tabs. A run starts at the
// first whitespace byte after content or a newline, and is either committed
// (line ending seen -> the run was trailing) or cancelled (content seen ->
// the run was internal whitespace and must be emitted verbatim).
type Tracker struct {
	buf   []byte // buffered bytes of the open run
	start int    // original offset of the run's first byte
	open  bool
}

// Begin opens a run at original offset start with its first byte.
func (t *Tracker) Begin(start int, b byte) {
	t.open, t.start = true, start
	t.buf = append(t.buf[:0], b)
}

// Add appends another whitespace byte to the open run.
func (t *Tracker) Add(b byte) { t.buf = append(t.buf, b) }

// Open reports whether a run is in progress.
func (t *Tracker) Open() bool { return t.open }

// Start returns the original offset of the open run.
func (t *Tracker) Start() int { return t.start }

// Bytes returns the buffered whitespace of the open run.
func (t *Tracker) Bytes() []byte { return t.buf }

// Len returns the buffered byte count.
func (t *Tracker) Len() int { return len(t.buf) }

// Close ends the run, returning its bytes and start offset.
func (t *Tracker) Close() ([]byte, int) {
	buf, start := t.buf, t.start
	t.open, t.buf = false, t.buf[:0]
	return buf, start
}
