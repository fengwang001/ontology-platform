package ontology

import (
	"strings"
	"testing"
)

func TestSpecialCharacters(t *testing.T) {
	cases := []struct{ name, input string }{
		{"starts-with-sharp-s", "ßabc"},
		{"ends-with-sharp-s", "abcß"},
		{"only-sharp-s", "ß"},
		{"sharp-s-separated", "ß-ß"},
		{"combining-in-middle", "au\u0308bc"},
		{"combining-at-end", "abc\u0301!"},
		{"combining-after-separator", "ab \u0300cd"},
		{"combining-between-runes", "x\u0308\u0301y"},
		{"fi-ligature-3-byte", "ﬁ"},
		{"fi-ligature-in-word", "aﬁb"},
		{"fl-ligature", "ﬂﬂ"},
		{"ae-ligatures", "æÆ"},
		{"oe-ligatures", "œŒ"},
		{"ascii-uppercase", "HELLO World 42"},
		{"non-ascii-separator", "abécd"},
		{"deleted-at-stream-end", "abc\u0300"},
		{"trailing-deleted-run", "ab\u0300\u0301"},
		{"mixed", "Straße ﬁnd œuf Æther ABC\u0304 x"},
		{"empty", ""},
		{"only-separators", "  ---  ."},
		{"cjk-separator", "hello世界world"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runSplits(t, c.input) })
	}
}

func TestSharpSOffsetSemantics(t *testing.T) {
	tz := NewTokenizer()
	got, err := tz.Feed([]byte("ßab "))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "ssab" || got[0].Start != 0 || got[0].End != 4 {
		t.Fatalf("unexpected %+v", got)
	}

	tz2 := NewTokenizer()
	out2, _ := tz2.Feed([]byte("abß"))
	fin, err := tz2.Close()
	if err != nil {
		t.Fatal(err)
	}
	got = append(out2, fin...)
	if len(got) != 1 || got[0].Text != "abss" || got[0].Start != 0 || got[0].End != 4 {
		t.Fatalf("unexpected %+v", got)
	}

	// Combining mark at token end swallowed into End.
	tz3 := NewTokenizer()
	out3, _ := tz3.Feed([]byte("ab\u0300x"))
	fin3, err := tz3.Close()
	if err != nil {
		t.Fatal(err)
	}
	out3 = append(out3, fin3...)
	if len(out3) != 1 || out3[0].Text != "abx" || out3[0].End != 5 {
		t.Fatalf("mark-swallow token %+v", out3)
	}

	tz4 := NewTokenizer()
	fin, err = tz4.Close()
	if err != nil || fin != nil {
		t.Fatalf("empty close: fin=%+v err=%v", fin, err)
	}
}

func TestTokenLengthBoundary(t *testing.T) {
	runSplits(t, strings.Repeat("a", 64))

	input65 := strings.Repeat("b", 65)
	tz := NewTokenizer()
	out, err := tz.Feed([]byte("xx " + input65 + " zz"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := tz.Close()
	if err != nil {
		t.Fatal(err)
	}
	got = append(out, got...)
	if len(got) != 2 {
		t.Fatalf("want 2 reported tokens, got %+v", got)
	}
	if got[0].Text != "xx" || got[0].Pos != 0 {
		t.Fatalf("first token %+v", got[0])
	}
	if got[1].Text != "zz" || got[1].Pos != 2 {
		t.Fatalf("position gap expected, got %+v", got[1])
	}
	if s := tz.Stats(); s != (Stats{Tokens: 2, Dropped: 1, Consumed: 3 + 65 + 3}) {
		t.Fatalf("stats %+v", s)
	}

	// 32 sharp-s runes fold to exactly 64 output bytes and are kept.
	fold64 := strings.Repeat("ß", 32)
	tz2 := NewTokenizer()
	if _, err := tz2.Feed([]byte(fold64)); err != nil {
		t.Fatal(err)
	}
	fin, err := tz2.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(fin) != 1 || len(fin[0].Text) != 64 || fin[0].Pos != 0 {
		t.Fatalf("fold64 token %+v", fin)
	}

	// 33 sharp-s runes fold to 66 bytes and are dropped; position spent.
	tz3 := NewTokenizer()
	if _, err := tz3.Feed([]byte(strings.Repeat("ß", 33) + " z")); err != nil {
		t.Fatal(err)
	}
	got, err = tz3.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "z" || got[0].Pos != 1 {
		t.Fatalf("fold66 gap %+v", got)
	}
	if s := tz3.Stats(); s.Dropped != 1 || s.Tokens != 1 {
		t.Fatalf("fold66 stats %+v", s)
	}
}

func TestNoAlias(t *testing.T) {
	raw := []byte("alpha beta")
	tz := NewTokenizer()
	got, _ := tz.Feed(raw)
	if len(got) != 1 || got[0].Text != "alpha" {
		t.Fatalf("setup %+v", got)
	}
	raw[0] = 'z'
	if got[0].Text != "alpha" {
		t.Fatalf("returned token aliases input buffer: %q", got[0].Text)
	}
	rest, err := tz.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].Text != "beta" {
		t.Fatalf("rest %+v", rest)
	}
}

func TestReplayDeterminism(t *testing.T) {
	input := "Straße—ﬁnd œuf, ÆTHER abc\u0304!! 42"
	raw := []byte(input)
	first := feedChunks(t, NewTokenizer(), raw, []int{3, 9, 20, len(raw)})
	second := feedChunks(t, NewTokenizer(), raw, []int{1, 1, len(raw)})
	if len(first) != len(second) {
		t.Fatalf("replay length %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	t.Logf("replay OK\n  input =%q\n  output=%+v\n  basis =two different splittings produced identical tokens",
		input, first)
}
