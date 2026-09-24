package main

import (
	"fmt"
	"math/rand"
	"strings"

	"ontology/logical"
	"ontology/props"
)

func loadOne(s string) *props.Props {
	p := props.New()
	if err := p.Load(strings.NewReader(s)); err != nil {
		panic(err)
	}
	return p
}

func get(p *props.Props, k string) string {
	v, _ := p.Get(k)
	return v
}

func checkSeparators() bool {
	type tc struct{ in, key, val string }
	cases := []tc{
		{"key=value", "key", "value"},
		{"key:value", "key", "value"},
		{"key value", "key", "value"},
		{"key = value  ", "key", "value  "},
		{"k==v", "k", "=v"},
		{"=v", "", "v"},
		{"k", "k", ""},
		{"a\\=b=c", "a=b", "c"},
		{"k\\ 2=v", "k 2", "v"},
	}
	for _, c := range cases {
		if get(loadOne(c.in), c.key) != c.val {
			return false
		}
	}
	return true
}

func checkComments() bool {
	p := loadOne("# c\n! c\n  # c\nk=v # x\n")
	return p.Len() == 1 && get(p, "k") == "v # x"
}

func checkContinuation() bool {
	if get(loadOne("k=v\\\n   w"), "k") != "vw" {
		return false
	}
	p := loadOne("k=v\\\\\nx=y")
	if get(p, "k") != `v\` || get(p, "x") != "y" {
		return false
	}
	if get(loadOne("k=v\\\\\\\n  w"), "k") != `v\w` {
		return false
	}
	if get(loadOne("k=a\\\n#b"), "k") != "a#b" {
		return false
	}
	if get(loadOne("k=v\\"), "k") != "v" {
		return false
	}
	// whitespace-only continuation terminates the logical line.
	p = loadOne("k=a\\\n   \nx=b")
	return get(p, "k") == "a" && get(p, "x") == "b"
}

func checkEscape() bool {
	p := loadOne("a=\\t\\n\\r\\f\\q\\=\\ \\u0041bc")
	if get(p, "a") != "\t\n\r\fq= Abc" {
		return false
	}
	err := props.New().Load(strings.NewReader("a=b\\u12x\n")).(error)
	de, ok := err.(*props.DecodeError)
	return ok && de.Line == 1 && de.Col == 4
}

func checkDupOrder() bool {
	p := loadOne("a=1\nb=2\na=3\n")
	return fmt.Sprint(p.Ordered()) == "[a b]" && get(p, "a") == "3"
}

func checkStore() bool {
	p := props.New()
	p.Set("", "v")
	p.Set(" #!:=", "x")
	p.Set("v", " leading and trailing  ")
	var b strings.Builder
	if err := p.Store(&b); err != nil {
		return false
	}
	q := loadOne(b.String())
	if q.Len() != p.Len() {
		return false
	}
	for _, k := range p.Ordered() {
		if get(q, k) != get(p, k) {
			return false
		}
	}
	return true
}

func checkRoundTrip(n int) bool {
	alpha := []rune{'a', 'b', ' ', '\t', '=', ':', '#', '!', '\\', '\n', '\r', '中'}
	rnd := rand.New(rand.NewSource(42))
	randStr := func() string {
		l := rnd.Intn(5)
		rs := make([]rune, l)
		for i := range rs {
			rs[i] = alpha[rnd.Intn(len(alpha))]
		}
		return string(rs)
	}
	for t := 0; t < n; t++ {
		p := props.New()
		for j, n := 0, rnd.Intn(8)+1; j < n; j++ {
			p.Set(randStr(), randStr())
		}
		var b strings.Builder
		if err := p.Store(&b); err != nil {
			return false
		}
		q := loadOne(b.String())
		if q.Len() != p.Len() {
			return false
		}
		for _, k := range p.Ordered() {
			if get(q, k) != get(p, k) {
				return false
			}
		}
		if fmt.Sprint(q.Ordered()) != fmt.Sprint(p.Ordered()) {
			return false
		}
	}
	return true
}

func checkCounter() bool {
	var sb strings.Builder
	for i := 0; i < 1024*1024; i++ {
		switch {
		case i%100 == 0:
			sb.WriteString("k=v")
		case i%100 == 99:
			sb.WriteString("\\\n")
		default:
			sb.WriteByte('a')
		}
	}
	r, _ := logical.NewReader(strings.NewReader(sb.String()))
	for r.Next() != nil {
	}
	return r.Checks() <= 2*int64(sb.Len())
}

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}
	check("separators", checkSeparators())
	check("comments", checkComments())
	check("continuation", checkContinuation())
	check("escape", checkEscape())
	check("dup-order", checkDupOrder())
	check("store", checkStore())
	check("roundtrip-1000", checkRoundTrip(1000))
	check("counter", checkCounter())
	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
}
