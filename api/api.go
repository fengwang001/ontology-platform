// Package api is the public façade over the wire codec: New builds a codec,
// Marshal/Unmarshal move messages between fields and wire bytes, GetField
// retrieves a field by number, and SelfCheck verifies the four invariants
// on built-in messages. A Codec carries no mutable state, so all methods are
// safe for concurrent use.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/wire"
)

// Codec encodes and decodes protobuf wire messages. The zero value is not
// used; create one with New.
type Codec struct{}

// New returns a ready-to-use codec.
func New() *Codec { return &Codec{} }

// Marshal serialises fields in ascending field-number order.
func (c *Codec) Marshal(fields []wire.Field) ([]byte, error) {
	return wire.Encode(fields)
}

// Unmarshal parses b against schema (field number -> wire type). Unknown
// fields are skipped; any failure returns nil and leaves no partial result.
func (c *Codec) Unmarshal(b []byte, schema map[int]int) (map[int]wire.Field, error) {
	return wire.Decode(b, schema)
}

// GetField returns the field by number via hash lookup on the decoded map;
// it does not scan fields.
func (c *Codec) GetField(m map[int]wire.Field, num int) (wire.Field, bool) {
	f, ok := m[num]
	return f, ok
}

// goldenFields and goldenBytes are the section-3 five-field message.
func goldenFields() []wire.Field {
	return []wire.Field{
		{Num: 1, Wire: wire.WireVarint, U: 150},
		{Num: 2, Wire: wire.WireBytes, B: []byte("A")},
		{Num: 3, Wire: wire.WireFixed32, U: 0x01020304},
		{Num: 4, Wire: wire.WireVarint, U: 1}, // zigzag32(-1) == 1 on the wire
		{Num: 5, Wire: wire.WireFixed64, U: 0x0807060504030201},
	}
}

var goldenBytes = []byte{
	0x08, 0x96, 0x01, 0x12, 0x01, 0x41, 0x1d, 0x04, 0x03, 0x02, 0x01,
	0x20, 0x01, 0x29, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
}

func schemaOf(fs []wire.Field) map[int]int {
	s := make(map[int]int, len(fs))
	for _, f := range fs {
		s[f.Num] = f.Wire
	}
	return s
}

// SelfCheck verifies the four invariants on built-in messages:
// textbook-byte equality, roundtrip, order independence, and all-five
// distinct rejection classes returning nothing and leaving the codec usable.
func (c *Codec) SelfCheck() error {
	fs := goldenFields()
	schema := schemaOf(fs)

	encoded, err := c.Marshal(fs)
	if err != nil {
		return fmt.Errorf("selfcheck: marshal: %w", err)
	}
	if !bytes.Equal(encoded, goldenBytes) {
		return fmt.Errorf("selfcheck: bytes %x != textbook %x", encoded, goldenBytes)
	}

	dec, err := c.Unmarshal(encoded, schema)
	if err != nil {
		return fmt.Errorf("selfcheck: roundtrip decode: %w", err)
	}
	if len(dec) != len(fs) {
		return fmt.Errorf("selfcheck: roundtrip lost fields")
	}
	for _, f := range fs {
		got, ok := c.GetField(dec, f.Num)
		if !ok || got.Wire != f.Wire || got.U != f.U || !bytes.Equal(got.B, f.B) {
			return fmt.Errorf("selfcheck: field %d mismatch", f.Num)
		}
	}

	// Fields in reverse wire order must decode to the same result.
	var shuffled []byte
	for i := len(fs) - 1; i >= 0; i-- {
		one, err := c.Marshal([]wire.Field{fs[i]})
		if err != nil {
			return fmt.Errorf("selfcheck: shuffled marshal: %w", err)
		}
		shuffled = append(shuffled, one...)
	}
	ds, err := c.Unmarshal(shuffled, schema)
	if err != nil || len(ds) != len(dec) {
		return fmt.Errorf("selfcheck: order independence failed: %w", err)
	}
	for num, want := range dec {
		got := ds[num]
		if got.Wire != want.Wire || got.U != want.U || !bytes.Equal(got.B, want.B) {
			return fmt.Errorf("selfcheck: field %d differs by order", num)
		}
	}

	// Five pairwise-distinct failure classes; each must fail entirely
	// (nil result + its sentinel), and the codec must decode golden after.
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"truncated", []byte{0x12, 0x05, 0x41}, wire.ErrTruncated},
		{"overflow", append(bytes.Repeat([]byte{0xff}, 10), 0x00), wire.ErrOverflow},
		{"unknown-wire", []byte{0x0b, 0x00}, wire.ErrUnknownWire},
		{"duplicate", []byte{0x08, 0x01, 0x08, 0x02}, wire.ErrDuplicateField},
		{"zero-field", []byte{0x00, 0x01}, wire.ErrZeroField},
	}
	seen := map[error]bool{}
	for _, tc := range cases {
		m, err := c.Unmarshal(tc.b, map[int]int{1: 0, 2: 2})
		if !errors.Is(err, tc.want) || m != nil {
			return fmt.Errorf("selfcheck: %s not rejected correctly", tc.name)
		}
		if seen[tc.want] {
			return fmt.Errorf("selfcheck: sentinel of %s reused", tc.name)
		}
		seen[tc.want] = true
	}
	if _, err := c.Unmarshal(goldenBytes, schema); err != nil {
		return fmt.Errorf("selfcheck: codec unusable after rejections: %w", err)
	}
	return nil
}
