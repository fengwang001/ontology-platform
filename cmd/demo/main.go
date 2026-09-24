// Command demo prints OK/FAIL checks for the properties parser.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"

	"ontology/logical"
	"ontology/props"
)

var fails int

func check(name string, ok bool) {
	s := "OK  "
	if !ok {
		s = "FAIL"
		fails++
	}
	fmt.Println(s, name)
}

func load(s string) *props.Map {
	m, _ := props.Load(strings.NewReader(s))
	return m
}

// ents reports whether m holds exactly the flattened key/value list.
func ents(m *props.Map, kv ...string) bool {
	var want []props.Entry
	for i := 0; i+1 < len(kv); i += 2 {
		want = append(want, props.Entry{Key: kv[i], Val: kv[i+1]})
	}
	return m != nil && reflect.DeepEqual(m.Ent, want)
}

func main() {
	check("separators", ents(load("a=1\nb:2\nc 3\nd = e  \nf==g\n=h\ni\n"),
		"a", "1", "b", "2", "c", "3", "d", "e  ", "f", "=g", "", "h", "i", ""))
	check("comments", ents(load("# x\n! y\nk=v # x\n"), "k", "v # x"))
	check("continuation", checkContinuation())
	check("escapes", checkEscapes())
	check("duplicate-order", ents(load("a=1\nb=2\na=3\n"), "a", "3", "b", "2"))
	check("store-special", checkStore())
	check("roundtrip-1000", checkRoundtrip())
	check("counter", checkCounter())
	fmt.Printf("total: %d fail(s)\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}

func checkContinuation() bool {
	cases := []struct{ in, want string }{
		{"k=v\\\n   w\n", "k=vw"},
		{"k=v\\\\\nx=y\n", "k=v\\\\|x=y"},
		{"k=v\\\\\\\n  w\n", "k=v\\\\w"},
		{"k=a\\\n#b\n", "k=a#b"},
		{"k=v\\", "k=v"},
		{"k=v\\\n   \nw\n", "k=v|w"},
	}
	for _, c := range cases {
		lines, err := logical.Split(strings.NewReader(c.in))
		var got []string
		for _, l := range lines {
			got = append(got, l.Text)
		}
		if err != nil || strings.Join(got, "|") != c.want {
			return false
		}
	}
	return true
}

func checkEscapes() bool {
	if !ents(load("k=a\\tb\\u0041\\q\\=\na\\=b=c\nk\\ 2=v\n"),
		"k", "a\tbAq=", "a=b", "c", "k 2", "v") {
		return false
	}
	_, err := props.Load(strings.NewReader("x\nk=\\u12g4\n"))
	var ee *props.EscapeError
	if !errors.As(err, &ee) || ee.Line != 2 || ee.Col != 3 {
		return false
	}
	_, err = props.Load(strings.NewReader("k=\\u12"))
	return errors.As(err, &ee) && ee.Line == 1 && ee.Col == 3
}

func roundtrip(m *props.Map) bool {
	var buf bytes.Buffer
	if err := m.Store(&buf); err != nil {
		return false
	}
	back, err := props.Load(&buf)
	return err == nil && reflect.DeepEqual(back.Ent, m.Ent)
}

func checkStore() bool {
	m := new(props.Map)
	for _, kv := range [][2]string{
		{"", ""}, {" k", " v"}, {"a=b", "x:y"}, {"#h", "!v"},
		{"t\tk", "v\nv"}, {"bs\\", "\\"}, {"非", "值✓"}, {"s p", "a  b "},
	} {
		m.Set(kv[0], kv[1])
	}
	return roundtrip(m)
}

func randStr(rng *rand.Rand, alpha []rune) string {
	s := make([]rune, rng.Intn(7))
	for i := range s {
		s[i] = alpha[rng.Intn(len(alpha))]
	}
	return string(s)
}

func checkRoundtrip() bool {
	rng := rand.New(rand.NewSource(1))
	alpha := []rune("ab =:#!\\\t\n\r\fé一")
	for n := 0; n < 1000; n++ {
		m := new(props.Map)
		for j := 0; j < 4; j++ {
			m.Set(randStr(rng, alpha), randStr(rng, alpha))
		}
		if !roundtrip(m) {
			return false
		}
	}
	return true
}

func checkCounter() bool {
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString("k=")
		for i := 0; i < 100; i++ {
			sb.WriteString(strings.Repeat("v", 99) + "\\\n")
		}
		sb.WriteString("end\n")
	}
	logical.Reset()
	_, err := logical.Split(strings.NewReader(sb.String()))
	return err == nil && logical.Checked() <= 2*int64(sb.Len())
}
