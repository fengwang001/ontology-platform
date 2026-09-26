package pack_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/layout"
	"ontology/pack"
)

var canon, _ = layout.NewSchema([]layout.Field{{Name: "id", Width: 2}, {Name: "flags", Width: 1}, {Name: "count", Width: 4}, {Name: "score", Width: 2, Signed: true}})
var canonRec = []byte{0x34, 0x12, 0xAB, 0xEF, 0xBE, 0xAD, 0xDE, 0xFF, 0xFF}
var trng = rand.New(rand.NewSource(7))

// naiveLE is the independent textbook offset-by-offset little-endian writer.
func naiveLE(s *layout.Schema, v map[string]int64) []byte {
	b := make([]byte, s.Size())
	for _, f := range s.Fields() {
		for j := 0; j < f.Width; j++ {
			b[f.Offset+j] = byte(uint64(v[f.Name]) >> (8 * uint(j)))
		}
	}
	return b
}

func rr() map[string]int64 {
	return map[string]int64{"id": int64(trng.Uint64() & 0xFFFF), "flags": int64(trng.Uint64() & 0xFF), "count": int64(trng.Uint64() & 0xFFFFFFFF), "score": int64(int16(trng.Uint64()))}
}

func TestRoundTrip(t *testing.T) {
	rt := func(v map[string]int64) {
		b, e1 := pack.Pack(canon, v)
		m, e2 := pack.Unpack(canon, b)
		if e1 != nil || e2 != nil || !reflect.DeepEqual(m, v) {
			t.Fatalf("roundtrip %v -> %v (%v/%v)", v, m, e1, e2)
		}
	}
	for _, v := range []map[string]int64{
		{"id": 0, "flags": 0, "count": 0, "score": 0},
		{"id": 0x1234, "flags": 0xAB, "count": 0xDEADBEEF, "score": -1},
		{"id": 65535, "flags": 255, "count": 0xFFFFFFFF, "score": 32767},
		{"id": 0, "flags": 0, "count": 0, "score": -32768},
	} {
		rt(v)
	}
	for i := 0; i < 64; i++ {
		rt(rr())
	}
}

func TestNaiveReference(t *testing.T) {
	v := map[string]int64{"id": 0x1234, "flags": 0xAB, "count": 0xDEADBEEF, "score": -1}
	if got, _ := pack.Pack(canon, v); string(got) != string(canonRec) {
		t.Fatalf("pack canonical = % X", got)
	}
	if got := naiveLE(canon, v); string(got) != string(canonRec) {
		t.Fatalf("naive canonical = % X", got)
	}
	for i := 0; i < 64; i++ {
		x := rr()
		if got, _ := pack.Pack(canon, x); string(got) != string(naiveLE(canon, x)) {
			t.Fatalf("case %d diverges from naive", i)
		}
	}
}

func TestRejectionsAtomic(t *testing.T) {
	cases := []struct {
		v    map[string]int64
		want error
	}{
		{map[string]int64{"nope": 1}, pack.ErrUnknownField},
		{map[string]int64{"count": 0x1DEADBEEF}, pack.ErrValueOutOfRange},
		{map[string]int64{"score": 32768}, pack.ErrValueOutOfRange},
		{map[string]int64{"score": -32769}, pack.ErrValueOutOfRange},
		{map[string]int64{"id": -1}, pack.ErrValueOutOfRange},
	}
	for _, tc := range cases {
		if b, e := pack.Pack(canon, tc.v); !errors.Is(e, tc.want) || b != nil {
			t.Fatalf("pack %v: %v", tc.v, e)
		}
	}
	if m, e := pack.Unpack(canon, make([]byte, 8)); !errors.Is(e, pack.ErrBufferTooShort) || m != nil {
		t.Fatalf("short buffer: %v", e)
	}
	if pack.ErrUnknownField == pack.ErrValueOutOfRange || pack.ErrUnknownField == pack.ErrBufferTooShort || pack.ErrValueOutOfRange == pack.ErrBufferTooShort {
		t.Fatal("sentinel errors must be distinct")
	}
	if b, e := pack.Pack(canon, map[string]int64{"id": 1}); e != nil || b[0] != 1 {
		t.Fatalf("schema unusable after rejection: %v", e)
	}
}

func TestConcurrentPackUnpack(t *testing.T) {
	want, _ := pack.Unpack(canon, canonRec)
	const n = 64
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < n; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m, e := pack.Unpack(canon, canonRec)
			if e != nil || !reflect.DeepEqual(m, want) {
				bad.Store(true)
			}
		}()
		v := map[string]int64{"id": int64(g), "flags": 0, "count": int64(g) * 7, "score": int64(g) - 32}
		ser, _ := pack.Pack(canon, v)
		go func() {
			defer wg.Done()
			b, e := pack.Pack(canon, v)
			if e != nil || string(b) != string(ser) {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent result diverged")
	}
}

func TestSelfCheck(t *testing.T) {
	c := api.New(canon)
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if v, e := c.GetField(canonRec, "score"); e != nil || v != -1 {
		t.Fatalf("GetField score: %d %v", v, e)
	}
	if _, e := c.GetField(canonRec, "nope"); !errors.Is(e, pack.ErrUnknownField) {
		t.Fatalf("GetField unknown: %v", e)
	}
	if _, e := c.GetField(canonRec[:8], "score"); !errors.Is(e, pack.ErrBufferTooShort) {
		t.Fatalf("GetField short span: %v", e)
	}
}
