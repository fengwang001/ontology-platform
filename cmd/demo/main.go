// Command demo exercises the logical and props packages and prints
// one OK/FAIL line per semantic requirement.
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"

	"ontology/logical"
	"ontology/props"
)

var checks, failed int

func P(k, v string) props.Pair { return props.Pair{Key: k, Val: v} }

func check(name string, ok bool) {
	checks++
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Println(status, name)
}

func parse(in string) []props.Pair {
	m, err := props.Parse(strings.NewReader(in))
	if err != nil {
		return nil
	}
	out := make([]props.Pair, m.Len())
	for i := range out {
		out[i] = m.At(i)
	}
	return out
}

func eq(got []props.Pair, want ...props.Pair) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func roundTrip(m *props.Map) bool {
	var sb strings.Builder
	if err := props.Store(&sb, m); err != nil {
		return false
	}
	got, err := props.Parse(strings.NewReader(sb.String()))
	if err != nil || got.Len() != m.Len() {
		return false
	}
	for i := 0; i < m.Len(); i++ {
		if got.At(i) != m.At(i) {
			return false
		}
	}
	return true
}

func randStr(rng *rand.Rand, alphabet []rune) string {
	b := make([]rune, rng.IntN(12))
	for i := range b {
		b[i] = alphabet[rng.IntN(len(alphabet))]
	}
	return string(b)
}

func main() {
	check("separators and whitespace", eq(parse("k1=v\nk2:v\nk3 v\nk4 = v  \nk5==v\n=x\nk6"),
		P("k1", "v"), P("k2", "v"), P("k3", "v"), P("k4", "v  "), P("k5", "=v"), P("", "x"), P("k6", "")))
	check("comments", eq(parse("# c\n  ! d\nk=v # x"), P("k", "v # x")))
	check("continuations", eq(parse("k1=v\\\n  w\nk2=v\\\\\nx=y\nk3=v\\\\\\\n w\nk4=a\\\n#b\nk5=v\\"),
		P("k1", "vw"), P("k2", `v\`), P("x", "y"), P("k3", `v\w`), P("k4", "a#b"), P("k5", "v")))
	check("blank continuation ends line", eq(parse("k=v\\\n   \nx=y"), P("k", "v"), P("x", "y")))
	check("escapes", eq(parse(`k=\t\n\r\fA\q\=\ x`), P("k", "\t\n\r\fAq= x")))
	check("key escapes", eq(parse(`a\=b=c`+"\n"+`k\ 2=v`), P("a=b", "c"), P("k 2", "v")))
	_, err := props.Parse(strings.NewReader("x=1\nk=" + `\u12`))
	var ee *props.EscapeError
	check("bad \\u reports line and column", errors.As(err, &ee) && ee.Line == 2 && ee.Col == 3)
	check("duplicate keys keep first position", eq(parse("a=1\nb=2\na=3"), P("a", "3"), P("b", "2")))
	m := props.New()
	for _, kv := range [][2]string{{"", ""}, {" k ", " v "}, {"#!=:", "a=b#c!"}, {`k\`, "v\\\n"}, {"键", "值\t"}} {
		m.Set(kv[0], kv[1])
	}
	check("store round trip of special chars", roundTrip(m))
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("aB =:#!\\\t\n\r\füé")
	ok := true
	for n := 0; n < 1000 && ok; n++ {
		m := props.New()
		for e := 1 + rng.IntN(6); e > 0; e-- {
			m.Set(randStr(rng, alphabet), randStr(rng, alphabet))
		}
		ok = roundTrip(m)
	}
	check("1000 random round trips", ok)
	var sb strings.Builder
	sb.WriteString("k=")
	for sb.Len() < 1<<20 {
		sb.WriteString(strings.Repeat("x", 500) + "\\\n")
	}
	sb.WriteString("end\n")
	in := sb.String()
	lines, err := logical.Lines(strings.NewReader(in))
	check("1MB chain: byte checks <= 2x input", err == nil && len(lines) == 1 && logical.Checked() <= int64(2*len(in)))
	fmt.Printf("total: %d checks, %d failed\n", checks, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
