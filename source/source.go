// Package source defines the byte source abstraction used by the assembler and
// an in-memory implementation that can inject short reads, read errors, and a
// length change in the middle of a response.
//
// It depends on no other package of this module.
package source

import (
	"errors"
	"io"
)

// Source supplies bytes of one resource. Read follows io.Reader semantics: it
// may legitimately return fewer bytes than requested (short read). Size is the
// resource length observed at the moment of the call and may change between
// calls.
type Source interface {
	Size() int64
	Read(p []byte) (int, error)
}

// Memory is a controllable in-memory source.
//
// MaxRead, if positive, caps every Read at that many bytes, so callers see
// persistent short reads. ReadErr, if non-nil, is returned after ErrAfter
// successful reads. ShrinkTo >= 0 changes the reported size and truncates data
// after ShrinkAfter successful reads, modelling a resource whose length
// changes halfway through assembly.
type Memory struct {
	Data        []byte
	MaxRead     int
	ReadErr     error
	ErrAfter    int
	ShrinkTo    int64
	ShrinkAfter int

	pos    int
	reads  int
	shrunk bool
}

// NewMemory returns a source over data with all fault injection disabled.
func NewMemory(data []byte) *Memory { return &Memory{Data: data, ShrinkTo: -1} }

// Reset rewinds the read cursor and all fault counters.
func (m *Memory) Reset() {
	m.pos = 0
	m.reads = 0
	m.shrunk = false
}

// Size reports the current (possibly shrunken) resource length.
func (m *Memory) Size() int64 {
	if m.shrunk {
		return m.ShrinkTo
	}
	return int64(len(m.Data))
}

// Read implements Source with the configured faults.
func (m *Memory) Read(p []byte) (int, error) {
	if !m.shrunk && m.ShrinkTo >= 0 && m.reads >= m.ShrinkAfter {
		m.shrunk = true
		if int(m.ShrinkTo) < len(m.Data) {
			m.Data = m.Data[:m.ShrinkTo]
		}
	}
	m.reads++
	n := len(p)
	if m.MaxRead > 0 && n > m.MaxRead {
		n = m.MaxRead
	}
	if avail := len(m.Data) - m.pos; n > avail {
		n = avail
	}
	if n > 0 {
		copy(p, m.Data[m.pos:m.pos+n])
		m.pos += n
	}
	if m.ReadErr != nil && m.reads > m.ErrAfter {
		return n, m.ReadErr
	}
	if n == 0 {
		return 0, errors.New("source: exhausted without configured EOF")
	}
	return n, nil
}

// ReadFull fills buf by repeatedly calling src.Read across short reads. An EOF
// (or zero-byte read) with bytes still missing returns io.ErrUnexpectedEOF,
// which callers identify with errors.Is(err, io.ErrUnexpectedEOF).
func ReadFull(src Source, buf []byte) error {
	off := 0
	for off < len(buf) {
		n, err := src.Read(buf[off:])
		off += n
		if err != nil {
			if err == io.EOF && off < len(buf) {
				return io.ErrUnexpectedEOF
			}
			if off < len(buf) {
				return err
			}
			return nil
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}
