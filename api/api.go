// Package api is the outward-facing facade over pack and bits. It exposes
// New, Pack, Unpack, Set, Clear, Test and SelfCheck, and depends only on
// pack (and indirectly bits); the dependency direction never reverses.
package api

import (
	"errors"

	"ontology/bits"
	"ontology/pack"
)

// defaultBits is the fixed in-process bitmap capacity exposed by the
// facade; indices >= defaultBits are rejected as out of range.
const defaultBits = 1 << 20

// API holds the process-local bitmap. Packing is stateless apart from
// validating each supplied schema.
type API struct {
	bmp *pack.Bitmap
}

// New constructs a facade with an empty defaultBits-bit bitmap.
func New() *API {
	return &API{bmp: pack.NewBitmap(defaultBits)}
}

// Pack validates and packs the given fields LSB-first. Any illegal width
// or out-of-range value rejects the whole operation and returns (0, err).
func (a *API) Pack(fields []bits.Field) (uint64, error) {
	s, err := pack.NewSchema(fields) // validates widths and total <= 64
	if err != nil {
		return 0, err
	}
	values := make([]int64, len(fields))
	for i, f := range fields {
		values[i] = f.Value
	}
	return s.Pack(values) // validates ranges, then packs
}

// Unpack builds the schema described by fields and extracts every field
// from word in order.
func (a *API) Unpack(fields []bits.Field, word uint64) ([]int64, error) {
	s, err := pack.NewSchema(fields)
	if err != nil {
		return nil, err
	}
	return s.Unpack(word), nil
}

// Set sets bit i of the in-process bitmap.
func (a *API) Set(i int) error { return a.bmp.Set(i) }

// Clear clears bit i of the in-process bitmap.
func (a *API) Clear(i int) error { return a.bmp.Clear(i) }

// Test reports bit i of the in-process bitmap.
func (a *API) Test(i int) (bool, error) { return a.bmp.Test(i) }

// notesFields / notesWord are the canonical NOTES.md vector.
var notesFields = []bits.Field{
	{Width: 3, Value: 5, Signed: false},
	{Width: 5, Value: 18, Signed: false},
	{Width: 4, Value: -3, Signed: true},
	{Width: 2, Value: 1, Signed: false},
}

const notesWord = uint64(7573)

// SelfCheck verifies the four invariants on built-in vectors:
// round trip; contiguous non-overlapping layout; equality with a textbook
// naive reference; and rejection leaves no trace. It also confirms O(1)
// bitmap location. It returns nil only when every check passes.
func (a *API) SelfCheck() error {
	word, err := a.Pack(notesFields)
	if err != nil {
		return err
	}
	if word != notesWord { // canonical packed result 7573 (0x1D95)
		return errors.New("api: selfcheck pack mismatch")
	}
	got, err := a.Unpack(notesFields, word) // invariant 1
	if err != nil {
		return err
	}
	for i, f := range notesFields {
		if got[i] != f.Value {
			return errors.New("api: selfcheck round trip failed")
		}
	}
	s, _ := pack.NewSchema(notesFields) // invariant 2
	off := 0
	for _, r := range s.Ranges() {
		if r.Off != off {
			return errors.New("api: selfcheck layout gap/overlap")
		}
		off += r.Width
	}
	if off > 64 {
		return errors.New("api: selfcheck width overflow")
	}
	var ref uint64 // invariant 3: textbook value<<off accumulation
	o := 0
	for _, f := range notesFields {
		p := uint64(f.Value) & (uint64(1)<<uint(f.Width) - 1)
		ref |= p << uint(o)
		o += f.Width
	}
	if ref != word {
		return errors.New("api: selfcheck naive reference mismatch")
	}
	if w, err := a.Pack([]bits.Field{{Width: 3, Value: 9}}); // invariant 4
	err == nil || w != 0 || !errors.Is(err, bits.ErrValueOverflow) {
		return errors.New("api: selfcheck overflow rejection failed")
	}
	if _, err := a.Pack([]bits.Field{{Width: 0}}); !errors.Is(err, bits.ErrBadWidth) {
		return errors.New("api: selfcheck bad-width rejection failed")
	}
	if err := a.Set(defaultBits); !errors.Is(err, bits.ErrBitIndex) {
		return errors.New("api: selfcheck bit-index rejection failed")
	}
	if err := a.Set(0); err != nil { // still usable after rejections
		return err
	}
	if ok, _ := a.Test(0); !ok {
		return errors.New("api: selfcheck bitmap unusable after rejection")
	}
	return pack.CheckConstantTime() // O(1) location across m=100..10000
}
