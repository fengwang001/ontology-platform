package props

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func loadOne(t *testing.T, in string) *Properties {
	t.Helper()
	p, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load(%q): %v", in, err)
	}
	return p
}

func TestSeparators(t *testing.T) {
	tests := []struct {
		in, key, val string
	}{
		{"key=value\n", "key", "value"},
		{"key:value\n", "key", "value"},
		{"key value\n", "key", "value"},
		{"key = value  \n", "key", "value  "},
		{"k==v\n", "k", "=v"},
		{"=v\n", "", "v"},
		{"k\n", "k", ""},
		{"   k=v\n", "k", "v"},
	}
	for _, tc := range tests {
		p := loadOne(t, tc.in)
		if v, ok := p.Get(tc.key); !ok || v != tc.val {
			t.Fatalf("%q: got %q,%v want %q", tc.in, v, ok, tc.val)
		}
	}
}

func TestComments(t *testing.T) {
	tests := []string{
		"# c\n! c\n  \t# c\nk=v # x\n",
	}
	for _, in := range tests {
		p := loadOne(t, in)
		if p.Len() != 1 {
			t.Fatalf("%q: len=%d", in, p.Len())
		}
		if v, _ := p.Get("k"); v != "v # x" {
			t.Fatalf("got %q", v)
		}
	}
}

func TestContinuation(t *testing.T) {
	tests := []struct {
		in, key, val string
	}{
		{"k=v\\\n   w\n", "k", "vw"},
		{"k=v\\\\\nx=y\n", "k", "\\"},
		{"k=v\\\\\\\n  w\n", "k", "\\w"},
		{"k=a\\\n#b\n", "k", "a#b"},
		{"k=v\\", "k", "v"},
		{"k=v\\\n   \n  w=x\n", "k", "v"},
	}
	for _, tc := range tests {
		p := loadOne(t, tc.in)
		if v, _ := p.Get(tc.key); v != tc.val {
			t.Fatalf("%q: got %q want %q", tc.in, v, tc.val)
		}
	}
}

func TestEscapes(t *testing.T) {
	tests := []struct {
		in, key, val string
	}{
		{`a\=b=c` + "\n", "a=b", "c"},
		{`k\ 2=v` + "\n", "k 2", "v"},
		{`k=a\tb\nc\rd\fe` + "\n", "k", "a\tb\nc\rd\fe"},
		{`k=\q\=\ \:\#` + "\n", "k", "q= :#"},
		{`k=\u0041\u00e9` + "\n", "k", "Aé"},
	}
	for _, tc := range tests {
		p := loadOne(t, tc.in)
		if v, _ := p.Get(tc.key); v != tc.val {
			t.Fatalf("%q: got %q want %q", tc.in, v, tc.val)
		}
	}
}

func TestUnicodeError(t *testing.T) {
	tests := []struct {
		in        string
		line, col int
	}{
		{"k=\\u004\n", 1, 3},
		{"# comment\nk=\\u12zz\n", 2, 3},
		{"k=v\\\n  \\u00\n", 2, 3},
	}
	for _, tc := range tests {
		_, err := Load(strings.NewReader(tc.in))
		var se *SyntaxError
		if !errors.As(err, &se) || se.Line != tc.line || se.Column != tc.col {
			t.Fatalf("%q: err=%v want line %d col %d", tc.in, err, tc.line, tc.col)
		}
	}
}

func TestDuplicateOrder(t *testing.T) {
	p := loadOne(t, "a=1\nb=2\na=3\n")
	if pairs := p.Pairs(); len(pairs) != 2 || pairs[0] != (Pair{"a", "3"}) ||
		pairs[1] != (Pair{"b", "2"}) {
		t.Fatalf("got %v", pairs)
	}
}

func TestStoreSpecial(t *testing.T) {
	p := New()
	p.Set("", "x")
	p.Set("k", "")
	p.Set(" a", " v")
	p.Set("#!", "=: #!\n\\t")
	p.Set("a=b:c d", "middle space")
	p.Set("中文", "日本語")
	var buf bytes.Buffer
	if err := p.Store(&buf); err != nil {
		t.Fatal(err)
	}
	q, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got, want := q.Pairs(), p.Pairs()
	if len(got) != len(want) {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("[%d] got %q want %q\ntext:\n%s", i, got[i], want[i], buf.String())
		}
	}
}

func TestRoundTripRandom(t *testing.T) {
	const alphabet = "ab #!=:\\\n\t\r\fé中"
	rng := rand.New(rand.NewSource(1))
	gen := func() string {
		n := rng.Intn(8)
		b := make([]byte, 0, n*3)
		for i := 0; i < n; i++ {
			b = append(b, []byte(string(alphabet[rng.Intn(len([]rune(alphabet)))]))...)
		}
		return string(b)
	}
	for i := 0; i < 1000; i++ {
		p := New()
		n := 1 + rng.Intn(6)
		for j := 0; j < n; j++ {
			p.Set(gen(), gen())
		}
		var buf bytes.Buffer
		if err := p.Store(&buf); err != nil {
			t.Fatal(err)
		}
		q, err := Load(&buf)
		if err != nil {
			t.Fatalf("iter %d text=%q: %v", i, buf.String(), err)
		}
		got, want := q.Pairs(), p.Pairs()
		if len(got) != len(want) {
			t.Fatalf("iter %d len mismatch", i)
		}
		for k := range got {
			if got[k] != want[k] {
				t.Fatalf("iter %d %q != %q text=%q", i, got[k], want[k], buf.String())
			}
		}
	}
}
