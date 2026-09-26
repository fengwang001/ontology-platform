// Command demo runs end-to-end self-checks for the protobuf wire codec.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/enc"
	"ontology/wire"
)

var failed bool

func check(name string, ok bool) { // one OK/FAIL line each, <=10 total
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

// fiveFields is the section-3 message.
func fiveFields() []wire.Field {
	return []wire.Field{
		{Num: 1, Wire: wire.WireVarint, U: 150},
		{Num: 2, Wire: wire.WireBytes, B: []byte("A")},
		{Num: 3, Wire: wire.WireFixed32, U: 0x01020304},
		{Num: 4, Wire: wire.WireVarint, U: uint64(enc.Zigzag32(-1))},
		{Num: 5, Wire: wire.WireFixed64, U: 0x0807060504030201},
	}
}

func schemaOf(fs []wire.Field) map[int]int {
	s := make(map[int]int, len(fs))
	for _, f := range fs {
		s[f.Num] = f.Wire
	}
	return s
}

func fieldEqual(x, y wire.Field) bool {
	return x.Num == y.Num && x.Wire == y.Wire && x.U == y.U && bytes.Equal(x.B, y.B)
}

func main() {
	fs := fiveFields()
	golden := []byte{
		0x08, 0x96, 0x01, 0x12, 0x01, 0x41, 0x1d, 0x04, 0x03, 0x02, 0x01,
		0x20, 0x01, 0x29, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	}
	encoded, err := wire.Encode(fs)
	check("golden: 5 field key/value bytes + whole message", err == nil && bytes.Equal(encoded, golden))

	got, err := wire.Decode(encoded, schemaOf(fs))
	rt := err == nil && len(got) == 5 && got[1].U == 150 &&
		bytes.Equal(got[2].B, []byte("A")) && got[3].U == 0x01020304 &&
		got[4].U == 1 && got[5].U == 0x0807060504030201
	check("roundtrip: every field value identical", rt)

	// Encode sorts; emit one field at a time in reverse for a shuffled stream.
	var shuffled []byte
	for i := len(fs) - 1; i >= 0; i-- {
		one, _ := wire.Encode([]wire.Field{fs[i]})
		shuffled = append(shuffled, one...)
	}
	d1, e1 := wire.Decode(shuffled, schemaOf(fs))
	d2, e2 := wire.Decode(encoded, schemaOf(fs))
	same := e1 == nil && e2 == nil && len(d1) == len(d2)
	for k, v := range d2 {
		same = same && fieldEqual(v, d1[k])
	}
	check("decode order-independent: shuffled == ascending", same)
	check("zigzag: field 4 of 20 01 unzigzags to -1", int32(enc.Unzigzag32(uint32(got[4].U))) == -1)

	_, eOf := wire.Decode(append(bytes.Repeat([]byte{0xff}, 10), 0), map[int]int{1: 0})
	check("overflow: 10 continuation bytes rejected", errors.Is(eOf, enc.ErrOverflow))
	_, eUw := wire.Decode([]byte{0x0b, 0x00}, map[int]int{1: 0}) // field1 wire3
	check("unknown wire type rejected", errors.Is(eUw, wire.ErrUnknownWire))
	_, eDup := wire.Decode([]byte{0x08, 0x01, 0x08, 0x02}, map[int]int{1: 0})
	_, eZero := wire.Decode([]byte{0x00, 0x01}, map[int]int{1: 0})
	check("duplicate field / field 0 rejected", errors.Is(eDup, wire.ErrDuplicateField) && errors.Is(eZero, wire.ErrZeroField))

	withUnknown := append(append([]byte(nil), golden...), []byte{0x9d, 0x06, 0x01, 0x02, 0x03, 0x04}...)
	sk, eSk := wire.Decode(withUnknown, schemaOf(fs)) // field 99 fixed32
	check("unknown field skipped by wire type", eSk == nil && len(sk) == 5 && sk[1].U == 150)

	c := api.New()
	o1 := true
	for _, m := range []int{100, 1000, 10000} {
		big := make([]wire.Field, m)
		bs := make(map[int]int, m)
		for i := range big {
			big[i] = wire.Field{Num: i + 1, Wire: wire.WireVarint, U: uint64(i + 1)}
			bs[i+1] = wire.WireVarint
		}
		buf, e := c.Marshal(big)
		dec, e2 := c.Unmarshal(buf, bs)
		last, ok := c.GetField(dec, m)
		o1 = o1 && e == nil && e2 == nil && ok && last.U == uint64(m)
	}
	check("O(1) get of last field for m in 100..10000", o1)
	check("concurrent marshal/unmarshal + SelfCheck race-free", concurrentCheck(c))

	if failed {
		os.Exit(1)
	}
}

// concurrentCheck: N readers share one buffer, N writers marshal distinct
// messages, N run SelfCheck. WaitGroup only, no sleep.
func concurrentCheck(c *api.Codec) bool {
	const n = 32
	shared, _ := c.Marshal(fiveFields())
	schema := schemaOf(fiveFields())
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := true
	fail := func() { mu.Lock(); ok = false; mu.Unlock() }
	for g := 0; g < n; g++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			m, e := c.Unmarshal(shared, schema)
			if e != nil || len(m) != 5 || m[1].U != 150 {
				fail()
			}
		}()
		go func(g int) {
			defer wg.Done()
			b, e := c.Marshal([]wire.Field{{Num: 1, Wire: 0, U: uint64(g + 7)}})
			m, e2 := c.Unmarshal(b, map[int]int{1: 0})
			if e != nil || e2 != nil || m[1].U != uint64(g+7) {
				fail()
			}
		}(g)
		go func() {
			defer wg.Done()
			if c.SelfCheck() != nil {
				fail()
			}
		}()
	}
	wg.Wait()
	return ok
}
