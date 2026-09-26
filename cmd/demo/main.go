package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/enc"
	"ontology/stream"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	vecs := []struct {
		v uint64
		b []byte
	}{
		{0, []byte{0x00}}, {1, []byte{0x01}}, {127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}}, {300, []byte{0xac, 0x02}},
		{16383, []byte{0xff, 0x7f}}, {16384, []byte{0x80, 0x80, 0x01}},
		{2097151, []byte{0xff, 0xff, 0x7f}},
	}
	ok := true
	for _, tc := range vecs {
		ok = ok && bytes.Equal(enc.EncodeUint(tc.v), tc.b)
	}
	check("eight vector encodings", ok)

	ok = true
	for _, tc := range vecs {
		v, n, err := enc.DecodeUint(tc.b)
		ok = ok && err == nil && v == tc.v && n == len(tc.b)
	}
	check("roundtrip", ok)

	_, _, err := enc.DecodeUint([]byte{0x80, 0x00})
	check("non-canonical 80 00 rejected", errors.Is(err, enc.ErrNonCanonical))

	over := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	_, _, err = enc.DecodeUint(over)
	check("overflow rejected", errors.Is(err, enc.ErrOverflow))

	_, _, err = enc.DecodeUint(nil)
	check("empty input rejected", errors.Is(err, enc.ErrEmptyInput))

	check("signed -64/64", bytes.Equal(enc.EncodeInt(-64), []byte{0x40}) &&
		bytes.Equal(enc.EncodeInt(64), []byte{0xc0, 0x00}))

	r := stream.NewReader([]byte{0x2a, 0x80, 0x00})
	first, err := r.ReadUint()
	pos := r.Pos()
	_, err2 := r.ReadUint()
	_, err3 := r.ReadUint() // retry: identical rejection, still no advance
	r.Reset([]byte{0x05})
	v, err4 := r.ReadUint()
	check("cursor unchanged after rejection", first == 42 && err == nil && pos == 1 &&
		errors.Is(err2, enc.ErrNonCanonical) && errors.Is(err3, enc.ErrNonCanonical) &&
		r.Pos() == 1 && v == 5 && err4 == nil)

	const m = 10000
	w := stream.NewWriter()
	for i := 0; i < m; i++ {
		w.WriteUint(uint64(i)*2654435761 + 127)
	}
	buf := w.Bytes()
	r.Reset(buf)
	ok = r.Len() > m // multi-byte values present
	for i := 0; i < m && ok; i++ {
		v, err := r.ReadUint()
		ok = err == nil && v == uint64(i)*2654435761+127
	}
	check("single-pass decode of m=10000", ok && r.Pos() == len(buf))

	const n = 64
	w2 := stream.NewWriter()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(v uint64) { defer wg.Done(); w2.WriteUint(v) }(uint64(1<<21 + i))
	}
	wg.Wait()
	r.Reset(w2.Bytes())
	seen := map[uint64]bool{}
	ok = true
	for i := 0; i < n && ok; i++ {
		v, err := r.ReadUint()
		ok = err == nil && v >= 1<<21 && v < 1<<21+n && !seen[v]
		seen[v] = true
	}
	check("concurrent writes atomic, all values decoded", ok && r.Pos() == r.Len())

	check("api.SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
