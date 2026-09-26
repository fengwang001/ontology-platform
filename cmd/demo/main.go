// Command demo runs self-contained VLQ/LEB128 checks and prints OK/FAIL.
package main

import (
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/enc"
	"ontology/stream"
)

var fails int

func check(name string, ok bool, detail ...interface{}) {
	if ok {
		fmt.Print("OK ")
	} else {
		fmt.Print("FAIL ")
		fails++
	}
	fmt.Print(name)
	if len(detail) > 0 {
		fmt.Print(" ")
		fmt.Println(detail...)
	} else {
		fmt.Println()
	}
}

func main() {
	vals := []uint64{0, 1, 127, 128, 300, 16383, 16384, 2097151}
	var seq string
	for _, v := range vals {
		seq += fmt.Sprintf("%d:%x ", v, api.EncodeUint(v))
	}
	check("eight vectors", true, seq)

	rt := true
	for i := uint64(0); i < 5000; i++ {
		u := i*2654435761 + 12345
		if gu, n, e := api.DecodeUint(api.EncodeUint(u)); e != nil || gu != u || n != len(api.EncodeUint(u)) {
			rt = false
		}
	}
	for _, s := range []int64{0, 1, -1, 64, -64, math.MaxInt64, math.MinInt64} {
		if gs, n, e := api.DecodeInt(api.EncodeInt(s)); e != nil || gs != s || n != len(api.EncodeInt(s)) {
			rt = false
		}
	}
	check("roundtrip", rt)

	_, _, e1 := api.DecodeUint([]byte{0x80, 0x00})
	check("non-canonical 80 00 rejected", e1 == enc.ErrNonCanonical)
	_, _, e2 := api.DecodeUint([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80})
	check("overflow rejected", e2 == enc.ErrOverflow)
	_, _, e3 := api.DecodeUint(nil)
	check("empty rejected", e3 == enc.ErrEmpty)

	// Rejected read must not move the cursor; reader stays usable.
	r := stream.NewReader([]byte{0x05, 0x80, 0x00})
	v0, _ := r.ReadUint()
	_, bad := r.ReadUint()
	check("cursor unchanged after reject", v0 == 5 && bad == enc.ErrNonCanonical && r.Pos() == 1)

	check("signed -64/64",
		fmt.Sprintf("%x", api.EncodeInt(-64)) == "40" &&
			fmt.Sprintf("%x", api.EncodeInt(64)) == "c000")

	// Single-pass at large m: every byte consumed exactly once.
	const m = 10000
	var buf []byte
	for i := 0; i < m; i++ {
		buf = append(buf, api.EncodeUint(uint64(i)*7919+13)...)
	}
	lr := stream.NewReader(buf)
	for i := 0; i < m; i++ {
		if _, err := lr.ReadUint(); err != nil {
			break
		}
	}
	check("single-pass m=10000", lr.Pos() == len(buf) && lr.Len() == len(buf))

	// Concurrent atomic writes: decode yields exactly the value multiset.
	const n = 128
	w := stream.NewWriter()
	var wg sync.WaitGroup
	want := map[uint64]int{}
	for i := 0; i < n; i++ {
		x := uint64(i)*1_000_003 + 999999
		want[x]++
		wg.Add(1)
		go func() { defer wg.Done(); w.WriteUint(x) }()
	}
	wg.Wait()
	cr := stream.NewReader(w.Bytes())
	got := map[uint64]int{}
	interleaved := false
	for cr.Pos() < cr.Len() {
		x, err := cr.ReadUint()
		if err != nil {
			interleaved = true
			break
		}
		got[x]++
	}
	match := len(got) == len(want)
	for x, c := range want {
		match = match && got[x] == c
	}
	check("concurrent writer atomic", !interleaved && match)

	check("api.SelfCheck", api.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
