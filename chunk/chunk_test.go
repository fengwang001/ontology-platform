package chunk

import "testing"

func TestIterChunks(t *testing.T) {
	cases := []struct {
		in   string
		want []Chunk
	}{
		{"", nil},
		{"abc", []Chunk{{"abc", false}}},
		{"123", []Chunk{{"123", true}}},
		{"file10", []Chunk{{"file", false}, {"10", true}}},
		{"a01b2", []Chunk{{"a", false}, {"01", true}, {"b", false}, {"2", true}}},
		{"9x", []Chunk{{"9", true}, {"x", false}}},
		{"１２3", []Chunk{{"１２", false}, {"3", true}}},
		{"a\x00b1", []Chunk{{"a\x00b", false}, {"1", true}}},
	}
	for _, c := range cases {
		it := New(c.in)
		var got []Chunk
		for {
			ch, ok := it.Next()
			if !ok {
				break
			}
			got = append(got, ch)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%q: got %d chunks, want %d", c.in, len(got), len(c.want))
		}
		for i, ch := range got {
			if ch != c.want[i] {
				t.Errorf("%q: chunk %d = %+v, want %+v", c.in, i, ch, c.want[i])
			}
		}
		joined := ""
		for _, ch := range got {
			joined += ch.Text
		}
		if joined != c.in {
			t.Errorf("%q: chunks rejoin to %q", c.in, joined)
		}
	}
}
