package stream

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

type dcase struct {
	in   string
	mime bool
	want string
	kind error
	off  int64
}

func runParts(mime bool, max int, parts ...string) ([]byte, error) {
	d := NewDecoder(mime, max)
	for _, p := range parts {
		if _, err := d.Write([]byte(p)); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}
func kindOff(err error) (error, int64) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, e.Off
	}
	return err, -1
}
func encode(mime bool, x []byte) string {
	e := NewEncoder(mime)
	e.Write(x[:len(x)/2])
	e.Write(x[len(x)/2:])
	e.Close()
	return string(e.Output())
}
func stdEnc(mime bool, x []byte) string {
	s := base64.StdEncoding.EncodeToString(x)
	if !mime {
		return s
	}
	var p []string
	for i := 0; i < len(s); i += 76 {
		p = append(p, s[i:min(i+76, len(s))])
	}
	return strings.Join(p, "\r\n")
}
func same(out []byte, err error, refOut []byte, refErr error) bool {
	ka, oa := kindOff(err)
	kb, ob := kindOff(refErr)
	return bytes.Equal(out, refOut) && ka == kb && oa == ob
}
func TestStrict(t *testing.T) {
	cases := []dcase{
		{"", false, "", nil, -1}, {"QQ==", false, "A", nil, -1}, {"QUI=", false, "AB", nil, -1},
		{"TWFu", false, "Man", nil, -1}, {"TWE=", false, "Ma", nil, -1}, {"TQ==", false, "M", nil, -1},
		{"QR==", false, "", ErrNonCanonical, 1}, {"QUJ=", false, "", ErrNonCanonical, 2},
		{"QQ", false, "", ErrLength, 2}, {"QQ=", false, "", ErrLength, 3},
		{"QQ===", false, "", ErrPadding, 4}, {"QQ==QQ==", false, "", ErrPadding, 4},
		{"Q!JD", false, "", ErrInvalidChar, 1}, {"QQ=Q", false, "", ErrPadding, 3},
		{"QU\nJD", true, "", ErrNewline, 2}, {"QUJD\r", true, "", ErrNewline, 4},
		{"QUJD\rX", true, "", ErrNewline, 4}, {"QUJD\r\r\n", true, "", ErrNewline, 4},
		{"QUJD\nREVG", false, "", ErrNewline, 4}, {"QQ== ", true, "", ErrInvalidChar, 4},
		{"QQ==\t", true, "", ErrInvalidChar, 4}, {"QUJD\r\nREVG", true, "ABCDEF", nil, -1},
		{"QUJD\nREVG", true, "ABCDEF", nil, -1}, {"\nQUJD", true, "ABC", nil, -1},
		{"QQ==\r\n", true, "A", nil, -1},
	}
	for _, c := range cases {
		out, err := runParts(c.mime, 0, c.in)
		k, off := kindOff(err)
		if k != c.kind || k == nil && string(out) != c.want || k != nil && off != c.off {
			t.Errorf("decode %q: got (%q, %v, %d)", c.in, out, k, off)
		}
	}
}
func TestSplitPoints(t *testing.T) {
	inputs := []dcase{
		{"QUJDRA==", false, "", nil, 0}, {"QUJD\r\nREVG\n", true, "", nil, 0},
		{"QR==", false, "", nil, 0}, {"QQ==QQ==", false, "", nil, 0}, {"QUJD\rX", true, "", nil, 0},
	}
	for _, in := range inputs {
		refOut, refErr := runParts(in.mime, 0, in.in)
		for k := 0; k <= len(in.in); k++ {
			if out, err := runParts(in.mime, 0, in.in[:k], in.in[k:]); !same(out, err, refOut, refErr) {
				t.Errorf("%q @%d: got (%q, %v)", in.in, k, out, err)
			}
		}
		parts := make([]string, len(in.in))
		for i := range parts {
			parts[i] = in.in[i : i+1]
		}
		if out, err := runParts(in.mime, 0, parts...); !same(out, err, refOut, refErr) {
			t.Errorf("%q bytewise: got (%q, %v)", in.in, out, err)
		}
	}
}
func TestRoundTrip(t *testing.T) {
	for n := 0; n <= 300; n++ {
		x := make([]byte, n)
		for i := range x {
			x[i] = byte(i*37 + n)
		}
		for _, mime := range []bool{false, true} {
			y := encode(mime, x)
			dec, err := runParts(mime, 0, y)
			if y != stdEnc(mime, x) || err != nil || !bytes.Equal(dec, x) || encode(mime, dec) != y {
				t.Fatalf("n=%d mime=%v", n, mime)
			}
		}
	}
}
func TestLimit(t *testing.T) {
	d := NewDecoder(false, 4)
	_, err := d.Write([]byte("QUJDREVG"))
	k, off := kindOff(err)
	if k != ErrLimit || off != 4 || string(d.Output()) != "ABC" {
		t.Fatalf("got (%v, %d, %q)", k, off, d.Output())
	}
	if _, err = d.Write([]byte("QUJD")); err == nil || d.Close() == nil {
		t.Fatal("not terminal after limit")
	}
	if out, err := runParts(false, 3, "QUJD"); err != nil || string(out) != "ABC" {
		t.Fatalf("exact limit: got (%q, %v)", out, err)
	}
}
func TestCounter(t *testing.T) {
	n := 1 << 20
	d := NewDecoder(false, 0)
	for i := 0; i < n; i++ {
		if _, err := d.Write([]byte("A")); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.checked != int64(n) || len(d.Output()) != n*3/4 {
		t.Fatalf("checked=%d out=%d", d.checked, len(d.Output()))
	}
}
