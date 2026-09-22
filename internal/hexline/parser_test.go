package hexline

import (
	"bytes"
	"errors"
	"testing"
)

func feedAll(t *testing.T, line []byte, chunk int, maxLine int) (uint64, int, error) {
	t.Helper()
	p := NewParser(maxLine)
	consumed := 0
	var size uint64
	var err error
	for consumed < len(line) {
		end := consumed + chunk
		if end > len(line) {
			end = len(line)
		}
		var n int
		n, size, err = p.Feed(line[consumed:end])
		consumed += n
		if err != nil {
			return size, consumed, err
		}
	}
	if err == nil && !p.Done() {
		err = p.Close()
	}
	return size, consumed, err
}

func TestParseSizeLines(t *testing.T) {
	cases := []struct {
		line string
		want uint64
	}{
		{"a\r\n", 10},
		{"000A\r\n", 10},
		{"1f;name=value\r\n", 31},
		{"a;b=c;d=e\r\n", 10},
		{`4;x="a;b=c\"d";y=z` + "\r\n", 4},
		{`0;trailer=""` + "\r\n", 0},
	}
	for _, tc := range cases {
		for chunk := 1; chunk <= len(tc.line); chunk++ {
			got, n, err := feedAll(t, []byte(tc.line), chunk, 0)
			if err != nil {
				t.Fatalf("line %q chunk %d: %v", tc.line, chunk, err)
			}
			if n != len(tc.line) {
				t.Fatalf("line %q chunk %d: consumed %d of %d", tc.line, chunk, n, len(tc.line))
			}
			if got != tc.want {
				t.Fatalf("line %q chunk %d: size %d want %d", tc.line, chunk, got, tc.want)
			}
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		line string
		kind Kind
		off  int
	}{
		{"nonhex", "xg\r\n", KindNonHex, 0},
		{"nonhex after ext", "a;xy=!\x01\r\n", KindNonHex, 0}, // control byte not a token char
		{"bad after digits", "a=\r\n", KindNonHex, 1},
		{"bare LF", "a\n", KindNonHex, 1},
		{"missing newline", "a\r", KindNonHex, 2},
		{"line too long", "000a\r\n", KindLineTooLong, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			max := 0
			if tc.kind == KindLineTooLong {
				max = 4
			}
			var got *Error
			for chunk := 1; chunk <= len(tc.line); chunk++ {
				_, _, err := feedAll(t, []byte(tc.line), chunk, max)
				he, ok := AsError(err)
				if !ok {
					t.Fatalf("chunk %d: want *hexline.Error, got %v", chunk, err)
				}
				if got == nil {
					got = he
				} else if !errors.Is(he, got) && (he.Kind != got.Kind || he.Offset != got.Offset) {
					t.Fatalf("chunk %d: error %v/%d differs from %v/%d",
						chunk, he.Kind, he.Offset, got.Kind, got.Offset)
				}
			}
			if got == nil {
				t.Fatal("no error")
			}
			if got.Kind != tc.kind {
				t.Fatalf("kind = %d, want %d", got.Kind, tc.kind)
			}
			if tc.off != 0 && got.Offset != tc.off {
				t.Fatalf("offset = %d, want %d", got.Offset, tc.off)
			}
		})
	}
}

func TestQuotedEscapes(t *testing.T) {
	line := []byte(`5;foo="a;b=c\"d\\;e"`)
	line = append(line, '\r', '\n')
	p := NewParser(0)
	var body bytes.Buffer
	consumed := 0
	for !p.Done() {
		end := consumed + 1
		if end > len(line) {
			t.Fatal("unterminated")
		}
		n, _, err := p.Feed(line[consumed:end])
		if err != nil {
			he, _ := AsError(err)
			t.Fatalf("unexpected %v quote=%v", he, p.InQuote())
		}
		consumed += n
		body.Write(line[consumed-n : consumed])
	}
	if consumed != len(line) {
		t.Fatalf("consumed %d want %d", consumed, len(line))
	}
}

func TestUnterminatedQuote(t *testing.T) {
	line := []byte(`4;x="abc;`)
	for chunk := 1; chunk <= len(line); chunk++ {
		p := NewParser(0)
		off := 0
		for off < len(line) {
			end := off + chunk
			if end > len(line) {
				end = len(line)
			}
			n, _, err := p.Feed(line[off:end])
			if err != nil {
				t.Fatalf("chunk %d: unexpected feed error %v", chunk, err)
			}
			off += n
		}
		err := p.Close()
		he, ok := AsError(err)
		if !ok || he.Kind != KindUnterminatedQuote || he.Offset != len(line) {
			t.Fatalf("chunk %d: got %v (off %d), want unterminated quote at %d",
				chunk, err, off, len(line))
		}
	}
}
