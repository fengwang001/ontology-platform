package ontology_test

import (
	"testing"

	"ontology/scalar"
	"ontology/stream"
	"ontology/u16"
	"ontology/u8"
)

func hx(s string) []byte {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			continue
		}
		var v byte
		for j := 0; j < 2; j++ {
			c := s[i+j]
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= c - '0'
			case c >= 'A' && c <= 'F':
				v |= c - 'A' + 10
			case c >= 'a' && c <= 'f':
				v |= c - 'a' + 10
			}
		}
		b = append(b, v)
		i++
	}
	return b
}

func decodeU8(in []byte) []rune {
	var d u8.Decoder
	var out []rune
	var rpb []byte
	i := 0
	for i < len(in) || len(rpb) > 0 {
		var b byte
		if len(rpb) > 0 {
			b, rpb = rpb[0], rpb[1:]
		} else {
			b, i = in[i], i+1
		}
		r := d.Feed(b)
		switch r.Event {
		case u8.EvOK:
			out = append(out, r.Rune)
		case u8.EvBad:
			out = append(out, 0xFFFD)
			if r.Reprocess {
				rpb = append(rpb, b)
			}
		}
	}
	if d.Close() {
		out = append(out, 0xFFFD)
	}
	return out
}

func decodeU16LE(in []byte) []rune {
	d := u16.NewDecoder(u16.LE)
	var out []rune
	for _, b := range in {
		r := d.Feed(b)
		switch r.Event {
		case u16.EvOK:
			out = append(out, r.Rune)
		case u16.EvBOM:
			out = append(out, r.Rune)
		case u16.EvBad:
			out = append(out, 0xFFFD)
		}
	}
	return out
}

func runStream(in []byte, cfg stream.Config) ([]byte, stream.Stats, error) {
	tr := stream.New(cfg)
	_, err := tr.Write(in)
	if err == nil {
		err = tr.Close()
	}
	return tr.Output(), tr.Stats(), err
}

func TestScalar(t *testing.T) {
	tab := []struct {
		r           rune
		valid, surr bool
		hi, lo      bool
	}{
		{0, true, false, false, false},
		{0xD7FF, true, false, false, false},
		{0xD800, false, true, true, false},
		{0xDBFF, false, true, true, false},
		{0xDC00, false, true, false, true},
		{0xDFFF, false, true, false, true},
		{0xE000, true, false, false, false},
		{0x10FFFF, true, false, false, false},
		{0x110000, false, false, false, false},
		{-1, false, false, false, false},
	}
	for _, c := range tab {
		if scalar.Valid(c.r) != c.valid {
			t.Errorf("Valid(%X)", c.r)
		}
		if scalar.Surrogate(c.r) != c.surr || scalar.HighSurrogate(c.r) != c.hi ||
			scalar.LowSurrogate(c.r) != c.lo {
			t.Errorf("class(%X)", c.r)
		}
	}
	hi, lo := scalar.ToSurrogatePair(0x1F600)
	if scalar.FromSurrogatePair(hi, lo) != 0x1F600 {
		t.Fatal("pair roundtrip")
	}
}

func TestReplacementUnits(t *testing.T) {
	tab := []struct {
		in   string
		want []rune
	}{
		{"F0 90 80 41", []rune{0xFFFD, 0x41}},
		{"E0 80 80", []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{"ED A0 80", []rune{0xFFFD, 0xFFFD, 0xFFFD}},
		{"C0 AF", []rune{0xFFFD, 0xFFFD}},
		{"F4 90 80 80", []rune{0xFFFD, 0xFFFD, 0xFFFD, 0xFFFD}},
		{"E2 82", []rune{0xFFFD}},
		{"80 80", []rune{0xFFFD, 0xFFFD}},
	}
	for _, c := range tab {
		got := decodeU8(hx(c.in))
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %X want %X", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %X want %X", c.in, got, c.want)
			}
		}
	}
}
