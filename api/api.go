// Package api is the public fixed-width record codec (New, Marshal,
// Unmarshal, GetField, SelfCheck); it depends only on pack and layout.
package api

import (
	"encoding/binary"
	"errors"

	"ontology/layout"
	"ontology/pack"
)

// Codec binds one fixed schema to the pack/unpack operations.
type Codec struct{ schema *layout.Schema }

// New binds a schema built without error.
func New(s *layout.Schema) *Codec { return &Codec{schema: s} }

// Marshal encodes a name→value map into the fixed-width record.
func (c *Codec) Marshal(v map[string]int64) ([]byte, error) { return pack.Pack(c.schema, v) }

// Unmarshal decodes a whole record into a name→value map.
func (c *Codec) Unmarshal(b []byte) (map[string]int64, error) {
	return pack.Unpack(c.schema, b)
}

// GetField decodes one field; unknown name vs span-too-short are distinct.
func (c *Codec) GetField(buf []byte, name string) (int64, error) {
	f, ok := c.schema.FieldByName(name)
	if !ok {
		return 0, pack.ErrUnknownField
	}
	if len(buf) < f.Offset+f.Width {
		return 0, pack.ErrBufferTooShort
	}
	var b [8]byte
	copy(b[:], buf[f.Offset:f.Offset+f.Width])
	u := binary.LittleEndian.Uint64(b[:])
	if f.Signed && f.Width < 8 { // sign-extend narrower signed fields
		if bits := uint(8 * f.Width); u&(uint64(1)<<(bits-1)) != 0 {
			return int64(u) - int64(uint64(1)<<bits), nil
		}
	}
	return int64(u), nil
}

// SelfCheck verifies the four invariants over built-in schemas and vectors.
func (c *Codec) SelfCheck() error {
	for _, fn := range []func() error{
		checkCanonical, checkRoundTrip, checkOffsets, checkAtomic,
	} {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

// checkCanonical: NOTES.md vector, round-trip, and byte equality with an
// independent encoding/binary reference.
func checkCanonical() error {
	s, _ := layout.NewSchema([]layout.Field{
		{Name: "id", Width: 2}, {Name: "flags", Width: 1}, {Name: "count", Width: 4},
		{Name: "score", Width: 2, Signed: true},
	})
	v := map[string]int64{"id": 0x1234, "flags": 0xAB, "count": 0xDEADBEEF, "score": -1}
	want := []byte{0x34, 0x12, 0xAB, 0xEF, 0xBE, 0xAD, 0xDE, 0xFF, 0xFF}
	c := New(s)
	got, err := c.Marshal(v)
	if err != nil || string(got) != string(want) {
		return errors.New("api: canonical vector mismatch")
	}
	back, err := c.Unmarshal(got)
	if err != nil {
		return err
	}
	for k, x := range v {
		if back[k] != x {
			return errors.New("api: canonical round-trip mismatch")
		}
	}
	ref := make([]byte, s.Size()) // independent textbook stdlib encoder
	for _, f := range s.Fields() {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(v[f.Name]))
		copy(ref[f.Offset:], b[:f.Width])
	}
	if string(ref) != string(want) {
		return errors.New("api: stdlib reference mismatch")
	}
	return nil
}

func checkRoundTrip() error {
	for _, signed := range []bool{false, true} {
		for _, w := range []int{1, 2, 4, 8} {
			s, _ := layout.NewSchema([]layout.Field{{Name: "v", Width: w, Signed: signed}})
			bits := uint(8 * w)
			vs := []int64{0, -1, int64(-1) << (bits - 1), int64(1)<<(bits-1) - 1}
			if !signed {
				vs = []int64{0, int64(^uint64(0) >> (64 - bits))}
			}
			for _, x := range vs {
				b, err := New(s).Marshal(map[string]int64{"v": x})
				if err != nil {
					return err
				}
				if m, err := New(s).Unmarshal(b); err != nil || m["v"] != x {
					return errors.New("api: round-trip mismatch")
				}
			}
		}
	}
	return nil
}

func checkOffsets() error {
	s, _ := layout.NewSchema([]layout.Field{
		{Name: "a", Width: 8}, {Name: "b", Width: 1}, {Name: "c", Width: 2}, {Name: "d", Width: 4},
	})
	off := 0
	for _, f := range s.Fields() {
		if f.Offset != off {
			return errors.New("api: offset not running sum")
		}
		off += f.Width
	}
	if off != s.Size() {
		return errors.New("api: fields leave a gap or overrun")
	}
	return nil
}

func checkAtomic() error {
	s, _ := layout.NewSchema([]layout.Field{{Name: "u8", Width: 1}})
	c := New(s)
	if b, err := c.Marshal(map[string]int64{"x": 1}); err != pack.ErrUnknownField || b != nil {
		return errors.New("api: unknown rejection not atomic")
	}
	if b, err := c.Marshal(map[string]int64{"u8": 256}); err != pack.ErrValueOutOfRange || b != nil {
		return errors.New("api: range rejection not atomic")
	}
	if m, err := c.Unmarshal(nil); err != pack.ErrBufferTooShort || m != nil {
		return errors.New("api: short-buffer rejection not atomic")
	}
	if b, err := c.Marshal(map[string]int64{"u8": 7}); err != nil || b[0] != 7 {
		return errors.New("api: codec unusable after rejection")
	}
	return nil
}
