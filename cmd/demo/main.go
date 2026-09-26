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

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	a := api.New()
	schema := map[int]int{1: 0, 2: 2, 3: 5, 4: 0, 5: 1}
	msg := []wire.Field{
		{Num: 1, Wire: 0, U: 150},
		{Num: 2, Wire: 2, B: []byte("A")},
		{Num: 3, Wire: 5, U: 0x01020304},
		{Num: 4, Wire: 0, U: uint64(enc.Zigzag32(-1))},
		{Num: 5, Wire: 1, U: 0x0807060504030201},
	}
	b, err := a.Marshal(msg)
	check("5-field msg bytes 0896011201411D040302012001290102030405060708",
		err == nil && fmt.Sprintf("%X", b) == "0896011201411D040302012001290102030405060708")

	dec, err := a.Unmarshal(b, schema)
	rt := err == nil && len(dec) == 5
	for _, f := range msg {
		g, ok := a.GetField(dec, f.Num)
		rt = rt && ok && g.U == f.U && bytes.Equal(g.B, f.B)
	}
	check("round-trip", rt)

	sh, _ := a.Marshal([]wire.Field{msg[4], msg[2], msg[0], msg[3], msg[1]})
	dec2, err := a.Unmarshal(sh, schema)
	same := err == nil && len(dec2) == 5
	for n, f := range dec {
		same = same && dec2[n].U == f.U && bytes.Equal(dec2[n].B, f.B)
	}
	check("order-independent decode", same)

	f4, _ := a.GetField(dec, 4)
	check("zigzag(-1) round-trips to -1", enc.Unzigzag32(uint32(f4.U)) == -1)

	rej := func(b []byte, want error) bool {
		m, err := a.Unmarshal(b, schema)
		return m == nil && errors.Is(err, want)
	}
	check("reject: truncated/overflow/badwire/dup/zero all distinct",
		rej([]byte{0x12, 0x05, 0x41}, wire.ErrTruncated) &&
			rej([]byte{0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, wire.ErrOverflow) &&
			rej([]byte{0x0B, 0x00}, wire.ErrBadWireType) &&
			rej([]byte{0x08, 0x01, 0x08, 0x02}, wire.ErrDuplicateField) &&
			rej([]byte{0x00, 0x00}, wire.ErrFieldNumZero))

	// 9D 06 = field 99 wire 5 (unknown, 4 bytes skipped), then field 2 = "A".
	unk, err := a.Unmarshal([]byte{0x9D, 0x06, 1, 2, 3, 4, 0x12, 0x01, 0x41}, schema)
	u2, ok2 := a.GetField(unk, 2)
	check("unknown field skipped by wire type", err == nil && len(unk) == 1 && ok2 && string(u2.B) == "A")

	check("SelfCheck", a.SelfCheck() == nil)

	const m = 10000
	big := make([]wire.Field, m)
	bigSchema := map[int]int{}
	for i := range big {
		big[i] = wire.Field{Num: i + 1, Wire: 0, U: uint64(i) * 7}
		bigSchema[i+1] = 0
	}
	bb, _ := a.Marshal(big)
	bd, _ := a.Unmarshal(bb, bigSchema)
	last, okLast := a.GetField(bd, m)
	check("large-m O(1) lookup", okLast && last.U == uint64(m-1)*7)

	var wg sync.WaitGroup
	var mu sync.Mutex
	good := true
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			d, e := a.Unmarshal(b, schema)
			mu.Lock()
			good = good && e == nil && len(d) == 5
			mu.Unlock()
		}()
		go func(v int) {
			defer wg.Done()
			bb, e := a.Marshal([]wire.Field{{Num: 1, Wire: 0, U: uint64(v)}})
			d, e2 := a.Unmarshal(bb, map[int]int{1: 0})
			mu.Lock()
			good = good && e == nil && e2 == nil && d[1].U == uint64(v)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	check("concurrent marshal/unmarshal", good)

	if failed {
		os.Exit(1)
	}
}
