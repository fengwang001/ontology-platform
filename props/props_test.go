package props_test

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/props"
)

func flat(m *props.Map) []string {
	var out []string
	for _, e := range m.Ent {
		out = append(out, e.Key, e.Val)
	}
	return out
}

func TestLoad(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"eq", "key=value\n", []string{"key", "value"}},
		{"colon", "key:value\n", []string{"key", "value"}},
		{"space", "key value\n", []string{"key", "value"}},
		{"sep-ws-value-tail-kept", "key = value  \n", []string{"key", "value  "}},
		{"double-eq", "k==v\n", []string{"k", "=v"}},
		{"empty-key", "=v\n", []string{"", "v"}},
		{"key-only", "k\n", []string{"k", ""}},
		{"leading-ws", "   k=v\n", []string{"k", "v"}},
		{"comment-hash", "# c\nk=v\n", []string{"k", "v"}},
		{"comment-bang", "! c\nk=v\n", []string{"k", "v"}},
		{"hash-in-value", "k=v # x\n", []string{"k", "v # x"}},
		{"escaped-seps-in-key", "a\\=b=c\n", []string{"a=b", "c"}},
		{"escaped-space-in-key", "k\\ 2=v\n", []string{"k 2", "v"}},
		{"escapes", "k=\\t\\n\\r\\f\n", []string{"k", "\t\n\r\f"}},
		{"unicode", "k=\\u0041\\u00e9\\u4E00z\n", []string{"k", "Aé一z"}},
		{"unknown-escape", "k=\\q\\=\n", []string{"k", "q="}},
		{"dup-first-pos", "a=1\nb=2\na=3\n", []string{"a", "3", "b", "2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := props.Load(strings.NewReader(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if got := flat(m); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q want %q", got, c.want)
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
		{"short", "k=\\u12\n", 1, 3},
		{"bad-hex", "k=\\u12g4\n", 1, 3},
		{"second-line", "a=1\nk=\\uZZZZ\n", 2, 3},
		{"indented", "  k=\\u1\n", 1, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := props.Load(strings.NewReader(c.in))
			var ee *props.EscapeError
			if !errors.As(err, &ee) {
				t.Fatalf("err = %v", err)
			}
			if ee.Line != c.line || ee.Col != c.col {
				t.Errorf("got %d:%d want %d:%d", ee.Line, ee.Col, c.line, c.col)
			}
		})
	}
}

func TestStore(t *testing.T) {
	cases := []struct{ name, key, val, out string }{
		{"plain", "k", "v", "k=v\n"},
		{"key-space", "a b", "v", "a\\ b=v\n"},
		{"key-seps", "a=b:c", "v", "a\\=b\\:c=v\n"},
		{"key-hash-bang", "#k", "v", "\\#k=v\n"},
		{"key-bang", "!k", "v", "\\!k=v\n"},
		{"key-hash-mid", "a#b", "v", "a#b=v\n"},
		{"empty-key", "", "v", "=v\n"},
		{"empty-both", "", "", "=\n"},
		{"value-leading-space", "k", " v", "k=\\ v\n"},
		{"value-inner-space", "k", "a b", "k=a b\n"},
		{"value-trailing-space", "k", "v ", "k=v \n"},
		{"value-hash", "k", "#v", "k=#v\n"},
		{"value-seps", "k", "a=b:c", "k=a=b:c\n"},
		{"backslash", "k", "a\\b", "k=a\\\\b\n"},
		{"controls", "k", "\t\n\r\f", "k=\\t\\n\\r\\f\n"},
		{"non-ascii", "键", "值", "键=值\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := new(props.Map)
			m.Set(c.key, c.val)
			var buf bytes.Buffer
			if err := m.Store(&buf); err != nil {
				t.Fatal(err)
			}
			if buf.String() != c.out {
				t.Fatalf("got %q want %q", buf.String(), c.out)
			}
			back, err := props.Load(&buf)
			if err != nil || !reflect.DeepEqual(back.Ent, m.Ent) {
				t.Errorf("roundtrip got %v err %v", back.Ent, err)
			}
		})
	}
}

func TestRandomRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alpha := []rune("ab =:#!\\\t\n\r\fé一")
	for n := 0; n < 1000; n++ {
		m := new(props.Map)
		for j := 0; j < 4; j++ {
			m.Set(randStr(rng, alpha), randStr(rng, alpha))
		}
		var buf bytes.Buffer
		if err := m.Store(&buf); err != nil {
			t.Fatal(err)
		}
		back, err := props.Load(&buf)
		if err != nil || !reflect.DeepEqual(back.Ent, m.Ent) {
			t.Fatalf("round %d: got %v want %v err %v", n, back.Ent, m.Ent, err)
		}
	}
}

func randStr(rng *rand.Rand, alpha []rune) string {
	s := make([]rune, rng.Intn(7))
	for i := range s {
		s[i] = alpha[rng.Intn(len(alpha))]
	}
	return string(s)
}
