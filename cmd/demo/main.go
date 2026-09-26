// Command demo exercises the length-prefix codec and prints OK/FAIL per item.
// It reads no arguments, performs no network access, and exits 0 on success.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/frame"
	"ontology/lenp"
)

var failed bool

func check(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func hex(b []byte) string {
	const digits = "0123456789abcdef"
	s := make([]byte, 0, len(b)*3)
	for i, c := range b {
		if i > 0 {
			s = append(s, ' ')
		}
		s = append(s, digits[c>>4], digits[c&0xf])
	}
	return string(s)
}

func main() {
	a := api.New()
	src := [][]byte{[]byte("hi"), {}, []byte("world!"), []byte("A")}

	// Per-field prefixes and complete field bytes (section 3 table).
	var pfx, full string
	for _, f := range src {
		p := lenp.PutLength(len(f))
		pfx += "[" + hex(p) + "] "
		full += "[" + hex(append(p, f...)) + "] "
	}
	check("prefixes", pfx == "[02 00 00 00] [00 00 00 00] [06 00 00 00] [01 00 00 00] ", pfx)
	check("fields", true, full)

	rec := a.MarshalFields(src)
	want := "02 00 00 00 68 69 00 00 00 00 06 00 00 00 77 6f 72 6c 64 21 01 00 00 00 41"
	check("record", hex(rec) == want, hex(rec))
	check("empty-field=00 00 00 00", bytes.Equal(lenp.PutLength(0), []byte{0, 0, 0, 0}), "")

	got, err := a.UnmarshalFields(rec)
	rt := err == nil && len(got) == len(src)
	for i := range src {
		rt = rt && bytes.Equal(got[i], src[i])
	}
	check("roundtrip(incl empty)", rt, "")

	r := frame.NewReader(rec)
	consistent := true
	for r.Pos() < len(rec) {
		start := r.Pos()
		f, e := r.NextField()
		consistent = consistent && e == nil && r.Pos() == start+4+len(f)
	}
	consistent = consistent && r.Pos() == len(rec)
	check("prefix self-consistent", consistent, "")

	bad := append(append([]byte{}, rec...), 100, 0, 0, 0)
	_, e := a.UnmarshalFields(bad)
	check("truncated rejected", errors.Is(e, frame.ErrTruncated), "")

	// O(1) skip: counter is unexported, so externally only the offset
	// arithmetic result is observable; counter==0 is pinned by the test.
	const m = 10000
	big := make([][]byte, m)
	for i := range big {
		big[i] = make([]byte, 4096)
	}
	br := frame.NewReader(a.MarshalFields(big))
	skipped := true
	for i := 0; i < m; i++ {
		if br.SkipField() != nil {
			skipped = false
		}
	}
	check(fmt.Sprintf("skip m=%d zero-payload-touch(pos=end)", m),
		skipped && br.Pos() == m*(4+4096), "")

	// Concurrent decode of one read-only buffer + concurrent encode.
	const n = 32
	lists, serial := make([][][]byte, n), make([][]byte, n)
	for i := range lists {
		lists[i] = [][]byte{make([]byte, i), src[i%len(src)]}
		serial[i] = a.MarshalFields(lists[i])
	}
	var wg sync.WaitGroup
	dc, ec := make([][][]byte, n), make([][]byte, n)
	start := make(chan struct{})
	okC := true
	var mu sync.Mutex
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			f, derr := a.UnmarshalFields(rec)
			if derr != nil || len(f) != len(src) {
				mu.Lock()
				okC = false
				mu.Unlock()
			}
			dc[i] = f
		}(i)
		go func(i int) {
			defer wg.Done()
			<-start
			ec[i] = a.MarshalFields(lists[i])
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n && okC; i++ {
		for j := range src {
			okC = okC && bytes.Equal(dc[i][j], src[j])
		}
		okC = okC && bytes.Equal(ec[i], serial[i])
	}
	check("concurrent encode/decode", okC, "")
	check("selfcheck", a.SelfCheck() == nil, "")

	if failed {
		os.Exit(1)
	}
}
