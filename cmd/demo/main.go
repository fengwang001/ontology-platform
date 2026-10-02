// Command demo 逐项演练流式行尾/空白规范化器的语义，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"math"
	"ontology/norm"
	"ontology/par"
	"ontology/span"
	"ontology/ws"
	"os"
	"slices"
	"strings"
)

var failed int

func check(name string, ok bool) {
	s := "OK"
	if !ok {
		s, failed = "FAIL", failed+1
	}
	fmt.Printf("%s %s\n", s, name)
}

func stream(in string, opt norm.Options, cuts ...int) (string, *span.Map) {
	n := norm.New(opt)
	prev := 0
	for _, c := range append(cuts, len(in)) {
		c = min(c, len(in))
		if c >= prev {
			n.Write([]byte(in[prev:c]))
			prev = c
		}
	}
	n.Close()
	return string(n.Output()), n.Map()
}

func cutsEnum(n, k int, fn func([]int)) {
	cur := make([]int, k)
	var rec func(i, lo int)
	rec = func(i, lo int) {
		if i == k {
			fn(append([]int(nil), cur...))
			return
		}
		for ; lo <= n; lo++ {
			cur[i] = lo
			rec(i+1, lo+1)
		}
	}
	rec(0, 0)
}

func main() {
	out, _ := stream("\r\r\n", norm.Options{})
	check(`\r\r\n -> "\n\n"`, out == "\n\n")

	out, _ = stream("a \t \nb c  \n \t\n", norm.Options{})
	check("trailing-WS & blank-only line", out == "a\nb c\n\n")

	tab := [][4]string{{"", "", "", ""}, {"\n", "\n", "\n", ""},
		{"\n\n", "\n\n", "\n", ""}, {"  \r\n", "\n", "\n", ""}}
	polOK := true
	for i, pol := range []norm.Policy{norm.Preserve, norm.EnsureOne, norm.StripTrailing} {
		for _, row := range tab {
			got, _ := stream(row[0], norm.Options{Policy: pol})
			polOK = polOK && got == row[i+1]
		}
	}
	check("policy table 4x3", polOK)

	mix := "\r\na \t \r\n\r\rb  \n\n c \t"
	ref, rm := stream(mix, norm.Options{})
	splitOK := true
	for c := 0; c <= len(mix); c++ {
		got, gm := stream(mix, norm.Options{}, c)
		splitOK = splitOK && got == ref && slices.Equal(gm.Runs(), rm.Runs())
	}
	check("all split points identical", splitOK)

	idem := true
	for _, pol := range []norm.Policy{norm.Preserve, norm.EnsureOne, norm.StripTrailing} {
		for _, in := range []string{"", "\n", "\n\n", "  \r\n", "a \t \r\n\r\rb  ", mix} {
			once, _ := stream(in, norm.Options{Policy: pol})
			twice, _ := stream(once, norm.Options{Policy: pol})
			idem = idem && once == twice && !strings.ContainsRune(once, '\r')
		}
	}
	check("idempotent x3 policies", idem)

	src := "a  \nb\r\nc \t\r"
	mo, mm := stream(src, norm.Options{})
	mapOK := true
	for o := 0; o <= len(mo); o++ {
		mapOK = mapOK && mm.ToOut(mm.ToOrig(o)) == o && (o == 0 || mm.ToOrig(o) >= mm.ToOrig(o-1))
	}
	for i := 1; i <= len(src); i++ {
		mapOK = mapOK && mm.ToOut(i) >= mm.ToOut(i-1)
	}
	check("map inverse & monotone", mapOK)
	_, m1 := stream("a  \n", norm.Options{})
	_, m2 := stream("a\r\nb", norm.Options{})
	check("deleted-byte ToOut", m1.ToOut(1) == 1 && m1.ToOut(2) == 1 && m2.ToOut(1) == 1)
	n := norm.New(norm.Options{Strict: true})
	_, err := n.Write([]byte("a\x00b"))
	var at *norm.ErrAt
	check("strict NUL w/ offset", errors.Is(err, norm.ErrNUL) && errors.As(err, &at) && at.Off == 1)
	n = norm.New(norm.Options{MaxPending: 2})
	_, err = n.Write([]byte("a   \n"))
	_, err2 := n.Write([]byte("x"))
	check("pending overflow -> terminal", errors.Is(err, ws.ErrOverflow) && errors.Is(err2, norm.ErrClosed))

	trunc := true
	for c := 0; c <= len(mix); c++ {
		got, _ := stream(mix[:c], norm.Options{}, 1)
		want, _ := stream(mix[:c], norm.Options{})
		trunc = trunc && got == want
	}
	check("truncation traversal", trunc)

	parOK := true
	for k := 1; k <= 8; k++ {
		cutsEnum(len(mix), k-1, func(cuts []int) {
			got, gm, err := par.Normalize([]byte(mix), cuts, norm.Options{})
			parOK = parOK && err == nil && string(got) == ref && slices.Equal(gm.Runs(), rm.Runs())
		})
	}
	check("par all K=1..8 & cuts", parOK)

	probeOK := true
	line := "abc \t\r\n"
	for _, in := range []string{strings.Repeat(line, 100000), strings.Repeat(line, 10000000/len(line))} {
		_, m, _ := norm.Normalize([]byte(in), norm.Options{})
		bound := 2*int(math.Log2(float64(m.Len()))) + 4
		for o := 0; o <= m.OutLen(); o += 4099 {
			m.ToOrig(o)
			probeOK = probeOK && m.LastProbe() <= bound
		}
	}
	_, pm, _ := norm.Normalize([]byte(strings.Repeat("x\n", 5000000)), norm.Options{})
	check("probe bounds & pure-LF runs", probeOK && pm.Len() <= 4)

	fmt.Printf("total: %d failure(s)\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
