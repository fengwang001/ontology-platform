package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/logical"
	"ontology/props"
)

func load(s string) (*props.Properties, error) {
	p := props.New()
	return p, p.Load(strings.NewReader(s))
}

func get(p *props.Properties, k string) string { v, _ := p.Get(k); return v }

func main() {
	fail := 0
	report := func(name string, ok bool) {
		if !ok {
			fail++
		}
		fmt.Printf("%s  %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	}

	// 1. separators and whitespace
	p, _ := load("key=value\nkey2:value2\nkey3 value3\nkey = value  \nk==v\n=v\nonly\n")
	report("separators/whitespace", get(p, "key") == "value" && get(p, "key2") == "value2" &&
		get(p, "key3") == "value3" && get(p, "key") == "value" && get(p, "k") == "=v" &&
		get(p, "") == "v" && get(p, "only") == "" && get(p, "key") == "value")

	// 2. comments
	p, _ = load("! c\n   # c2\nk=v # x\n")
	report("comments", p.Len() == 1 && get(p, "k") == "v # x")

	// 3. continuations incl. blank/whitespace-only continuation rows
	p, _ = load("k=v\\\n   w\n")
	p2, _ := load("k=v\\\\\nx=y\n")
	p3, _ := load("k=v\\\\\\\n  w\n")
	p4, _ := load("k=a\\\n#b\n")
	p5, _ := load("k=v\\\n\n  \n   w\n")
	p6, _ := load("k=v\\")
	report("continuations+blank-line", get(p, "k") == "vw" && get(p2, "k") == `v\` &&
		get(p2, "x") == "y" && get(p3, "k") == `v\w` && get(p4, "k") == "a#b" &&
		get(p5, "k") == "vw" && get(p6, "k") == "v")

	// 4. escapes and \uXXXX error position
	p, _ = load(`a\=b=c`+"\n"+`k\ 2=v`+"\n"+`e=\t\n\r\f\q\=\ \u4e2d`+"\n")
	_, err := load("k=\\u12zz\n")
	var de *props.DecodeError
	report("escapes+uXXXX error pos", get(p, "a=b") == "c" && get(p, "k 2") == "v" &&
		get(p, "e") == "\t\n\r\fq= 中" && errors.As(err, &de) && de.Line == 1 && de.Col == 3)

	// 5. duplicate key keeps first-occurrence order
	p, _ = load("a=1\nb=2\na=3\n")
	report("duplicate-key order", strings.Join(p.All(), ",") == "a,b" && get(p, "a") == "3")

	// 6. store special chars roundtrip
	m := props.New()
	for _, kv := range [][2]string{{"", "v"}, {"k", ""}, {" k", " v"}, {"#x", "#y"}, {"a=b:c", "x y"}, {"z\\z\n\t", "\r\f!"}} {
		p7 := props.New()
		_ = p7
	}
	var buf bytes.Buffer
	special := map[string]string{"": "v", "k": "", " k": " v", "#x": "#y", "a=b:c": "x y", "z\\z\n\t": "\r\f!"}
	_ = m
	sp := props.New()
	_ = sp.Load(mapReader(special))
	_ = sp.Store(&buf)
	rp := props.New()
	_ = rp.Load(&buf)
	ok6 := equalMap(special, rp)
	report("store special chars", ok6)

	// 7. 1000 random roundtrips
	alpha := []rune("abc =:#!\\\t\n中")
	rng := rand.New(rand.NewRandSource(42))
	ok7 := true
	for n := 0; n < 1000 && ok7; n++ {
		src := map[string]string{}
		var keys []string
		for j, cnt := 0, rng.Intn(6)+1; j < cnt; j++ {
			k := randStr(rng, alpha)
			if _, seen := src[k]; !seen {
				keys = append(keys, k)
			}
			src[k] = randStr(rng, alpha)
		}
		var b bytes.Buffer
			q := props.New()
		_ = q.Load(mapReader(src))
		_ = q.Store(&b)
		r := props.New()
		_ = r.Load(&b)
		if !equalOrdered(src, keys, r) {
			ok7 = false
		}
	}
	report("1000 random roundtrips", ok7)

	// 8. logical byte-check counter: 1 MB of long continuation chains
	big := strings.Repeat("a\\\n", 200000)
	sc := logical.NewScanner(strings.NewReader(big))
	for {
		_, e := sc.Next()
		if errors.Is(e, osEOF) {
			break
		}
	}
	report("logical byte-check counter", sc.Checks() <= 2*int64(len(big)))

	fmt.Printf("TOTAL %d/8\n", 8-fail)
	if fail > 0 {
		os.Exit(1)
	}
}

var osEOF = os.ErrInvalid

func randStr(rng *rand.Rand, alpha []rune) string {
	var b strings.Builder
	for i, n := 0, rng.Intn(5); i < n; i++ {
		b.WriteRune(alpha[rng.Intn(len(alpha))])
	}
	return b.String()
}

func mapReader(m map[string]string) *strings.Reader {
	var b strings.Builder
	for _, k := range mapKeys(m) {
		q := props.New()
		_ = q
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte('\n')
	}
	return strings.NewReader(b.String())
}

func mapKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func equalMap(m map[string]string, p *props.Properties) bool {
	if len(m) != p.Len() {
		return false
	}
	for k, v := range m {
		if gv, ok := p.Get(k); !ok || gv != v {
			return false
		}
	}
	return true
}

func equalOrdered(m map[string]string, keys []string, p *props.Properties) bool {
	if !equalMap(m, p) || strings.Join(keys, "\x00") != strings.Join(p.All(), "\x00") {
		return false
	}
	return true
}
