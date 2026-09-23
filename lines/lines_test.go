package lines

import (
	"bytes"
	"testing"
)

func TestSplitJoin(t *testing.T) {
	cases := [][]byte{
		nil, []byte(""), []byte("a"), []byte("a\n"), []byte("a\nb"),
		[]byte("a\r\nb\r\n"), []byte("\n\n"), []byte("\r\n\r"),
		[]byte("x\ry\n"), []byte("no newline final"), []byte("\n"),
	}
	for _, in := range cases {
		got := Join(Split(in))
		if !bytes.Equal(got, in) {
			t.Fatalf("roundtrip %q: got %q", in, got)
		}
	}
}

func TestLineShape(t *testing.T) {
	cases := []struct {
		in      string
		content string
		term    string
		noNL    bool
	}{
		{"a\n", "a", "\n", false},
		{"a\r\n", "a", "\r\n", false},
		{"a", "a", "", true},
		{"\r\n", "", "\r\n", false},
		{"", "", "", true},
	}
	for _, tc := range cases {
		ls := Split([]byte(tc.in))
		var l Line
		if tc.in == "" {
			if len(ls) != 0 {
				t.Fatalf("%q: want no lines", tc.in)
			}
			continue
		}
		l = ls[0]
		if l.Content != tc.content || l.Term != tc.term || l.NoNL() != tc.noNL {
			t.Fatalf("%q: got %+v", tc.in, l)
		}
	}
}
