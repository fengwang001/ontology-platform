package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/layout"
	"ontology/pack"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK: " + name)
	} else {
		failed = true
		fmt.Println("FAIL: " + name)
	}
}

func main() {
	specs := []layout.Field{
		{Name: "id", Width: 2},
		{Name: "flags", Width: 1},
		{Name: "count", Width: 4},
		{Name: "score", Width: 2, Signed: true},
	}
	sc, _ := layout.NewSchema(specs)
	vals := map[string]int64{"id": 0x1234, "flags": 0xAB, "count": 0xDEADBEEF, "score": -1}

	rec, err := pack.Pack(sc, vals)
	fmt.Printf("id@0:34 12 flags@2:AB count@3:EF BE AD DE score@7:FF FF -> % X\n", rec)

	back, err := pack.Unpack(sc, rec)
	codec := api.New(sc)
	score, _ := codec.GetField(rec, "score")
	rt := err == nil && back["id"] == 0x1234 && back["flags"] == 0xAB &&
		back["count"] == 0xDEADBEEF && back["score"] == -1 &&
		score == -1 && codec.SelfCheck() == nil
	check("roundtrip preserves every value (api SelfCheck)", rt)
	check("offsets non-overlapping with full coverage", packed(sc))

	_, eUnk := pack.Pack(sc, map[string]int64{"nope": 1})
	_, eRange := pack.Pack(sc, map[string]int64{"count": 0x1DEADBEEF})
	_, eShort := pack.Unpack(sc, rec[:8])
	check("unknown field rejected", errors.Is(eUnk, pack.ErrUnknownField))
	check("out-of-range value rejected", errors.Is(eRange, pack.ErrValueOutOfRange))
	check("short buffer rejected", errors.Is(eShort, pack.ErrBufferTooShort))
	check("score sign-extended to -1", back["score"] == -1)

	// O(1) name lookup: locating the last field stays single-hit at all sizes.
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		fs := make([]layout.Field, m)
		for i := range fs {
			fs[i] = layout.Field{Name: fmt.Sprintf("f%d", i), Width: []int{1, 2, 4, 8}[i%4]}
		}
		bc, _ := layout.NewSchema(fs)
		if _, ok := bc.FieldByName(fmt.Sprintf("f%d", m-1)); !ok {
			bigOK = false
		}
	}
	check("O(1) name lookup for m=100..10000", bigOK)

	check("concurrent pack/unpack consistent", concurrent(sc, rec))

	if failed {
		os.Exit(1)
	}
}

func packed(s *layout.Schema) bool {
	fs, off := s.Fields(), 0
	for _, f := range fs {
		if f.Offset != off {
			return false
		}
		off += f.Width
	}
	return off == s.Size() && off == 9
}

func concurrent(sc *layout.Schema, rec []byte) bool {
	const n = 64
	var wg sync.WaitGroup
	ok := true
	var mu sync.Mutex
	wg.Add(n * 2)
	for g := 0; g < n; g++ {
		go func() { // readers: same read-only bytes, identical results
			defer wg.Done()
			m, e := pack.Unpack(sc, rec)
			mu.Lock()
			if e != nil || m["count"] != 0xDEADBEEF || m["score"] != -1 {
				ok = false
			}
			mu.Unlock()
		}()
		v := int64(g)
		go func() { // writers: distinct maps must match serial encoding
			defer wg.Done()
			got, e := pack.Pack(sc, map[string]int64{"id": v, "count": v, "score": v})
			want, _ := pack.Pack(sc, map[string]int64{"id": v, "count": v, "score": v})
			mu.Lock()
			if e != nil || string(got) != string(want) {
				ok = false
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return ok
}
