package eol

import "testing"

func TestDecoder(t *testing.T) {
	type step struct {
		in   byte
		kind Kind
		len  int
	}
	cases := []struct {
		name  string
		feed  []byte
		flush bool
		lens  []int // EolLen for each emitted line ending, in order
	}{
		{"lf", []byte{'\n'}, false, []int{1}},
		{"crlf", []byte{'\r', '\n'}, false, []int{2}},
		{"lone cr", []byte{'\r'}, true, []int{1}},
		{"cr then other", []byte{'\r', 'x'}, false, []int{1}},
		{"cr cr lf two endings", []byte{'\r', '\r', '\n'}, false, []int{1, 2}},
		{"cr cr two endings", []byte{'\r', '\r'}, true, []int{1, 1}},
		{"mixed", []byte{'a', '\r', '\n', 'b', '\r', 'c', '\n'}, false, []int{2, 1, 1}},
		{"none", []byte{'a', 'b'}, false, nil},
	}
	for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var d Decoder
				var got []int
				var feed func(byte)
				feed = func(b byte) {
					r := d.Feed(b)
					if r.Kind == LF {
						got = append(got, r.EolLen)
				}
				if r.ReplayOK {
					feed(r.Replay)
				}
			}
			for _, b := range tc.feed {
				feed(b)
			}
			if tc.flush && d.Flush() {
				got = append(got, 1)
			}
			if len(got) != len(tc.lens) {
				t.Fatalf("endings=%v want %v", got, tc.lens)
			}
			for i := range got {
				if got[i] != tc.lens[i] {
					t.Fatalf("endings=%v want %v", got, tc.lens)
				}
			}
		})
	}
}

func TestSplitEveryByte(t *testing.T) {
	inputs := [][]byte{[]byte("\r\r\n"), []byte("x\ry\r\nz\r"), []byte("\r\r\r\n\n")}
	for _, in := range inputs {
		var d Decoder
		var ref Decoder
		var split, whole []int
		var feed func(*[]int, *Decoder, byte)
		feed = func(dst *[]int, dec *Decoder, b byte) {
			r := dec.Feed(b)
			if r.Kind == LF {
				*dst = append(*dst, r.EolLen)
			}
			if r.ReplayOK {
				feed(dst, dec, r.Replay)
			}
		}
		for _, b := range in {
			feed(&whole, &ref, b)
			feed(&split, &d, b)
		}
		if ref.Flush() {
			whole = append(whole, 1)
		}
		if d.Flush() {
			split = append(split, 1)
		}
		if len(split) != len(whole) {
			t.Fatalf("%q split %v whole %v", in, split, whole)
		}
	}
}
