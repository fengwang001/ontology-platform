// Demo exercises the transcoder semantics and prints OK/FAIL per item.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"

	"ontology/par"
	"ontology/stream"
)

var fails int

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		fails++
	}
	fmt.Printf("%-22s %s\n", name, mark)
}

func run(cfg stream.Config, chunks ...[]byte) ([]byte, error) {
	tr := stream.New(cfg)
	for _, c := range chunks {
		if _, err := tr.Write(c); err != nil {
			return tr.Output(), err
		}
	}
	err := tr.Close()
	return tr.Output(), err
}

func main() {
	F := []byte{0xEF, 0xBF, 0xBD}
	samples := []struct{ in, want []byte }{
		{[]byte{0xF0, 0x90, 0x80, 0x41}, append(bytes.Clone(F), 0x41)},
		{[]byte{0xE0, 0x80, 0x80}, bytes.Repeat(F, 3)},
		{[]byte{0xED, 0xA0, 0x80}, bytes.Repeat(F, 3)},
		{[]byte{0xC0, 0xAF}, bytes.Repeat(F, 2)},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, bytes.Repeat(F, 4)},
		{[]byte{0xE2, 0x82}, F},
		{[]byte{0x80, 0x80}, bytes.Repeat(F, 2)},
	}
	ok := true
	for _, s := range samples {
		got, err := run(stream.Config{}, s.in)
		ok = ok && err == nil && bytes.Equal(got, s.want)
	}
	check("replace-samples", ok)

	tr := stream.New(stream.Config{Strict: true})
	_, err := tr.Write([]byte{0x61, 0xF0, 0x90, 0x80, 0x41})
	var le *stream.Error
	ok = errors.As(err, &le) && le.Off == 1 && le.Len == 3 && errors.Is(err, stream.ErrInvalid)
	_, err2 := tr.Write([]byte{0x62})
	check("strict-offset-len", ok && errors.Is(err2, stream.ErrInvalid))

	in := append([]byte("aé€😀"), 0xF0, 0x90, 0x80, 0x41, 0xE2, 0x82)
	want, _ := run(stream.Config{}, in)
	ok = true
	for cut := 0; cut <= len(in); cut++ {
		got, _ := run(stream.Config{}, in[:cut], in[cut:])
		ok = ok && bytes.Equal(got, want)
	}
	check("split-consistency", ok)

	tr = stream.New(stream.Config{Strict: true})
	tr.Write([]byte{0xE2, 0x82})
	errT := tr.Close()
	tr = stream.New(stream.Config{Strict: true})
	_, errI := tr.Write([]byte{0xC0})
	check("trunc-vs-invalid", errors.Is(errT, stream.ErrTruncated) && !errors.Is(errT, stream.ErrInvalid) &&
		errors.Is(errI, stream.ErrInvalid) && !errors.Is(errI, stream.ErrTruncated))

	g1, _ := run(stream.Config{InUTF16: true}, []byte{0xD8, 0x00})
	g2, _ := run(stream.Config{InUTF16: true}, []byte{0xDC, 0x00})
	g3, _ := run(stream.Config{InUTF16: true}, []byte{0xD8, 0x00, 0x00, 0x41})
	check("utf16-lone-surrogate", bytes.Equal(g1, F) && bytes.Equal(g2, F) &&
		bytes.Equal(g3, append(bytes.Clone(F), 0x41)))

	g, _ := run(stream.Config{}, []byte{0x61, 0xEF, 0xBB, 0xBF, 0x62})
	check("midstream-bom-kept", bytes.Equal(g, []byte{0x61, 0xEF, 0xBB, 0xBF, 0x62}))

	src := []byte("aé€😀中文")
	u16out, _ := run(stream.Config{OutUTF16: true}, src)
	back, _ := run(stream.Config{InUTF16: true}, u16out)
	raw := make([]byte, 256)
	rand.New(rand.NewSource(1)).Read(raw)
	o1, _ := run(stream.Config{}, raw)
	o2, _ := run(stream.Config{}, o1)
	check("roundtrip-idempotent", bytes.Equal(back, src) && bytes.Equal(o1, o2))

	tr = stream.New(stream.Config{})
	tr.Write(raw)
	tr.Close()
	s := tr.Stats()
	check("byte-conservation", s.GoodBytes+s.BadBytes+s.BOMBytes == s.Consumed)

	full, _ := run(stream.Config{}, src)
	var got []byte
	for rest := src; len(rest) > 0; {
		t2 := stream.New(stream.Config{MaxOut: 5})
		n, err := t2.Write(rest)
		if err == nil {
			err = t2.Close()
		}
		got = append(got, t2.Output()...)
		if err == nil {
			break
		}
		if !errors.Is(err, stream.ErrLimit) {
			break
		}
		rest = rest[n:]
	}
	check("limit-resume", bytes.Equal(got, full))

	ok = true
	for k := 1; k <= 8; k++ {
		po, _, err := par.Transcode(raw, k, stream.Config{})
		ok = ok && err == nil && bytes.Equal(po, o1)
	}
	check("par-all-k", ok)

	ok = true
	for _, size := range []int{1 << 20, 1 << 24} {
		big := make([]byte, size)
		rand.New(rand.NewSource(2)).Read(big)
		t2 := stream.New(stream.Config{})
		for _, b := range big {
			t2.Write([]byte{b})
		}
		t2.Close()
		ok = ok && t2.Checks() <= 2*int64(size)
	}
	check("check-counts", ok)

	fmt.Printf("total: %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
