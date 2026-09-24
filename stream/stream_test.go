package stream

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"ontology/b64"
	"strings"
	"testing"
)

func decodeAll(mime bool, chunks ...string) ([]byte, error) {
	d := NewDecoder(mime, -1)
	for _, c := range chunks {
		if _, err := d.Write([]byte(c)); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}

func encode(mime bool, x []byte) string {
	e := NewEncoder(mime)
	e.Write(x)
	e.Close()
	return string(e.Output())
}

func TestStrictSamples(t *testing.T) {
	ins := []string{"QQ==", "QR==", "QUI=", "QUJ=", "QQ", "QQ=", "QQ===", "QQ==QQ==", ""}
	outs := []string{"A", "", "AB", "", "", "", "", "", ""}
	oks := []bool{true, false, true, false, false, false, false, false, true}
	for i := range ins {
		out, err := decodeAll(false, ins[i])
		if (err == nil) != oks[i] || oks[i] && string(out) != outs[i] {
			t.Errorf("decode(%q) = %q, %v", ins[i], out, err)
		}
	}
}

func TestNewlineRules(t *testing.T) {
	good := []string{"QUJD\r\nREU=", "QUJD\nREU=", "QUJD\n"}
	bad := []string{"QU\nJD", "QU\r\nJD", "QUJD\r", "QU JD", "QU\tJD"}
	for _, s := range good {
		if _, err := decodeAll(true, s); err != nil {
			t.Errorf("mime decode %q: %v", s, err)
		}
	}
	for _, s := range bad {
		if _, err := decodeAll(true, s); err == nil {
			t.Errorf("mime decode %q: want error", s)
		}
	}
	for _, s := range []string{"QUJD\nREU=", "QUJD\r\nREU="} {
		if _, err := decodeAll(false, s); err == nil {
			t.Errorf("non-mime decode %q: want error", s)
		}
	}
}

func TestErrorKinds(t *testing.T) {
	ins := []string{"Q!I=", "QR==", "QUJ=", "QQ==QQ==", "QQ===", "QQ", "QU\nJD", "QUJD\rX"}
	mimes := []bool{false, false, false, false, false, false, true, true}
	kinds := []error{b64.ErrInvalidChar, b64.ErrNonCanonical, b64.ErrNonCanonical,
		b64.ErrBadPadding, b64.ErrBadPadding, ErrLength, ErrNewline, ErrNewline}
	offs := []int64{1, 1, 2, 4, 4, 2, 2, 4}
	seen := map[error]bool{}
	for i := range ins {
		_, err := decodeAll(mimes[i], ins[i])
		var se *Error
		if !errors.As(err, &se) || se.Kind != kinds[i] || se.Off != offs[i] {
			t.Errorf("decode(%q) = %v, want %v@%d", ins[i], err, kinds[i], offs[i])
		}
		seen[kinds[i]] = true
	}
	if len(seen) != 5 {
		t.Errorf("got %d distinct kinds, want 5", len(seen))
	}
}

func TestSplitInvariance(t *testing.T) {
	key := func(err error) string {
		var se *Error
		if errors.As(err, &se) {
			return fmt.Sprintf("%v@%d", se.Kind, se.Off)
		}
		return "ok"
	}
	for _, in := range []string{"", "QQ==", "QR==", "QUJD\r\nREU=", "QQ==QQ==", "QQ", "QUI="} {
		wantOut, wantErr := decodeAll(true, in)
		for i := 0; i <= len(in); i++ {
			out, err := decodeAll(true, in[:i], in[i:])
			if string(out) != string(wantOut) || key(err) != key(wantErr) {
				t.Fatalf("%q@%d: (%q,%v) != (%q,%v)", in, i, out, err, wantOut, wantErr)
			}
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for n := 0; n <= 65; n++ {
		x := bytes.Repeat([]byte{'x'}, n)
		if y := encode(false, x); y != base64.StdEncoding.EncodeToString(x) {
			t.Fatalf("n=%d: encode = %q, want stdlib", n, y)
		}
		for _, mime := range []bool{false, true} {
			y := encode(mime, x)
			out, err := decodeAll(mime, y)
			if err != nil || string(out) != string(x) || encode(mime, out) != y {
				t.Fatalf("n=%d mime=%v: roundtrip broke", n, mime)
			}
		}
	}
}

func TestMIMELayout(t *testing.T) {
	if y := encode(true, make([]byte, 60)); len(y) != 82 || y[76] != '\r' || y[77] != '\n' || strings.Contains(y[78:], "\r") {
		t.Fatalf("bad MIME layout: %q", y)
	}
}

func TestOutputLimit(t *testing.T) {
	d := NewDecoder(false, 3)
	_, err := d.Write([]byte("QUJDRA=="))
	var se *Error
	if !errors.As(err, &se) || se.Kind != ErrTooLong || se.Off != 4 {
		t.Fatalf("got %v, want ErrTooLong@4", err)
	}
	if _, err := d.Write([]byte("QQ==")); err == nil || string(d.Output()) != "ABC" {
		t.Fatalf("after limit: err=%v out=%q", err, d.Output())
	}
}

func TestCheckedCounter(t *testing.T) {
	in := strings.Repeat("A", 1<<20)
	d := NewDecoder(false, -1)
	for i := 0; i < len(in); i++ {
		if _, err := d.Write([]byte{in[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil || d.Checked() != int64(len(in)) {
		t.Fatalf("close=%v checked=%d want %d", err, d.Checked(), len(in))
	}
	if !bytes.Equal(d.Output(), make([]byte, 3*(1<<20)/4)) {
		t.Fatal("output mismatch")
	}
}
