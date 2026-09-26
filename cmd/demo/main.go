package main

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"

	"ontology/api"
	"ontology/layout"
	"ontology/pack"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func demoSchema() *layout.Schema {
	s, err := layout.NewSchema([]layout.Spec{
		{Name: "id", Width: 2},
		{Name: "flags", Width: 1},
		{Name: "count", Width: 4},
		{Name: "score", Width: 2, Signed: true},
	})
	if err != nil {
		fmt.Println("FAIL schema:", err)
		os.Exit(1)
	}
	return s
}

func demoValues() map[string]int64 {
	return map[string]int64{"id": 0x1234, "flags": 0xAB, "count": 0xDEADBEEF, "score": -1}
}

func main() {
	s := demoSchema()
	want := []struct {
		name string
		off  int
	}{{"id", 0}, {"flags", 2}, {"count", 3}, {"score", 7}}
	covered := make([]bool, s.Total())
	ok := s.Total() == 9
	for _, w := range want {
		f, found := s.Field(w.name)
		ok = ok && found && f.Offset == w.off
		for j := f.Offset; j < f.Offset+f.Width; j++ {
			ok = ok && !covered[j] // 互不重叠
			covered[j] = true
		}
	}
	for _, c := range covered {
		ok = ok && c // 无空洞
	}
	check("offsets 0/2/3/7 total=9 no-overlap no-hole", ok)

	rec, err := pack.Pack(s, demoValues())
	check("record 34 12 AB EF BE AD DE FF FF",
		err == nil && bytes.Equal(rec, []byte{0x34, 0x12, 0xAB, 0xEF, 0xBE, 0xAD, 0xDE, 0xFF, 0xFF}))

	back, err := pack.Unpack(s, rec)
	check("roundtrip identical", err == nil && maps.Equal(back, demoValues()))
	check("score sign-extends to -1", back["score"] == -1)

	bad := demoValues()
	bad["nope"] = 1
	_, err1 := pack.Pack(s, bad)
	bad = demoValues()
	bad["count"] = 0x1DEADBEEF
	_, err2 := pack.Pack(s, bad)
	_, err3 := pack.Unpack(s, rec[:8])
	check("unknown/overflow/short-buffer rejected",
		errors.Is(err1, pack.ErrUnknownField) && errors.Is(err2, pack.ErrOverflow) && errors.Is(err3, pack.ErrShortBuffer))

	ok = true
	for _, m := range []int{100, 1000, 10000} { // 大 m：末字段可查且偏移正确
		specs := make([]layout.Spec, m)
		for i := range specs {
			specs[i] = layout.Spec{Name: fmt.Sprintf("f%d", i), Width: 1}
		}
		big, err := layout.NewSchema(specs)
		f, found := big.Field(fmt.Sprintf("f%d", m-1))
		ok = ok && err == nil && found && f.Offset == m-1
	}
	check("large-m lookup last field O(1) (asserted in layout tests)", ok)

	const n = 32 // 并发：N 路 Pack 各自值 + N 路 Unpack 同一段字节，对比串行基准
	wantRec, _ := pack.Pack(s, demoValues())
	var wg sync.WaitGroup
	agree := make(chan bool, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(v int64) {
			defer wg.Done()
			vals := demoValues()
			vals["id"] = v
			buf, err := pack.Pack(s, vals)
			serial, _ := pack.Pack(s, vals)
			agree <- err == nil && bytes.Equal(buf, serial)
		}(int64(i))
		go func() {
			defer wg.Done()
			out, err := pack.Unpack(s, wantRec)
			agree <- err == nil && maps.Equal(out, demoValues())
		}()
	}
	wg.Wait()
	close(agree)
	ok = true
	for a := range agree {
		ok = ok && a
	}
	check("concurrent pack/unpack consistent", ok)

	check("api.SelfCheck", api.New(s).SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
