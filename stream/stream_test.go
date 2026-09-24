package stream

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type result struct {
	out []byte
	err error
	off int64
}

func runSplit(mime bool, limit int, in string, split int) result {
	d := NewDecoder(mime, limit)
	var writeErr error
	for start := 0; start < len(in); start += split {
		end := min(start+split, len(in))
		if _, err := d.Write([]byte(in[start:end])); err != nil {
			writeErr = err
			break
		}
	}
	err := writeErr
	if err == nil {
		err = d.Close()
	}
	var offset int64
	var se *Error
	if errors.As(err, &se) {
		offset = se.Offset
	}
	return result{d.Output(), err, offset}
}

func decodeAll(mime bool, in string) result {
	return runSplit(mime, -1, in, len(in)+1)
}

func TestCanonicalPadding(t *testing.T) {
	cases := []struct {
		in  string
		out string
		err error
	}{
		{"", "", nil},
		{"QQ==", "A", nil},
		{"QR==", "", ErrNonCanonicalTail},
		{"QUI=", "AB", nil},
		{"QUJ=", "", ErrNonCanonicalTail},
		{"QQ", "", ErrInvalidLength},
		{"QQ=", "", ErrInvalidLength},
		{"QQ===", "A", ErrPaddingPosition},
		{"QQ==QQ==", "A", ErrPaddingPosition},
	}
	for _, tc := range cases {
		got := decodeAll(false, tc.in)
		if !errors.Is(got.err, tc.err) || string(got.out) != tc.out {
			t.Fatalf("%q: got (%q,%v), want (%q,%v)", tc.in, got.out, got.err, tc.out, tc.err)
		}
	}
}

func TestNewlinePositions(t *testing.T) {
	cases := []struct {
		in   string
		mime bool
		err  error
	}{
		{"QQ\r\n==", true, ErrInvalidNewline},
		{"QQ==\r\n", true, ErrInvalidNewline},
		{"QUJD\nQUI=", true, nil},
		{"QUJD\r\n\r\nQUI=", true, nil},
		{"QQ\r\n==", false, ErrInvalidNewline},
		{"QQ\r==", true, ErrInvalidNewline},
		{"Q Q==", true, ErrInvalidCharacter},
		{"QQ\t==", true, ErrInvalidCharacter},
	}
	for _, tc := range cases {
		if err := decodeAll(tc.mime, tc.in).err; !errors.Is(err, tc.err) {
			t.Fatalf("%q mime=%v: got %v, want %v", tc.in, tc.mime, err, tc.err)
		}
	}
}

func TestErrorClassesAndOffsets(t *testing.T) {
	cases := []struct {
		in  string
		err error
		off int64
	}{
		{"QQ*=", ErrInvalidCharacter, 2},
		{"QR==", ErrNonCanonicalTail, 1},
		{"QQ===", ErrPaddingPosition, 4},
		{"QQ=", ErrInvalidLength, 3},
		{"QQ\n==", ErrInvalidNewline, 2},
	}
	for _, tc := range cases {
		got := decodeAll(false, tc.in)
		if !errors.Is(got.err, tc.err) || got.off != tc.off {
			t.Fatalf("%q: got (%v,%d), want (%v,%d)", tc.in, got.err, got.off, tc.err, tc.off)
		}
	}
}

func TestAllSplitPoints(t *testing.T) {
	inputs := []string{"", "QQ==", "QUI=", "QUJD", "QUJD\r\n\r\nQUI=", "QQ= ", "QR==", "QQ==QQ==", "QQ\rX==", "QQ\r"}
	for _, in := range inputs {
		mime := strings.ContainsAny(in, "\r\n")
		want := runSplit(mime, -1, in, len(in)+1)
		for split := 1; split <= len(in)+1; split++ {
			got := runSplit(mime, -1, in, split)
			if !bytes.Equal(got.out, want.out) || !errors.Is(got.err, want.err) || got.off != want.off {
				t.Fatalf("%q split=%d: got (%q,%v,%d), want (%q,%v,%d)", in, split, got.out, got.err, got.off, want.out, want.err, want.off)
			}
		}
	}
}

func encodeAll(t *testing.T, mime bool, in []byte) []byte {
	e := NewEncoder(mime)
	_, err := e.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return e.Output()
}

func TestRoundTrips(t *testing.T) {
	cases := [][]byte{{}, {'A'}, {'A', 'B'}, {'A', 'B', 'C'}, bytes.Repeat([]byte("xy"), 40)}
	for _, mime := range []bool{false, true} {
		for _, in := range cases {
			encoded := encodeAll(t, mime, in)
			if mime && bytes.Contains(encoded, []byte("=\r\n")) {
				t.Fatal("newline after final line")
			}
			got := runSplit(mime, -1, string(encoded), 1)
			if got.err != nil || !bytes.Equal(got.out, in) {
				t.Fatalf("round trip %q: got %q,%v", in, got.out, got.err)
			}
		}
	}
}

func TestOutputLimit(t *testing.T) {
	got := decodeAll(false, "QUJDQUI=")
	if got.err != nil || string(got.out) != "ABCAB" {
		t.Fatalf("unlimited: %q %v", got.out, got.err)
	}
	got = decodeAll(false, "QUJDQUI=")
	limited := runSplit(false, 3, "QUJDQUI=", 1)
	if !errors.Is(limited.err, ErrOutputLimit) || string(limited.out) != "ABC" {
		t.Fatalf("limit: %q %v", limited.out, limited.err)
	}
}

func TestCheckedCountNoRescan(t *testing.T) {
	in := bytes.Repeat([]byte("QUJD"), 262144)
	got := runSplit(false, -1, string(in), 1)
	if got.err != nil {
		t.Fatal(got.err)
	}
	d := NewDecoder(false, -1)
	if _, err := d.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.checked != int64(len(in)) {
		t.Fatalf("checked=%d, want %d", d.checked, len(in))
	}
}
