// Package api is the public facade over wire. It is safe for concurrent use.
package api

import (
	"bytes"
	"errors"

	"ontology/enc"
	"ontology/wire"
)

// API exposes message marshal/unmarshal. The zero-dependency methods are
// stateless, so one value may be shared by many goroutines.
type API struct{}

// New returns a ready-to-use API.
func New() *API { return &API{} }

// Marshal encodes fields in ascending field-number order.
func (a *API) Marshal(fs []wire.Field) ([]byte, error) { return wire.Encode(fs) }

// Unmarshal decodes a message, skipping fields absent from schema.
func (a *API) Unmarshal(b []byte, schema map[int]int) (map[int]wire.Field, error) {
	return wire.Decode(b, schema)
}

// GetField fetches one decoded field by number in O(1).
func (a *API) GetField(m map[int]wire.Field, num int) (wire.Field, bool) {
	f, ok := wire.Lookup(m, num)
	return f, ok
}

// SelfCheck verifies the four invariants on built-in messages:
// round-trip, order independence, byte-level match with a hand-written
// naive reference, and all-or-nothing failure.
func (a *API) SelfCheck() error {
	msg := []wire.Field{
		{Num: 1, Wire: wire.WireVarint, U: 150},
		{Num: 2, Wire: wire.WireLengthDelim, B: []byte("A")},
		{Num: 3, Wire: wire.WireFixed32, U: 0x01020304},
		{Num: 4, Wire: wire.WireVarint, U: uint64(enc.Zigzag32(-1))},
		{Num: 5, Wire: wire.WireFixed64, U: 0x0807060504030201},
	}
	schema := map[int]int{1: 0, 2: 2, 3: 5, 4: 0, 5: 1}

	// Invariant 3: byte-level equality with a naive textbook reference.
	var naive []byte
	put := func(num, wt int, val ...byte) {
		naive = append(naive, enc.EncodeVarint(uint64(num)<<3|uint64(wt))...)
		naive = append(naive, val...)
	}
	put(1, 0, 0x96, 0x01)
	put(2, 2, 0x01, 0x41)
	put(3, 5, 0x04, 0x03, 0x02, 0x01)
	put(4, 0, 0x01)
	put(5, 1, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08)
	got, err := a.Marshal(msg)
	if err != nil || !bytes.Equal(got, naive) {
		return errors.New("api: encode differs from naive reference")
	}

	// Invariant 1: round-trip.
	dec, err := a.Unmarshal(got, schema)
	if err != nil || len(dec) != len(msg) {
		return errors.New("api: round-trip decode failed")
	}
	for _, f := range msg {
		g, ok := a.GetField(dec, f.Num)
		if !ok || g.U != f.U || !bytes.Equal(g.B, f.B) || g.Wire != f.Wire {
			return errors.New("api: round-trip field mismatch")
		}
	}

	// Invariant 2: shuffled byte order decodes identically.
	shuffled, err := a.Marshal([]wire.Field{msg[3], msg[0], msg[4], msg[1], msg[2]})
	if err != nil {
		return err
	}
	dec2, err := a.Unmarshal(shuffled, schema)
	if err != nil || len(dec2) != len(dec) {
		return errors.New("api: order-independent decode failed")
	}
	for num, f := range dec {
		g := dec2[num]
		if g.U != f.U || !bytes.Equal(g.B, f.B) || g.Wire != f.Wire {
			return errors.New("api: order-dependent result")
		}
	}

	// Invariant 4: every rejection is total — (nil, error), no partials.
	bad := [][]byte{
		{0x12, 0x05, 0x41}, // truncated length-delimited
		{0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // overflow
		{0x0B, 0x00},             // wire type 3
		{0x00, 0x00},             // field number 0
		{0x08, 0x01, 0x08, 0x02}, // duplicate field 1
	}
	for _, b := range bad {
		if m, err := a.Unmarshal(b, schema); err == nil || m != nil {
			return errors.New("api: rejection left partial state")
		}
	}
	// Still usable afterwards.
	if _, err := a.Unmarshal(got, schema); err != nil {
		return errors.New("api: unusable after rejection")
	}
	return nil
}
