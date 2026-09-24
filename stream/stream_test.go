package stream_test

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"unicode/utf8"

	"ontology/stream"
	"ontology/u16"
)

func runes(t *testing.T, b []byte) []rune {
	t.Helper()
	var got []rune
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if n == 0 || r == utf8.RuneError && n == 0 {
			t.Fatalf("invalid utf8 output: % x", b)
		}
		got = append(got, r)
		b = b[n:]
	}
	return got
}

func transcode(t *testing.T, in []byte, cfg stream.Config, chunks int) ([]byte, stream.Stats, error) {
	t.Helper()
	tr := stream.New(cfg)
	for pos := 0; pos < len(in); pos += chunks {
		end := pos + chunks
		if end > len(in) {
			end = len(in)
		}
		n, err := tr.Write(in[pos:end])
		pos += n - chunks
		if err != nil && !errors.Is(err, stream.ErrOutputLimit) {
			return tr.Output(), tr.Stats(), err
		}
	}
	err = tr.Close()
	return tr.Output(), tr.Stats(), err
}

func TestReplacementSamples(t *testing.T) {
	cases := []struct {
		in   []byte
		want []rune
	}{
		{[]byte{0xF0, 0x90, 0x80, 0x41}, []rune{0xFFFD, 0x41}},
		{[]byte{0xE0, 0x80, 0x80}, []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{[]byte{0xED, 0xA0, 0x80}, []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{[]byte{0xC0, 0xAF}, []rune{0xFFFD, 0xFFFD}},
		{[]byte{0xF4, 0x90, 0x80, 0x80}, []rune{0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD}},
		{[]byte{0xE2, 0x82}, []rune{0xFFFD}},
		{[]byte{0x80, 0x80}, []rune{0xFFFD, 0xFFFD}},
	}
	for _, c := range cases {
		out, _, err := transcode(t, c.in, stream.Config{}, 1)
		got := runes(t, out)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%x: %q %v want %q", c.in, runes(t, out), err, c.want)
		}
	}
}

func TestStrictAndSplits(t *testing.T) {
	cases := []struct {
		in  []byte
		bad bool
	}{{[]byte{0xC3, 0xA9}, false}, {[]byte{0xE2, 0x82, 0xAC}, false},
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, false}, {[]byte{0xF0, 0x90, 0x80, 0x41}, true},
		{[]byte{0xE2, 0x82}, true}, {[]byte{0x80, 0x41}, true}, {[]byte{0xC0, 0xAF}, true}}
	for _, in := range cases {
		base, _, _ := transcode(t, in.in, stream.Config{}, 99)
		for cut := 1; cut <= len(in); cut++ {
			out, _, err := transcode(t, in.in, stream.Config{}, cut)
			if err != nil || !bytes.Equal(out, base) {
			t.Fatalf("%x cut %d: %x %v want %x", in.in, cut, out, err, base)
			}
		}
		strict := stream.New(stream.Config{Strict: true})
		_, werr := strict.Write(in.in)
		cerr := strict.Close()
		if in.bad && werr == nil && cerr == nil {
			t.Fatalf("%x unexpectedly valid", in.in)
		}
		var ue *stream.UnitError
		if in.bad && (werr != nil || cerr != nil) && !errors.As(werr, &ue) && !errors.As(cerr, &ue) {
			t.Fatalf("missing offset error: %v %v", werr, cerr)
		}
		if in.bad {
			if _, err := strict.Write([]byte{'A'}); !errors.Is(err, ue.Err) || err == nil {
				t.Fatalf("terminal error not retained: %v", err)
			}
		}
	}
}

func TestTruncationIsDistinct(t *testing.T) {
	cases := []struct {
		in    []byte
		trunc bool
	}{{[]byte{0xC3}, true}, {[]byte{0xE0, 0x82}, true}, {[]byte{0xF0, 0x9F, 0x98}, true},
		{[]byte{0x41}, false}, {[]byte{0xC3, 0xA9}, false}}
	for _, in := range cases {
		tr := stream.New(stream.Config{Strict: true})
		_, werr := tr.Write(in.in)
		err := tr.Close()
		if werr != nil || in.trunc != errors.Is(err, stream.ErrTruncated) {
			t.Fatalf("%x: %v %v", in.in, werr, err)
		}
		if errors.Is(err, stream.ErrInvalidByte) {
			t.Fatalf("truncation confused with invalid: %v", err)
		}
	}
}

func TestUTF16AndBOM(t *testing.T) {
	valid := []byte("A€😀")
	tr := stream.New(stream.Config{ToUTF16: true, Order: u16.Little, EmitBOM: false})
	_, _ = tr.Write(valid)
	_ = tr.Close()
	mid := tr.Output()
	back, _, err := transcode(t, mid, stream.Config{FromUTF16: true, Order: u16.Little}, 3)
	if err != nil || !bytes.Equal(back, valid) {
		t.Fatalf("round trip %x: %v", back, err)
	}
	iso := []byte{0x41, 0xD8, 0x41, 0x00, 0x00, 0xDC, 0x42, 0xD8, 0x42, 0x00}
	out, _, err := transcode(t, iso, stream.Config{FromUTF16: true, Order: u16.Little}, 1)
	if err != nil || string(runes(t, out)) != "\ufffdA\ufffd\ufffdB" {
		t.Fatalf("surrogates: %q %v", runes(t, out), err)
	}
	bom := append([]byte{0xEF, 0xBB, 0xBF, 0x41, 0xEF, 0xBB, 0xBF}, 0x42)
	out, _, err = transcode(t, bom, stream.Config{EmitBOM: false}, 2)
	if err != nil || string(runes(t, out)) != "A\ufeffB" {
		t.Fatalf("bom: %q %v", runes(t, out), err)
	}
}

var _ io.Writer = (*stream.Transcoder)(nil)
