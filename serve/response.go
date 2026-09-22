package serve

import (
	"io"

	"ontology/coalesce"
)

// Response is one assembled range response.
//
// The body is immutable after Build; the only mutable state is the write
// cursor. That is the whole resume invariant: bytes already written are
// body[:written], so resuming at any point continues with body[written:]
// and any split of the writes yields the exact same byte stream.
//
// The zero value is a valid "not assembled yet" response: every query
// returns its zero value and WriteSome reports io.EOF.
type Response struct {
	body    []byte
	ranges  []coalesce.Range
	total   int64 // resource length
	multi   bool
	written int64
}

// Ranges returns a copy of the normalized range list (nil before assembly).
func (r *Response) Ranges() []coalesce.Range {
	if r == nil || r.ranges == nil {
		return nil
	}
	out := make([]coalesce.Range, len(r.ranges))
	copy(out, r.ranges)
	return out
}

// TotalBytes returns the assembled response body size in bytes.
func (r *Response) TotalBytes() int64 {
	if r == nil {
		return 0
	}
	return int64(len(r.body))
}

// Written returns how many body bytes have been handed to writers so far.
func (r *Response) Written() int64 {
	if r == nil {
		return 0
	}
	return r.written
}

// Multipart reports whether the body is multipart/byteranges framed.
func (r *Response) Multipart() bool {
	return r != nil && r.multi
}

// ResourceSize returns the total length of the underlying resource.
func (r *Response) ResourceSize() int64 {
	if r == nil {
		return 0
	}
	return r.total
}

// Done reports whether the whole body has been written out.
func (r *Response) Done() bool {
	return r == nil || r.written >= int64(len(r.body))
}

// WriteSome offers the unwritten remainder to w exactly once and advances
// the cursor by however many bytes w accepted, so short writes simply
// resume on the next call. It returns io.EOF when nothing remains.
func (r *Response) WriteSome(w io.Writer) (int, error) {
	if r == nil || r.written >= int64(len(r.body)) {
		return 0, io.EOF
	}
	n, err := w.Write(r.body[r.written:])
	if n < 0 {
		n = 0
	}
	r.written += int64(n)
	return n, err
}
