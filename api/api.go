// Package api is the public facade: bitfield pack/unpack and bitmap ops.
package api

import (
	"errors"
	"fmt"

	"ontology/bits"
	"ontology/pack"
)

// bitmapBits is the fixed capacity of each API instance's bitmap.
const bitmapBits = 1024

// API couples bitfield packing with a concurrency-safe bitmap.
type API struct {
	bm *pack.Bitmap
}

// New returns an API with an empty bitmap of bitmapBits bits.
func New() *API { return &API{bm: pack.NewBitmap(bitmapBits)} }

// Pack packs values per the given widths/signs, least-significant-bits first.
func (a *API) Pack(widths []int, signed []bool, values ...int64) (uint64, error) {
	s, err := pack.NewSchema(widths, signed)
	if err != nil {
		return 0, err
	}
	return s.Pack(values...)
}

// Unpack extracts fields from word per the given widths/signs.
func (a *API) Unpack(word uint64, widths []int, signed []bool) ([]int64, error) {
	s, err := pack.NewSchema(widths, signed)
	if err != nil {
		return nil, err
	}
	return s.Unpack(word)
}

// Set sets bit idx in the bitmap.
func (a *API) Set(idx int) error { return a.bm.Set(idx) }

// Clear clears bit idx in the bitmap.
func (a *API) Clear(idx int) error { return a.bm.Clear(idx) }

// Test reports whether bit idx is set.
func (a *API) Test(idx int) (bool, error) { return a.bm.Test(idx) }

func fullMask(w int) uint64 { return ^uint64(0) >> (64 - min(w, 64)) }

// checkVectors are the built-in SelfCheck vectors (signed negatives included).
var checkVectors = [][]bits.Field{
	{{Width: 3, Value: 5}, {Width: 5, Value: 18}, {Width: 4, Value: -3, Signed: true}, {Width: 2, Value: 1}},
	{{Width: 1, Value: 1}, {Width: 63, Value: -1, Signed: true}},
	{{Width: 64, Value: -1 << 63, Signed: true}},
	{{Width: 8, Value: 200}, {Width: 8, Value: -100, Signed: true}, {Width: 16, Value: 50000}},
}

// SelfCheck verifies the four invariants against the built-in vectors:
// round-trip, gapless non-overlapping layout, naive-reference equality,
// and rejected operations leaving no trace.
func (a *API) SelfCheck() error {
	for i, fs := range checkVectors { // invariants 1, 2(total<=64) and 3
		word, err := bits.PackFields(fs)
		if err != nil {
			return fmt.Errorf("selfcheck: pack vector %d: %w", i, err)
		}
		var naive uint64
		off := 0
		for j, f := range fs {
			naive |= (uint64(f.Value) & fullMask(f.Width)) << off
			if got := bits.Extract(word, off, f.Width, f.Signed); got != f.Value {
				return fmt.Errorf("selfcheck: roundtrip vector %d field %d: got %d, want %d", i, j, got, f.Value)
			}
			off += f.Width
		}
		if word != naive {
			return fmt.Errorf("selfcheck: vector %d packed %#x != naive %#x", i, word, naive)
		}
		if off > 64 {
			return errors.New("selfcheck: total width exceeds 64")
		}
	}
	off := 0 // invariant 2: an all-ones field occupies exactly [off, off+width)
	for i, w := range []int{3, 5, 4, 2} {
		fs := []bits.Field{{Width: 3}, {Width: 5}, {Width: 4}, {Width: 2}}
		fs[i] = bits.Field{Width: w, Signed: true, Value: -1}
		word, err := bits.PackFields(fs)
		if err != nil {
			return fmt.Errorf("selfcheck: layout field %d: %w", i, err)
		}
		if want := fullMask(w) << off; word != want {
			return fmt.Errorf("selfcheck: layout field %d: got %#x, want %#x", i, word, want)
		}
		off += w
	}
	if err := a.Set(0); err != nil { // invariant 4: rejections leave no trace
		return fmt.Errorf("selfcheck: setup set: %w", err)
	}
	if _, err := a.Pack([]int{3}, []bool{false}, 9); !errors.Is(err, bits.ErrValueOverflow) {
		return fmt.Errorf("selfcheck: overflow not rejected: %v", err)
	}
	if err := a.Set(bitmapBits); !errors.Is(err, bits.ErrBitIndex) {
		return fmt.Errorf("selfcheck: bit index not rejected: %v", err)
	}
	if _, err := a.Pack([]int{0}, []bool{false}, 0); !errors.Is(err, bits.ErrBadWidth) {
		return fmt.Errorf("selfcheck: bad width not rejected: %v", err)
	}
	if ok, err := a.Test(0); err != nil || !ok {
		return errors.New("selfcheck: bitmap unusable after rejections")
	}
	if ok, _ := a.Test(1); ok {
		return errors.New("selfcheck: rejected set leaked into bitmap")
	}
	return nil
}
