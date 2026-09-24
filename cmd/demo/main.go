// Command demo exercises the incremental dictionary encoder and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/dict"
	"ontology/enc"
)

var failed bool

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("OK %s | %s\n", name, detail)
		return
	}
	failed = true
	fmt.Printf("FAIL %s | %s\n", name, detail)
}

func ft(t enc.Token) string {
	switch t.Kind {
	case enc.KindReset:
		return "reset"
	case enc.KindPut:
		return fmt.Sprintf("put(%d,%s)", t.Code, t.Value)
	default:
		return fmt.Sprintf("ref(%d,%d)", t.Code, t.Count)
	}
}

func join(t []enc.Token) string {
	s := make([]string, len(t))
	for i, x := range t {
		s[i] = ft(x)
	}
	return strings.Join(s, " ")
}

// dictView derives the current decoder dictionary {code:value} from a stream.
func dictView(t []enc.Token) string {
	m := map[int]string{}
	for _, x := range t {
		switch x.Kind {
		case enc.KindReset:
			m = map[int]string{}
		case enc.KindPut:
			m[x.Code] = x.Value
		}
	}
	var b strings.Builder
	for c := 0; c < len(m); c++ {
		if c > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d:%s", c, m[c])
	}
	return b.String()
}

func main() {
	seq := []string{"a", "a", "a", "b", "c", "d", "e", "a"}
	wantEmitted := []string{"put(0,a)", "ref(0,1)", "ref(0,2)", "put(1,b)",
		"put(2,c)", "put(3,d)", "reset put(0,e)", "put(1,a)"}
	wantDict := []string{"0:a", "0:a", "0:a", "0:a 1:b", "0:a 1:b 2:c",
		"0:a 1:b 2:c 3:d", "0:e", "0:e 1:a"}
	e, _ := api.New(4)
	var steps []string
	ok8 := true
	prev := 0
	for i, v := range seq {
		_ = e.Append(v)
		tok := e.Tokens()
		em := join(tok[prev:])
		if em == "" {
			em = ft(tok[len(tok)-1]) // RLE merged into the trailing ref
		}
		prev = len(tok)
		d := dictView(tok)
		ok8 = ok8 && em == wantEmitted[i] && d == wantDict[i]
		steps = append(steps, fmt.Sprintf("%d:%s{%s}", i+1, em, d))
	}
	check("8steps", ok8, strings.Join(steps, " > "))

	want := []enc.Token{enc.PutToken(0, "a"), enc.RefToken(0, 2), enc.PutToken(1, "b"),
		enc.PutToken(2, "c"), enc.PutToken(3, "d"), enc.ResetToken(),
		enc.PutToken(0, "e"), enc.PutToken(1, "a")}
	tok := e.Tokens()
	out, derr := e.Decode(tok)
	check("stream", reflect.DeepEqual(tok, want), join(tok))
	check("roundtrip", derr == nil && strings.Join(out, "") == "aaabcdea",
		strings.Join(out, " "))

	flat, _ := e.Decode([]enc.Token{want[0], want[1]})
	check("rle-lossless", len(flat) == 3 && flat[0] == "a" && flat[2] == "a",
		"ref(0,2) => a a a")

	g1, _ := api.New(1)
	for _, v := range []string{"x", "x", "y"} {
		_ = g1.Append(v)
	}
	t1 := g1.Tokens()
	check("reset-code0", t1[len(t1)-1] == enc.PutToken(0, "y"), join(t1))

	_, ec := api.New(0)
	g2, _ := api.New(2)
	_ = g2.Append("z")
	before := g2.Tokens()
	ee := g2.Append("")
	_, eb := g2.Decode([]enc.Token{enc.RefToken(9, 1)})
	distinct := errors.Is(ec, api.ErrBadConfig) && errors.Is(ee, enc.ErrEmptyValue) &&
		errors.Is(eb, enc.ErrBadToken) && ec != ee && ec != eb && ee != eb
	check("3-errors", distinct, "badconfig / emptyvalue / badtoken")
	check("no-trace+usable", reflect.DeepEqual(g2.Tokens(), before) && g2.Append("q") == nil,
		"state intact, still appends")
	check("probe-O1", dict.SelfCheck() == nil, "m=100..10000, probes constant")

	var wg sync.WaitGroup
	const n = 32
	res := make([][]string, n)
	snap := make([][]enc.Token, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], _ = e.Decode(tok)
			snap[i] = e.Tokens()
		}(i)
	}
	wg.Wait()
	okc := true
	for i := 1; i < n; i++ {
		if strings.Join(res[i], "") != "aaabcdea" || !reflect.DeepEqual(snap[i], snap[0]) {
			okc = false
		}
	}
	check("concurrent", okc, fmt.Sprintf("%d goroutines Decode+Tokens identical", n))

	if failed {
		fmt.Println("RESULT FAIL")
		return
	}
	fmt.Println("RESULT OK")
}
