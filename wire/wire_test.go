package wire

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"ontology/enc"
	"reflect"
	"testing"
)

var goldenBytes = []byte{0x08, 0x96, 0x01, 0x12, 0x01, 0x41, 0x1d, 0x04, 0x03, 0x02, 0x01, 0x20, 0x01, 0x29, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
var goldenFields = []Field{
	{Num: 1, Wire: WireVarint, U: 150},
	{Num: 2, Wire: WireBytes, B: []byte("A")},
	{Num: 3, Wire: WireFixed32, U: 0x01020304},
	{Num: 4, Wire: WireVarint, U: uint64(enc.Zigzag32(-1))},
	{Num: 5, Wire: WireFixed64, U: 0x0807060504030201},
}
var goldenSchema = map[int]int{1: 0, 2: 2, 3: 5, 4: 0, 5: 1}

func schemaOf(fs []Field) map[int]int {
	s := make(map[int]int, len(fs))
	for _, f := range fs {
		s[f.Num] = f.Wire
	}
	return s
}
func mustDec(t *testing.T, b []byte, sc map[int]int) map[int]Field {
	m, err := Decode(b, sc)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestGoldenFiveFields(t *testing.T) { // invariant 3: golden bytes + zigzag
	got, err := Encode(goldenFields)
	if err != nil || !bytes.Equal(got, goldenBytes) {
		t.Fatalf("encode=%x err=%v", got, err)
	}
	m := mustDec(t, goldenBytes, goldenSchema)
	if len(m) != 5 || m[4].U != 1 || int32(enc.Unzigzag32(uint32(m[4].U))) != -1 {
		t.Fatalf("decode golden wrong: %+v", m)
	}
}
func TestRejectedDecodeNoPartial(t *testing.T) { // invariant 4: nil + 5 distinct sentinels
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"truncated-bytes", []byte{0x12, 0x05, 0x41}, enc.ErrTruncated},
		{"truncated-fixed", []byte{0x0d, 1, 2, 3}, enc.ErrTruncated},
		{"overflow", bytes.Repeat([]byte{0xff}, 10), enc.ErrOverflow},
		{"unknown-wire", []byte{0x0b, 0x00}, ErrUnknownWire},
		{"duplicate", []byte{0x08, 0x01, 0x08, 0x02}, ErrDuplicateField},
		{"zero-field", []byte{0x00, 0x01}, ErrZeroField},
	}
	classes := map[error]bool{}
	for _, tc := range cases {
		m, err := Decode(tc.b, map[int]int{1: 0, 2: 2})
		if !errors.Is(err, tc.want) || m != nil {
			t.Fatalf("%s: m=%v err=%v, want nil + %v", tc.name, m, err, tc.want)
		}
		classes[tc.want] = true
	}
	if len(classes) != 5 {
		t.Fatalf("want 5 distinct error classes, got %d", len(classes))
	}
	if m := mustDec(t, goldenBytes, goldenSchema); len(m) != 5 {
		t.Fatalf("codec unusable after rejections")
	}
}
func TestDecodeOrderIndependent(t *testing.T) { // invariant 2: shuffled == ascending
	asc, _ := Encode(goldenFields)
	var shuffled []byte
	for i := len(goldenFields) - 1; i >= 0; i-- {
		one, _ := Encode([]Field{goldenFields[i]})
		shuffled = append(shuffled, one...)
	}
	if !reflect.DeepEqual(mustDec(t, asc, goldenSchema), mustDec(t, shuffled, goldenSchema)) {
		t.Fatalf("decode depends on field order")
	}
}
func TestUnknownFieldsSkipped(t *testing.T) { // unknown numbers consumed by wire type
	raw := append(append([]byte(nil), goldenBytes...),
		[]byte{0x9d, 0x06, 0x01, 0x02, 0x03, 0x04, 0x98, 0x01, 0x07}...) // f99 f32, f19 varint
	if m := mustDec(t, raw, goldenSchema); len(m) != 5 {
		t.Fatalf("unknown field not skipped, len=%d", len(m))
	}
}
func TestNonCanonicalVarintAndKey(t *testing.T) { // 80 00 == 0; key split
	if m := mustDec(t, []byte{0x08, 0x80, 0x00}, map[int]int{1: 0}); m[1].U != 0 {
		t.Fatalf("80 00 decoded to %d, want 0", m[1].U)
	}
	if enc.KeyNum(797) != 99 || enc.KeyWire(797) != WireFixed32 { // bytes 9D 06
		t.Fatalf("key split wrong: num=%d wire=%d", enc.KeyNum(797), enc.KeyWire(797))
	}
}
func randomFields(rng *rand.Rand, n int) []Field {
	fs := make([]Field, n)
	for i := range fs {
		fs[i].Num = i + 1
		switch rng.IntN(4) {
		case 0, 1:
			fs[i].Wire, fs[i].U = rng.IntN(2), rng.Uint64()
		case 2:
			fs[i].Wire, fs[i].B = WireBytes, make([]byte, rng.IntN(24))
		case 3:
			fs[i].Wire, fs[i].U = WireFixed32, uint64(rng.Uint32())
		}
	}
	return fs
}
func TestRandomRoundTrip(t *testing.T) { // invariant 1 across sizes/wire types
	rng := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{1, 7, 50, 200} {
		fs := randomFields(rng, n)
		raw, err := Encode(fs)
		if err != nil {
			t.Fatal(err)
		}
		m := mustDec(t, raw, schemaOf(fs))
		for _, f := range fs {
			if g := m[f.Num]; g.Wire != f.Wire || g.U != f.U || !bytes.Equal(g.B, f.B) {
				t.Fatalf("n=%d field %d: %+v != %+v", n, f.Num, g, f)
			}
		}
	}
}
func TestProbeCountO1(t *testing.T) { // comparisons do not grow with m
	rng := rand.New(rand.NewPCG(3, 4))
	first := -1
	for _, m := range []int{100, 1000, 5000, 10000} {
		fs := randomFields(rng, m)
		raw, _ := Encode(fs)
		tab, err := decodeTable(raw, schemaOf(fs))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := tab.lookup(m)
		if f.U != fs[m-1].U || tab.probeCount > 2 {
			t.Fatalf("m=%d wrong value or probes=%d", m, tab.probeCount)
		}
		if first >= 0 && tab.probeCount != first {
			t.Fatalf("probe count grew with m: %d -> %d", first, tab.probeCount)
		}
		first = tab.probeCount
	}
}
