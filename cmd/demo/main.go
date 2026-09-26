package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/lex"
	"ontology/parse"
)

var fails int

func line(ok bool, text string) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", mark, text)
}

func main() {
	a := api.New()
	num := func(s string) (float64, error) {
		v, err := a.ParseText(s)
		return v.Num, err
	}
	n, err := num(`0`)
	line(err == nil && n == 0, `0 => 数 0`)
	_, err = num(`01`)
	line(errors.Is(err, lex.ErrLeadingZero), `01 => 拒绝: `+fmt.Sprint(err))
	n, err = num(`-0.5`)
	line(err == nil && n == -0.5, `-0.5 => 数 -0.5`)
	n, err = num(`1e3`)
	line(err == nil && n == 1000, `1e3 => 数 1000`)
	_, err = num(`1.`)
	line(errors.Is(err, lex.ErrEmptyFrac), `1. => 拒绝: `+fmt.Sprint(err))
	v, err := a.ParseText(`"a\nb"`)
	line(err == nil && v.Str == "a\nb", `"a\nb" => 串 "a<换行>b"`)
	v, err = a.ParseText(`"😀"`)
	line(err == nil && v.Str == "😀", `"😀" => 串 😀 U+1F600`)
	_, err = a.ParseText(`"\uD800"`)
	line(errors.Is(err, lex.ErrLoneSurrogate), `"\uD800" => 拒绝: `+fmt.Sprint(err))

	rt := true
	for _, s := range []string{`null`, `[1,"x",{"k":[true,null]}]`, `{"a":1,"b":[2,3]}`} {
		v, e1 := a.ParseText(s)
		o, e2 := a.Stringify(v)
		v2, e3 := a.ParseText(o)
		rt = rt && e1 == nil && e2 == nil && e3 == nil && v2.String() == o
	}
	_, err = a.ParseText(`{"a":1,"a":2}`)
	dup := errors.Is(err, parse.ErrDupKey)
	_, err = a.ParseText(`"\x"`)
	esc := errors.Is(err, lex.ErrBadEscape)
	line(true, fmt.Sprintf("往返一致 %v | 重复键拒绝 %v | 非法转义拒绝 %v", mark(rt), mark(dup), mark(esc)))

	var sb strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&sb, `,"k%d":%d`, i, i)
	}
	_, err = a.ParseText("{" + sb.String()[1:] + "}")
	big := err == nil
	src := `{"s":"😀","a":[0,1.5,-2e3],"o":{"k":null}}`
	want, _ := a.ParseText(src)
	outs := make([]string, 32)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := a.ParseText(src)
			if err == nil {
				outs[i] = v.String()
			}
		}(i)
	}
	wg.Wait()
	conc := want.String() != ""
	for _, o := range outs {
		conc = conc && o == want.String()
	}
	line(true, fmt.Sprintf("大m判重 %v | 并发一致 %v | SelfCheck %v", mark(big), mark(conc), mark(a.SelfCheck() == nil)))
	if fails > 0 {
		os.Exit(1)
	}
}

func mark(ok bool) string {
	if !ok {
		fails++
		return "FAIL"
	}
	return "OK"
}
