// Package api is the process-memory, standard-library-only entry point for
// length-prefixed variable field encoding. It depends only on frame.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/frame"
)

// API is stateless; all state lives on the stack of each call, so all its
// methods are safe for concurrent goroutine use.
type API struct{}

// New returns the API entry point.
func New() *API { return &API{} }

// MarshalFields length-prefix encodes fields into one record.
func (a *API) MarshalFields(fields [][]byte) []byte {
	return frame.Encode(fields)
}

// UnmarshalFields decodes a record back into its fields. Any malformed
// record fails wholesale with (nil, error).
func (a *API) UnmarshalFields(buf []byte) ([][]byte, error) {
	return frame.Decode(buf)
}

var selfCheckFields = [][]byte{
	[]byte("hi"), nil, []byte("world!"), []byte("A"),
}

// naiveReference is the textbook implementation: 4-byte LE length + payload.
func naiveReference(fields [][]byte) []byte {
	out := make([]byte, 0)
	for _, f := range fields {
		var p [4]byte
		p[0] = byte(len(f))
		p[1] = byte(len(f) >> 8)
		p[2] = byte(len(f) >> 16)
		p[3] = byte(len(f) >> 24)
		out = append(out, p[:]...)
		out = append(out, f...)
	}
	return out
}

// SelfCheck verifies the four invariants on a built-in field list
// (which contains an empty field). It returns nil only if all hold.
func (a *API) SelfCheck() error {
	fields := selfCheckFields
	rec := frame.Encode(fields)

	// (1) Round trip, byte-identical per field, empty stays empty.
	got, err := frame.Decode(rec)
	if err != nil {
		return fmt.Errorf("selfcheck round trip: %w", err)
	}
	if len(got) != len(fields) {
		return fmt.Errorf("selfcheck: got %d fields, want %d", len(got), len(fields))
	}
	for i := range fields {
		if len(got[i]) != len(fields[i]) || !bytes.Equal(got[i], fields[i]) {
			return fmt.Errorf("selfcheck: field %d differs", i)
		}
	}

	// (2) Prefix self-consistency: prefix value == payload length, fields
	// cover the whole buffer with no overlap and no gap.
	r := frame.NewReader(rec)
	for r.Pos() < len(rec) {
		start := r.Pos()
		f, err := r.NextField()
		if err != nil {
			return fmt.Errorf("selfcheck self-consistency: %w", err)
		}
		if r.Pos() != start+4+len(f) {
			return errors.New("selfcheck: field overlap or gap")
		}
	}
	if r.Pos() != len(rec) {
		return errors.New("selfcheck: buffer not fully covered")
	}

	// (3) Byte-identical to the textbook reference.
	if !bytes.Equal(rec, naiveReference(fields)) {
		return errors.New("selfcheck: encoding differs from naive reference")
	}

	// (4) A truncated record is rejected wholesale as (nil, error).
	bad := append(append([]byte{}, rec...), 5, 0, 0, 0) // prefix says 5, no payload
	f, perr := frame.Decode(bad)
	if perr == nil || f != nil {
		return errors.New("selfcheck: truncated record was not rejected")
	}
	if !errors.Is(perr, frame.ErrTruncated) {
		return fmt.Errorf("selfcheck: want ErrTruncated, got %v", perr)
	}
	return nil
}
