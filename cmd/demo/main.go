// Command demo 逐条演示 properties 解析器/回写器的语义判定。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/logical"
	"ontology/props"
)

var failed, total int

func check(name string, ok bool) {
	total++
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func eqProps(p *props.Props, want ...[2]string) bool {
	if p.Len() != len(want) {
		return false
	}
	for i, kv := range want {
		if k, v := p.At(i); k != kv[0] || v != kv[1] {
			return false
		}
	}
	return true
}

func loadOK(src string, want ...[2]string) bool {
	p, err := props.Load(src)
	return err == nil && eqProps(p, want...)
}

func main() {
	check("separators", loadOK("k1=v1\nk2:v2\nk3 v3\nk4 = v5  \nk6==v7\n=v8\nk9\n  k10=v10\n",
		[][2]string{{"k1", "v1"}, {"k2", "v2"}, {"k3", "v3"}, {"k4", "v5  "},
			{"k6", "=v7"}, {"", "v8"}, {"k9", ""}, {"k10", "v10"}}...))
	check("comments", loadOK("# c\n! d\n  # e\nk=v # x\n\t! f\n", [2]string{"k", "v # x"}))
	check("continuation", loadOK("k=v\\\n   w\nk2=v\\\\\nx=y\nk3=v\\\\\\\n  w\nk4=a\\\n#b\nk6=a\\\n   \nb=c\nk7=z\\",
		[][2]string{{"k", "vw"}, {"k2", `v\`}, {"x", "y"}, {"k3", `v\w`},
			{"k4", "a#b"}, {"k6", "a"}, {"b", "c"}, {"k7", "z"}}...))
	check("escapes", loadOK(`a\=b=c`+"\n"+`k\ 2=v`+"\n"+`e=\t\n\r\f\q\=\ `+"\n"+`u=A`+"\n"+`U=ÿ`+"\n",
		[][2]string{{"a=b", "c"}, {"k 2", "v"}, {"e", "\t\n\r\fq= "}, {"u", "A"}, {"U", "ÿ"}}...))
	check("bad-unicode-pos", checkBadUnicode())
	check("dup-order", loadOK("a=1\nb=2\na=3\nc=4\nb=5\n", [][2]string{{"a", "3"}, {"b", "5"}, {"c", "4"}}...))
	check("store-special", checkStore())
	check("random-1000", checkRandom())
	check("counter<=2x", checkCounter())
	fmt.Printf("total %d/%d\n", total-failed, total)
	if failed > 0 {
		os.Exit(1)
	}
}

func checkBadUnicode() bool {
	var ee *props.EscapeError
	if _, err := props.Load("a=b\nk=\\u12\n"); !errors.As(err, &ee) || ee.Line != 2 || ee.Col != 3 {
		return false
	}
	_, err := props.Load("k=ab\\\n  x=\\u1z")
	return errors.As(err, &ee) && ee.Line == 2 && ee.Col == 5
}

func checkStore() bool {
	var p props.Props
	p.Set("a b", " c d ")
	p.Set("#k", "#v")
	if p.Store() != "a\\ b=\\ c d \n\\#k=#v\n" {
		return false
	}
	var q props.Props
	list := [][2]string{
		{"", ""}, {"k", ""}, {" k ", " v "}, {"#!", "=:"}, {"a#b", "c!d"},
		{`a\b`, "c\nd\te"}, {"é中", "值"}, {"  ", "  "}, {"eq:=x", "y=z:w"}, {"!", "!"},
	}
	for _, kv := range list {
		q.Set(kv[0], kv[1])
	}
	r, err := props.Load(q.Store())
	return err == nil && eqProps(r, list...)
}

func checkRandom() bool {
	r := rand.New(rand.NewSource(1))
	alpha := []rune("ab =:#!\\\t\n\r\fé中")
	for i := 0; i < 1000; i++ {
		var p props.Props
		for j, n := 0, r.Intn(8); j < n; j++ {
			p.Set(randStr(r, alpha, r.Intn(6)), randStr(r, alpha, r.Intn(10)))
		}
		q, err := props.Load(p.Store())
		if err != nil || q.Len() != p.Len() {
			return false
		}
		for j := 0; j < p.Len(); j++ {
			ka, va := p.At(j)
			kb, vb := q.At(j)
			if ka != kb || va != vb {
				return false
			}
		}
	}
	return true
}

func randStr(r *rand.Rand, alpha []rune, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

// 计数器：1 MB 长续行链输入，字节检查总次数 ≤ 2 × 输入字节数。
func checkCounter() bool {
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString("key=some-long-value-chunk-abcdefghijklmnopqrstuvwxyz0123456789\\\n   ")
	}
	data := []byte(sb.String())
	var sc logical.Scanner
	sc.Split(data)
	return sc.Checked() > 0 && sc.Checked() <= 2*len(data)
}
