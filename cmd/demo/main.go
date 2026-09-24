package main

import (
	"bytes"
	"fmt"
	"ontology/logical"
	"ontology/props"
)

type report struct{ pass, fail int }

func (r *report) check(name string, ok bool) {
	if ok {
		r.pass++
		fmt.Println("OK  ", name)
	} else {
		r.fail++
		fmt.Println("FAIL", name)
	}
}

func main() {
	r := &report{}

	r.check("separators and whitespace", checkSeparators())
	r.check("comments", checkComments())
	r.check("continuations", checkContinuations())
	r.check("blank continuation", checkBlankCont())
	r.check("escapes and \\u error position", checkEscapes())
	r.check("duplicate key order", checkDupOrder())
	r.check("store special chars", checkStore())
	r.check("1000 random round trips", checkRoundTrip())
	r.check("logical checks counter", checkCounter())

	fmt.Printf("TOTAL %d/%d\n", r.pass, r.pass+r.fail)
	if r.fail > 0 {
		panic("FAIL")
	}
}

func load1(src string) *props.Properties {
	p, err := props.Load([]byte(src))
	if err != nil {
		panic(err)
	}
	return p
}

func checkSeparators() bool {
	p := load1("key=value\nkey2:value2\nkey3 value3\nkey4 = value  \nk==v\n=v\nonly\n  lead=x\n")
	want := map[string]string{"key": "value", "key2": "value2", "key3": "value3",
		"key4": "value  ", "k": "=v", "": "v", "only": "", "lead": "x"}
	for k, v := range want {
		if got, ok := p.Get(k); !ok || got != v {
			return false
		}
	}
	return p.Len() == len(want)
}

func checkComments() bool {
	p := load1("# c\n! c\n   # indented\nk=v # x\n")
	v, _ := p.Get("k")
	return v == "v # x" && p.Len() == 1
}

func join(src string) string {
	ls := logical.NewSplitter().Split([]byte(src))
	if len(ls) != 1 {
		return ""
	}
	return string(ls[0].Data)
}

func checkContinuations() bool {
	cases := map[string]string{
		"k=v\\\n   w":         "k=vw",
		"k=v\\\\\nx=y":       "k=v\\",
		"k=v\\\\\\\n  w":     "k=v\\w",
		"k=a\\\n#b":          "k=a#b",
		"k=v\\":              "k=v",
	}
	for in, want := range cases {
		if join(in) != want {
			return false
		}
	}
	return true
}

func checkBlankCont() bool {
	return join("k=v\\\n\n   \\\n  w") == "k=vw"
}

func checkEscapes() bool {
	p := load1("a\\=b=c\nk\\ 2=v\nt=\\t\\n\\r\\f\\q\\=\\ \nu=\\u00e9\n")
	for _, kv := range [][3]string{{"a=b", "c"}, {"k 2", "v"},
		{"t", "\t\n\r\fq= "}, {"u", "\u00e9"}} {
		if v, _ := p.Get(kv[0]); v != kv[1] {
			return false
		}
	}
	_, err := props.Load([]byte("k=\\u12g\n"))
	de, ok := err.(*props.DecodeError)
	return ok && de.Line == 1 && de.Column == 3
}

func checkDupOrder() bool {
	p := load1("a=1\nb=2\na=3\nc=4\n")
	var keys []string
	p.Range(func(k, _ string) bool { keys = append(keys, k); return true })
	v, _ := p.Get("a")
	return v == "3" && len(keys) == 3 && keys[0] == "a" && keys[1] == "b" && keys[2] == "c"
}

func checkStore() bool {
	p := props.New()
	p.Set("", "")
	p.Set("#x", "!y")
	p.Set("a b=c:d", " lead # !=: 中")
	p.Set("nl", "x\ny\tz\\w")
	back, err := props.Load(p.Store())
	if err != nil || back.Len() != p.Len() {
		return false
	}
	ok := true
	p.Range(func(k, v string) bool {
		gv, has := back.Get(k)
		ok = ok && has && gv == v
		return ok
	})
	return ok
}

func checkRoundTrip() bool {
	alpha := []rune{'a', ' ', '=', ':', '#', '!', '\\', '\t', '\n', '中'}
	seed := int64(1)
	rnd := func(n int) int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int(uint64(seed>>33) % uint64(n))
	}
	gen := func() string {
		n := rnd(5)
		b := make([]rune, n)
		for i := range b {
			b[i] = alpha[rnd(len(alpha))]
		}
		return string(b)
	}
	for range 1000 {
		p := props.New()
		n := 1 + rnd(4)
		for range n {
			p.Set(gen(), gen())
		}
		back, err := props.Load(p.Store())
		if err != nil || back.Len() != p.Len() {
			return false
		}
		var bk []string
		p.Range(func(k, v string) bool {
			gv, ok := back.Get(k)
			if !ok || gv != v {
				return false
			}
			bk = append(bk, k)
			return true
		})
		var want []string
		p.Range(func(k, _ string) bool { want = append(want, k); return true })
		if len(bk) != len(want) {
			return false
		}
	}
	return true
}

func checkCounter() bool {
	var buf []byte
	for range 1024 {
		buf = append(buf, "a=longvalue\\"...)
		buf = append(buf, '\n')
	}
	buf = append(buf, "a=end\n"...)
	buf = append(buf, bytes.Repeat([]byte("z=zzzzzz\n"), (1<<20)/8+1)...)
	buf = buf[:1<<20]
	sp := logical.NewSplitter()
	sp.Split(buf)
	return sp.Checks() <= 2*int64(len(buf))
}
