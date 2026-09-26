package bits_test

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/bits"
)

func mask64(w int) uint64 { return ^uint64(0) >> (64 - min(w, 64)) }

var specFields = []bits.Field{{3, 5, false}, {5, 18, false}, {4, -3, true}, {2, 1, false}}

func TestPackFields(t *testing.T) {
	cases := []struct {
		fields []bits.Field
		want   uint64
		err    error
	}{
		{specFields, 7573, nil},
		{[]bits.Field{{64, -1 << 63, true}}, 1 << 63, nil},
		{[]bits.Field{{3, 9, false}}, 0, bits.ErrValueOverflow},
		{[]bits.Field{{4, 8, true}}, 0, bits.ErrValueOverflow},
		{[]bits.Field{{0, 0, false}}, 0, bits.ErrBadWidth},
		{[]bits.Field{{65, 0, false}}, 0, bits.ErrBadWidth},
		{[]bits.Field{{33, 0, false}, {32, 0, false}}, 0, bits.ErrBadWidth},
	}
	for _, c := range cases {
		if got, err := bits.PackFields(c.fields); !errors.Is(err, c.err) || got != c.want {
			t.Errorf("%v: got (%d,%v), want (%d,%v)", c.fields, got, err, c.want, c.err)
		}
	}
	if errors.Is(bits.ErrValueOverflow, bits.ErrBitIndex) || errors.Is(bits.ErrBadWidth, bits.ErrValueOverflow) || errors.Is(bits.ErrBitIndex, bits.ErrBadWidth) {
		t.Error("sentinel errors must be mutually distinct")
	}
	word, _ := bits.PackFields(specFields)
	if bits.Extract(word, 8, 4, true) != -3 || bits.Extract(word, 8, 4, false) != 13 {
		t.Error("f2: sign-extended must be -3, zero-extended must be 13")
	}
}

func randFields(r *rand.Rand) []bits.Field {
	var fs []bits.Field
	total := 0
	for n := r.Intn(6) + 1; n > 0 && total < 64; n-- {
		w := r.Intn(64-total) + 1
		total += w
		f := bits.Field{Width: w, Signed: r.Intn(2) == 0}
		if f.Signed {
			f.Value = bits.Extract(r.Uint64(), 0, w, true) // random in-range signed value
		} else if w == 64 {
			f.Value = r.Int63() // unsigned 64-bit range is [0, MaxInt64]
		} else {
			f.Value = int64(r.Uint64() & mask64(w))
		}
		fs = append(fs, f)
	}
	return fs
}

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		fs := randFields(r)
		word, err := bits.PackFields(fs)
		if err != nil {
			t.Fatalf("pack: %v", err)
		}
		off := 0
		for j, f := range fs {
			if got := bits.Extract(word, off, f.Width, f.Signed); got != f.Value {
				t.Fatalf("iter %d field %d: got %d, want %d", i, j, got, f.Value)
			}
			off += f.Width
		}
	}
}

func TestNaiveReference(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	vecs := [][]bits.Field{specFields}
	for i := 0; i < 200; i++ {
		vecs = append(vecs, randFields(r))
	}
	for i, fs := range vecs {
		var naive uint64
		off := 0
		for _, f := range fs { // textbook reference: value << off, accumulated
			naive |= (uint64(f.Value) & mask64(f.Width)) << off
			off += f.Width
		}
		if got, err := bits.PackFields(fs); err != nil || got != naive {
			t.Fatalf("vec %d: got (%d,%v), naive %d", i, got, err, naive)
		}
	}
}

func TestLayout(t *testing.T) {
	for _, ws := range [][]int{{3, 5, 4, 2}, {1, 63}, {64}} {
		off := 0
		for i, w := range ws { // field i all-ones, rest zero: word must equal mask<<off
			fs := make([]bits.Field, len(ws))
			for j, wj := range ws { // zero-valued neighbours
				fs[j] = bits.Field{Width: wj}
			}
			fs[i] = bits.Field{Width: w, Signed: true, Value: -1} // all-ones pattern
			word, err := bits.PackFields(fs)
			if err != nil {
				t.Fatalf("pack: %v", err)
			}
			if want := mask64(w) << off; word != want {
				t.Fatalf("widths %v field %d: got %#x, want %#x", ws, i, word, want)
			}
			off += w
		}
	}
}

func TestRejectedNoSideEffect(t *testing.T) {
	b := []byte{0xA5, 0x3C}
	snap := append([]byte(nil), b...)
	ops := []func([]byte, int) error{
		bits.SetBit, bits.ClearBit,
		func(b []byte, i int) error { _, err := bits.TestBit(b, i); return err },
	}
	for _, op := range ops {
		for _, idx := range []int{-1, 16, 100} {
			if err := op(b, idx); !errors.Is(err, bits.ErrBitIndex) {
				t.Errorf("idx %d: want ErrBitIndex, got %v", idx, err)
			}
		}
	}
	if !bytes.Equal(b, snap) {
		t.Fatalf("bitmap mutated by rejected op: %x != %x", b, snap)
	}
	if _, err := bits.PackFields([]bits.Field{{3, 9, false}}); !errors.Is(err, bits.ErrValueOverflow) {
		t.Error("overflow must be rejected")
	}
	if err := bits.SetBit(b, 3); err != nil { // still usable afterwards
		t.Fatalf("bitmap unusable after rejections: %v", err)
	}
}
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
