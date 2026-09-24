package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/stream"
)

var fails int

type sample struct {
	in, want string
	kind     error
	off      int64
}

func check(name string, ok bool) {
	if !ok {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[ok] + name)
}

func classify(err error) (error, int64) {
	var e *stream.Error
	if errors.As(err, &e) {
		return e.Kind, e.Off
	}
	return err, -1
}

func sameErr(a, b error) bool {
	ka, oa := classify(a)
	kb, ob := classify(b)
	return ka == kb && oa == ob
}

func run(d *stream.Decoder, parts ...string) ([]byte, error) {
	for _, p := range parts {
		if _, err := d.Write([]byte(p)); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}

func enc(mime bool, x []byte) string {
	e := stream.NewEncoder(mime)
	e.Write(x[:len(x)/2])
	e.Write(x[len(x)/2:])
	e.Close()
	return string(e.Output())
}

func main() {
	samples := []sample{
		{"", "", nil, -1}, {"QQ==", "A", nil, -1}, {"QUI=", "AB", nil, -1},
		{"QR==", "", stream.ErrNonCanonical, 1}, {"QUJ=", "", stream.ErrNonCanonical, 2},
		{"QQ", "", stream.ErrLength, 2}, {"QQ=", "", stream.ErrLength, 3},
		{"QQ===", "", stream.ErrPadding, 4}, {"QQ==QQ==", "", stream.ErrPadding, 4},
		{"Q!JD", "", stream.ErrInvalidChar, 1}, {"QQ=Q", "", stream.ErrPadding, 3},
		{"QU\nJD", "", stream.ErrNewline, 2},
	}
	okA, okB := true, true
	seen := map[error]bool{}
	for _, c := range samples {
		out, err := run(stream.NewDecoder(true, 0), c.in)
		k, off := classify(err)
		okA = okA && k == c.kind && (k != nil || string(out) == c.want)
		okB = okB && k == c.kind && off == c.off
		seen[k] = true
	}
	check("canonical samples", okA)
	check("error kinds+offsets", okB && len(seen) == 6)
	ok := true
	for _, s := range []string{"QUJD\r\nREVG", "QUJD\nREVG"} {
		out, err := run(stream.NewDecoder(true, 0), s)
		ok = ok && err == nil && string(out) == "ABCDEF"
	}
	for _, s := range []string{"QU\nJD", "QUJD\r", "QUJD\rX", "QUJD\r\r\n"} {
		_, err := run(stream.NewDecoder(true, 0), s)
		ok = ok && errors.Is(err, stream.ErrNewline)
	}
	_, err := run(stream.NewDecoder(false, 0), "QUJD\nREVG")
	ok = ok && errors.Is(err, stream.ErrNewline)
	for _, s := range []string{"QQ== ", "QQ==\t"} {
		_, err := run(stream.NewDecoder(true, 0), s)
		ok = ok && errors.Is(err, stream.ErrInvalidChar)
	}
	check("newline rules", ok)

	ok = true
	for _, s := range []string{"QUJDRA==", "QUJD\r\nREVG\n", "QR==", "QQ==QQ==", "QUJD\rX"} {
		mime := strings.ContainsAny(s, "\r\n")
		refOut, refErr := run(stream.NewDecoder(mime, 0), s)
		for k := 0; k <= len(s); k++ {
			out, err := run(stream.NewDecoder(mime, 0), s[:k], s[k:])
			ok = ok && bytes.Equal(out, refOut) && sameErr(err, refErr)
		}
		parts := make([]string, len(s))
		for i := range parts {
			parts[i] = s[i : i+1]
		}
		out, err := run(stream.NewDecoder(mime, 0), parts...)
		ok = ok && bytes.Equal(out, refOut) && sameErr(err, refErr)
	}
	check("split consistency", ok)

	ok = true
	for n := 0; n <= 300; n++ {
		x := make([]byte, n)
		for i := range x {
			x[i] = byte(i*37 + n)
		}
		for _, m := range []bool{false, true} {
			y := enc(m, x)
			dec, err := run(stream.NewDecoder(m, 0), y)
			ok = ok && err == nil && bytes.Equal(dec, x) && enc(m, dec) == y
		}
	}
	y := enc(true, make([]byte, 60))
	ok = ok && len(y) == 82 && y[76:78] == "\r\n" && y[len(y)-1] != '\n'
	check("roundtrip", ok)

	d := stream.NewDecoder(false, 4)
	_, err = d.Write([]byte("QUJDREVG"))
	ok = errors.Is(err, stream.ErrLimit) && string(d.Output()) == "ABC"
	_, err = d.Write([]byte("QUJD"))
	ok = ok && err != nil && d.Close() != nil
	out, err := run(stream.NewDecoder(false, 3), "QUJD")
	ok = ok && err == nil && string(out) == "ABC"
	check("output limit", ok)

	n := 1 << 20
	d = stream.NewDecoder(false, 0)
	for i := 0; i < n; i++ {
		_, err = d.Write([]byte("A"))
	}
	ok = err == nil && d.Close() == nil && d.Checked() == int64(n) && len(d.Output()) == n*3/4
	check("checked counter", ok)

	fmt.Printf("total: %d/7 checks failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
