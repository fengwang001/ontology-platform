package stream

import (
	"bytes"
	"encoding/base64"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func feed(mime bool, limit int, chunks ...[]byte) ([]byte, error) {
	d := NewDecoder(mime, limit)
	for _, c := range chunks {
		if _, err := d.Write(c); err != nil {
			return d.Output(), err
		}
	}
	return d.Output(), d.Close()
}

func TestCanonicalSamples(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   string
		reject bool
	}{
		{"single", "QQ==", "A", false},
		{"noncanonical-1", "QR==", "", true},
		{"pair", "QUI=", "AB", false},
		{"noncanonical-2", "QUJ=", "", true},
		{"no-padding", "QQ", "", true},
		{"one-equals", "QQ=", "", true},
		{"three-equals", "QQ===", "", true},
		{"padding-midstream", "QQ==QQ==", "", true},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := feed(false, -1, []byte(tc.in))
			if (err != nil) != tc.reject {
				t.Fatalf("err=%v, want reject=%v", err, tc.reject)
			}
			if !tc.reject && string(out) != tc.want {
				t.Fatalf("got %q want %q", out, tc.want)
			}
		})
	}
}

func TestNewlinePositions(t *testing.T) {
	accepted := []struct {
		in   string
		want string
	}{
		{"QUFB\r\nQQ==", "AAAA"},
		{"QUFB\nQQ==", "AAAA"},
		{"QUFB\r\nQUFB", "AAAAAA"},
		{"\r\nQUFB", "AAA"},
	}
	for _, tc := range accepted {
		out, err := feed(true, -1, []byte(tc.in))
		if err != nil || string(out) != tc.want {
			t.Fatalf("%q: got %q err=%v want %q", tc.in, out, err, tc.want)
		}
	}
	rejected := []string{
		"Q\rQ==", "QQ\n==", "QUFB\r", "QUFB\rQQ==",
	}
	for _, in := range rejected {
		if _, err := feed(true, -1, []byte(in)); !errors.Is(err, ErrNewline) {
			t.Fatalf("%q: err=%v want ErrNewline", in, err)
		}
	}
	if _, err := feed(false, -1, []byte("QUFB\r\nQQ==")); !errors.Is(err, ErrNewline) {
		t.Fatalf("MIME newline accepted without MIME: %v", err)
	}
}

func TestErrorKindsAndOffsets(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		mime   bool
		cause  error
		offset int
	}{
		{"illegal", "Q*==", false, ErrIllegalChar, 1},
		{"illegal-space", "Q Q=", false, ErrIllegalChar, 1},
		{"illegal-tab", "QQ\t==", false, ErrIllegalChar, 2},
		{"canonical", "QR==", false, ErrCanonical, 1},
		{"canonical-pair", "QUJ=", false, ErrCanonical, 2},
		{"padding-after-end", "QQ==QQ==", false, ErrPadding, 4},
		{"length", "QUI", false, ErrLength, 0},
		{"length-one", "Q", false, ErrLength, 0},
		{"newline-inside", "QQ\r==", true, ErrNewline, 2},
		{"newline-bare-cr", "QUFB\rQ", true, ErrNewline, 4},
		{"newline-trailing-cr", "QUFB\r", true, ErrNewline, 4},
		{"newline-disabled", "QUFB\nQQ==", false, ErrNewline, 4},
		{"limit", "QUFBQQ==", false, ErrLimit, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := feed(tc.mime, 3, []byte(tc.in))
			var se *Error
			if !errors.As(err, &se) || !errors.Is(err, tc.cause) || se.Offset != tc.offset {
				t.Fatalf("got %v offset=%d, want %v offset=%d", err, offsetOf(err), tc.cause, tc.offset)
			}
		})
	}
}

func offsetOf(err error) int {
	var se *Error
	if errors.As(err, &se) {
		return se.Offset
	}
	return -1
}

func TestSplitInvariance(t *testing.T) {
	inputs := []string{
		"QUFBQQ==", "QUFB\r\nQQ==", "QR==", "QUJ=", "QQ==Q", "QUFB\rQ",
		"QUI", "QQ==\r\nQ", "QUFB\r", "", "QUFB\r\n", "QQ=\r\n=",
	}
	for _, in := range inputs {
		fullOut, fullErr := feed(true, -1, []byte(in))
		for i := 0; i <= len(in); i++ {
			d := NewDecoder(true, -1)
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
			if string(d.Output()) != string(fullOut) || (err == nil) != (fullErr == nil) {
				t.Fatalf("in=%q split=%d: out=%q/%q err=%v/%v", in, i, d.Output(), fullOut, err, fullErr)
			}
			var got, want *Error
			if errors.As(err, &got) && errors.As(fullErr, &want) {
				if got.Offset != want.Offset || !errors.Is(got, want.Cause) {
					t.Fatalf("in=%q split=%d: %+v != %+v", in, i, got, want)
				}
			}
		}
	}
}

func TestRoundTrip(t *testing.T) {
	samples := [][]byte{{}, {0}, {1}, {0, 1}, {254, 255}, {0, 1, 2}, bytes.Repeat([]byte{7}, 76)}
	rng := rand.New(rand.NewSource(1))
	for n := 0; n <= 40; n++ {
		b := make([]byte, n)
		rng.Read(b)
		samples = append(samples, b)
	}
	for _, x := range samples {
		for _, mime := range []bool{false, true} {
			enc := NewEncoder(mime)
			_, _ = enc.Write(x)
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			y := enc.Output()
			out, err := feed(mime, -1, y)
			if err != nil || !bytes.Equal(out, x) {
				t.Fatalf("mime=%v n=%d: out=%v err=%v", mime, len(x), out, err)
			}
			if !mime && string(y) != base64.StdEncoding.EncodeToString(x) {
				t.Fatalf("encoding mismatch with stdlib: %q", y)
			}
			re := NewEncoder(mime)
			_, _ = re.Write(out)
			_ = re.Close()
			if mime {
				stripped := strings.ReplaceAll(string(y), "\r\n", "")
				if !bytes.Equal(re.Output(), []byte(stripped)) {
					t.Fatalf("canonical re-encode mismatch")
				}
			} else if !bytes.Equal(re.Output(), y) {
				t.Fatalf("Encode(Decode(y)) != y")
			}
		}
	}
}

func TestMIMELineWrap(t *testing.T) {
	x := bytes.Repeat([]byte("x"), 57)
	enc := NewEncoder(true)
	_, _ = enc.Write(x)
	_ = enc.Close()
	y := string(enc.Output())
	if !strings.Contains(y, "\r\n") || strings.HasSuffix(y, "\r\n") {
		t.Fatalf("bad MIME framing: %q", y)
	}
	if y != base64.StdEncoding.EncodeToString(x)[:76]+"\r\n"+base64.StdEncoding.EncodeToString(x)[76:] {
		t.Fatalf("MIME wrap mismatch: %q", y)
	}
}

func TestOutputLimit(t *testing.T) {
	cases := []struct {
		limit   int
		out     string
		stopped bool
	}{
		{-1, "AAAAAA", false},
		{6, "AAAAAA", false},
		{5, "AAA", true},
		{3, "AAA", true},
		{2, "", true},
	}
	for _, tc := range cases {
		out, err := feed(false, tc.limit, []byte("QUFBQUFB"))
		if string(out) != tc.out || (errors.Is(err, ErrLimit)) != tc.stopped {
			t.Fatalf("limit=%d: out=%q err=%v", tc.limit, out, err)
		}
		d := NewDecoder(false, tc.limit)
		if _, err := d.Write([]byte("QUFBQUFB")); err != nil {
			if _, err2 := d.Write([]byte("QQ==")); err2 == nil {
				t.Fatal("terminal decoder accepted more input")
			}
		}
	}
}

func TestCheckedCounter(t *testing.T) {
	const n = 1 << 20
	in := bytes.Repeat([]byte("QUFB"), n/4)
	d := NewDecoder(false, -1)
	for _, b := range in {
		if _, err := d.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.Checked() != n {
		t.Fatalf("checked=%d want=%d", d.Checked(), n)
	}
}
