// Package eol recognizes line endings (\r\n, lone \r, \n) across stream chunks.
// It depends on no other package in this module.
package eol

// Decider is a stateful line-ending classifier. A lone \r stays pending until
// the next byte decides whether it begins a \r\n pair.
type Decider struct {
	cr    bool
	crOff int
}

// New returns a fresh Decider.
func New() *Decider { return &Decider{} }

// Feed consumes one byte at offset. Return values:
//   - nlAt >= 0: a normalized '\n' ends a line ending at original offset nlAt
//     ('\n' offset for \n and \r\n; the lone \r's own offset otherwise).
//   - text: b is ordinary content and must be emitted (may accompany an nlAt,
//     which happens when a pending \r is terminated by a non-line byte).
//
// A \r\n pair emits exactly one nl; \r\r\n yields two line endings.
func (d *Decider) Feed(b byte, off int) (nlAt int, text bool) {
	nlAt = -1
	if d.cr {
		d.cr = false
		pending := d.crOff
		if b == '\n' {
			return off, false
		}
		if b == '\r' {
			d.cr, d.crOff = true, off
			return pending, false
		}
		return pending, true
	}
	if b == '\r' {
		d.cr, d.crOff = true, off
		return -1, false
	}
	if b == '\n' {
		return off, false
	}
	return -1, true
}

// Flush resolves a pending \r at stream end as a lone \r line ending.
// It returns the offset of the pending \r when one existed.
func (d *Decider) Flush() (nlAt int, ok bool) {
	if d.cr {
		d.cr = false
		return d.crOff, true
	}
	return 0, false
}

// Pending reports whether a \r is awaiting the next byte.
func (d *Decider) Pending() bool { return d.cr }

// PendingAt returns the offset of the pending \r and true when one exists.
func (d *Decider) PendingAt() (int, bool) { return d.crOff, d.cr }
