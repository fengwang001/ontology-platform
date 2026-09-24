package props_test

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"ontology/props"
)

func pairs(m *props.Map) []props.Pair {
	out := make([]props.Pair, m.Len())
	for i := range out {
		out[i] = m.At(i)
	}
	return out
}

func parse(t *testing.T, in string) []props.Pair {
	t.Helper()
	m, err := props.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse(%q): %v", in, err)
	}
	return pairs(m)
}

func TestParseTable(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []props.Pair
	}{
		{"sep equal", "key=value", []props.Pair{{"key", "value"}}},
		{"sep colon", "key:value", []props.Pair{{"key", "value"}}},
		{"sep space", "key value", []props.Pair{{"key", "value"}}},
		{"sep ws eaten, value tail kept", "key = value  ", []props.Pair{{"key", "value  "}}},
		{"double equal", "k==v", []props.Pair{{"k", "=v"}}},
		{"empty key", "=v", []props.Pair{{"", "v"}}},
		{"key only", "k", []props.Pair{{"k", ""}}},
		{"leading ws ignored", " \t k=v", []props.Pair{{"k", "v"}}},
		{"comment hash and bang", "# c\n  ! d\nk=v", []props.Pair{{"k", "v"}}},
		{"hash inside value", "k=v # x", []props.Pair{{"k", "v # x"}}},
		{"cont basic", "k=v\\\n   w", []props.Pair{{"k", "vw"}}},
		{"cont even slashes", "k=v\\\\\nx=y", []props.Pair{{"k", `v\`}, {"x", "y"}}},
		{"cont triple slash", "k=v\\\\\\\n  w", []props.Pair{{"k", `v\w`}}},
		{"cont hash not comment", "k=a\\\n#b", []props.Pair{{"k", "a#b"}}},
		{"cont at eof", "k=v\\", []props.Pair{{"k", "v"}}},
		{"cont blank line ends", "k=v\\\n  \nx=y", []props.Pair{{"k", "v"}, {"x", "y"}}},
		{"cont ws line with slash", "k=v\\\n  \\\nw", []props.Pair{{"k", "vw"}}},
		{"escapes", `k=\t\n\r\f`, []props.Pair{{"k", "\t\n\r\f"}}},
		{"unicode escape both hex cases", "k1=A\nk2=ü", []props.Pair{{"k1", "A"}, {"k2", "ü"}}},
		{"unknown escapes", `k=\q\=\ `, []props.Pair{{"k", "q= "}}},
		{"key escaped sep", `a\=b=c`, []props.Pair{{"a=b", "c"}}},
		{"key escaped space", `k\ 2=v`, []props.Pair{{"k 2", "v"}}},
		{"dup key keeps first pos", "a=1\nb=2\na=3", []props.Pair{{"a", "3"}, {"b", "2"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parse(t, c.in); !slices.Equal(got, c.want) {
				t.Fatalf("Parse(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestBadUnicode(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		line, col int
	}{
		{"too short", `k=\u12`, 1, 3},
		{"bad hex", "x=1\nk=" + `\uZZZZ`, 2, 3},
		{"in key", `k\u12x4=v`, 1, 2},
		{"after continuation", "a=b\\\nc=" + `\u1`, 2, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := props.Parse(strings.NewReader(c.in))
			var ee *props.EscapeError
			if !errors.As(err, &ee) {
				t.Fatalf("Parse(%q): want EscapeError, got %v", c.in, err)
			}
			if ee.Line != c.line || ee.Col != c.col {
				t.Fatalf("Parse(%q): got %d:%d, want %d:%d", c.in, ee.Line, ee.Col, c.line, c.col)
			}
		})
	}
}

func TestStoreRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		pairs []props.Pair
	}{
		{"empty key", []props.Pair{{"", "v"}}},
		{"empty value", []props.Pair{{"k", ""}}},
		{"both empty", []props.Pair{{"", ""}}},
		{"spaces", []props.Pair{{" k ", "  v  "}}},
		{"seps and comment chars", []props.Pair{{"#!=:", "a=b:c#d!e"}}},
		{"backslashes", []props.Pair{{`k\`, `v\`}}},
		{"controls", []props.Pair{{"a\tb", "c\nd\re\tf"}}},
		{"non ascii", []props.Pair{{"键", "值 ü"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := props.New()
			for _, p := range c.pairs {
				m.Set(p.Key, p.Val)
			}
			var sb strings.Builder
			if err := props.Store(&sb, m); err != nil {
				t.Fatal(err)
			}
			if got := parse(t, sb.String()); !slices.Equal(got, c.pairs) {
				t.Fatalf("round trip = %v, want %v (stored %q)", got, c.pairs, sb.String())
			}
		})
	}
}

func TestRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("aB =:#!\\\t\n\r\füé")
	for n := 0; n < 1000; n++ {
		m := props.New()
		for e := 1 + rng.IntN(6); e > 0; e-- {
			m.Set(randStr(rng, alphabet), randStr(rng, alphabet))
		}
		var sb strings.Builder
		if err := props.Store(&sb, m); err != nil {
			t.Fatal(err)
		}
		got, err := props.Parse(strings.NewReader(sb.String()))
		if err != nil || !slices.Equal(pairs(got), pairs(m)) {
			t.Fatalf("round %d: got %v, want %v (stored %q, err %v)", n, pairs(got), pairs(m), sb.String(), err)
		}
	}
}

func randStr(rng *rand.Rand, alphabet []rune) string {
	b := make([]rune, rng.IntN(12))
	for i := range b {
		b[i] = alphabet[rng.IntN(len(alphabet))]
	}
	return string(b)
}
