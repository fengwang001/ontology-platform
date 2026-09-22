package serve

import (
	"io"

	"ontology/coalesce"
)

// WriteTo writes the assembled body to w, resuming from where any previous
// call stopped. It loops over short writes, so a sink that accepts only
// part of the remaining bytes per call is fully supported; after an error
// the caller may call WriteTo again (with the same or another writer) and
// the byte stream continues exactly where it left off.
//
// Invariant: the bytes emitted by all WriteTo calls concatenated always
// equal body[0:], because every call emits body[written:] and written only
// advances by the number of bytes the sink actually accepted.
func (a *Assembler) WriteTo(w io.Writer) (int64, error) {
	if !a.assembled {
		return 0, ErrNotAssembled
	}
	for a.written < int64(len(a.body)) {
		n, err := w.Write(a.body[a.written:])
		if n > 0 {
			a.written += int64(n)
		}
		if err != nil {
			return a.written, err
		}
		if n == 0 {
			return a.written, io.ErrShortWrite
		}
	}
	return a.written, nil
}

// Ranges returns a copy of the normalized range list: sorted, clipped,
// non-overlapping and non-adjacent. Nil before the first successful
// Assemble. Querying never mutates the assembler.
func (a *Assembler) Ranges() []coalesce.Range {
	if a.ranges == nil {
		return nil
	}
	out := make([]coalesce.Range, len(a.ranges))
	copy(out, a.ranges)
	return out
}

// Total returns the assembled response size in bytes (0 before Assemble).
func (a *Assembler) Total() int64 { return int64(len(a.body)) }

// Written returns how many bytes WriteTo has emitted so far.
func (a *Assembler) Written() int64 { return a.written }

// IsMultipart reports whether the body is multipart/byteranges framed
// (two or more ranges) or a bare single-range byte stream.
func (a *Assembler) IsMultipart() bool { return a.multipart }

// Boundary returns the multipart boundary token, or "" for bare bodies.
func (a *Assembler) Boundary() string { return a.boundary }
