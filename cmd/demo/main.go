package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"ontology/stream"
)

type check struct {
	name string
	ok   bool
}

func decodeAll(mime bool, limit int, chunks ...[]byte) ([]byte, error) {
	d := stream.NewDecoder(mime, limit)
	for _, c := range chunks {
		if _, err := d.Write(c); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}

func encodeAll(mime bool, p []byte) []byte {
	e := stream.NewEncoder(mime)
	_, _ = e.Write(p)
	_ = e.Close()
	return e.Output()
}

func canonicalOK() bool {
	cases := []struct {
		in     string
		want   string
		reject bool
	}{
		{"QQ==", "A", false},
		{"QR==", "", true},
		{"QUI=", "AB", false},
		{"QUJ=", "", true},
		{"QQ", "", true},
		{"QQ=", "", true},
		{"QQ===", "", true},
		{"QQ==QQ==", "", true},
		{"", "", false},
	}
	for _, tc := range cases {
		out, err := decodeAll(false, -1, []byte(tc.in))
		if tc.reject != (err != nil) || (!tc.reject && string(out) != tc.want) {
			return false
		}
	}
	return true
}

func newlineOK() bool {
	if _, err := decodeAll(true, -1, []byte("QQ==\r\nQQ==")); err == nil {
		return false
	}
	if out, err := decodeAll(true, -1, []byte("QUFB\r\nQQ==")); err != nil || string(out) != "AAAA" {
		return false
	}
	if out, err := decodeAll(true, -1, []byte("QUFB\nQQ==")); err != nil || string(out) != "AAAA" {
		return false
	}
	for _, bad := range []string{"Q\rQ==", "QQ= \n", "QUFB\rQQ==", "QU\t=="} {
		if _, err := decodeAll(false, -1, []byte(bad)); err == nil {
			return false
		}
	}
	if _, err := decodeAll(false, -1, []byte("QUFB\r\nQQ==")); err == nil {
		return false
	}
	return true
}

func errorsOK() bool {
	cases := []struct {
		in     string
		cause  error
		offset int
	}{
		{"Q*==", stream.ErrIllegalChar, 1},
		{"QR==", stream.ErrCanonical, 1},
		{"QQ==QQ==", stream.ErrPadding, 4},
		{"QUI", stream.ErrLength, 0},
		{"Q\rQ==", stream.ErrNewline, 1},
	}
	for _, tc := range cases {
		_, err := decodeAll(true, -1, []byte(tc.in))
		var se *stream.Error
		if !errors.As(err, &se) || !errors.Is(err, tc.cause) || se.Offset != tc.offset {
			return false
		}
	}
	return true
}

func runSplit(in []byte, mime bool, limit int) (string, error) {
	d := stream.NewDecoder(mime, limit)
	for _, b := range in {
		if _, err := d.Write([]byte{b}); err != nil {
			return string(d.Output()), err
		}
	}
	return string(d.Output()), d.Close()
}

func splitsOK() bool {
	inputs := []string{"QUFBQQ==", "QUFB\r\nQQ==", "QR==", "QQ==Q", "QUFB\rQ", "QUI"}
	for _, in := range inputs {
		wantOut, wantErr := runSplit([]byte(in), true, -1)
		for i := 0; i <= len(in); i++ {
			d := stream.NewDecoder(true, -1)
			var err error
			if i > 0 {
				_, err = d.Write([]byte(in[:i]))
			}
			if err == nil && i < len(in) {
				_, err = d.Write([]byte(in[i:]))
			}
			if err == nil {
				err = d.Close()
			}
			if string(d.Output()) != wantOut || (err == nil) != (wantErr == nil) {
				return false
			}
			var got, want *stream.Error
			if errors.As(err, &got) && errors.As(wantErr, &want) {
				if got.Offset != want.Offset || !errors.Is(got, want.Cause) {
					return false
				}
			}
		}
	}
	return true
}

func roundtripOK() bool {
	samples := [][]byte{{}, {0}, {0, 1}, {0, 1, 2}, bytes.Repeat([]byte("A"), 57)}
	for _, x := range samples {
		for _, mime := range []bool{false, true} {
			y := encodeAll(mime, x)
			out, err := decodeAll(mime, -1, y)
			if err != nil || !bytes.Equal(out, x) {
				return false
			}
		}
	}
	y := encodeAll(false, []byte{0, 1, 2})
	out, _ := decodeAll(false, -1, y)
	return bytes.Equal(encodeAll(false, out), y)
}

func limitOK() bool {
	d := stream.NewDecoder(false, 3)
	if _, err := d.Write([]byte("QUFBQQ==")); !errors.Is(err, stream.ErrLimit) {
		return false
	}
	if string(d.Output()) != "AAA" {
		return false
	}
	if _, err := d.Write([]byte("QQ==")); err == nil {
		return false
	}
	return true
}

func counterOK() bool {
	in := []byte(strings.Repeat("QUFB", 256))
	d := stream.NewDecoder(false, -1)
	for _, b := range in {
		if _, err := d.Write([]byte{b}); err != nil {
			return false
		}
	}
	return d.Checked() == len(in)
}

func main() {
	checks := []check{
		{"canonical samples", canonicalOK()},
		{"newline positions", newlineOK()},
		{"five error kinds with offsets", errorsOK()},
		{"all split points identical", splitsOK()},
		{"roundtrip (plain & MIME)", roundtripOK()},
		{"output limit stops at group edge", limitOK()},
		{"checked counter == input bytes", counterOK()},
	}

	pass := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%-5s %s\n", status, c.name)
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo failed")
	}
}
