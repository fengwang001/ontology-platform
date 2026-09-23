package eol

import "testing"

func feedAll(s *Splitter, data string) []Event {
	var ev []Event
	for i := 0; i < len(data); i++ {
		ev = append(ev, s.Feed(data[i])...)
	}
	return ev
}

func TestSplitter(t *testing.T) {
	cases := []struct {
		in string
		lf int
	}{
		{"", 0},
		{"a", 0},
		{"\n", 1},
		{"\r", 1},
		{"\r\n", 1},
		{"\r\r\n", 2},
		{"\r\r", 2},
		{"a\rb\r\nc\nd", 3},
		{"\n\r\n\r", 3},
	}
	for _, c := range cases {
		// Every possible split point must yield the same event stream.
		var base []Event
		for cut := 0; cut <= len(c.in); cut++ {
			s := &Splitter{}
			got := append(feedAll(s, c.in[:cut]), feedAll(s, c.in[cut:])...)
			got = append(got, s.Flush()...)
			if cut == 0 {
				base = got
				continue
			}
			if len(got) != len(base) {
				t.Fatalf("%q cut %d: %v != %v", c.in, cut, got, base)
			}
			for i := range got {
				if got[i] != base[i] {
					t.Fatalf("%q cut %d: %v != %v", c.in, cut, got, base)
				}
			}
		}
		lf := 0
		for _, e := range base {
			if e == LF {
				lf++
			}
		}
		if lf != c.lf {
			t.Errorf("%q: LF count %d want %d", c.in, lf, c.lf)
		}
	}
}
