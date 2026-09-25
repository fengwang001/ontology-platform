package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/grapheme"
	"ontology/seg"
)

// s12 是第三节的 12 码点字符串。
const s12 = "éa‍b\r\n\U0001F1E6\U0001F1E7c👍🏻"

var want12 = []grapheme.Cluster{
	{Start: 0, End: 3, Runes: 2}, {Start: 3, End: 8, Runes: 3},
	{Start: 8, End: 10, Runes: 2}, {Start: 10, End: 18, Runes: 2},
	{Start: 18, End: 19, Runes: 1}, {Start: 19, End: 27, Runes: 2},
}

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	check("seg: Extend/Regional/NoBreak rules",
		seg.Extend(0x0301) && seg.Extend(seg.ZWJ) && seg.Extend(0x1F3FB) && seg.Extend(0xFE0F) &&
			!seg.Extend('a') && seg.Regional(0x1F1E6) && !seg.Regional(0x1F200) &&
			seg.NoBreak(seg.CR, seg.LF, 0) && seg.NoBreak(seg.ZWJ, 'b', 0) &&
			seg.NoBreak('e', 0x0301, 0) && seg.NoBreak(0x1F1E6, 0x1F1E7, 1) &&
			!seg.NoBreak(0x1F1E7, 0x1F1E8, 2) && !seg.NoBreak('a', 'b', 0))

	cs, err := grapheme.Decode(s12)
	same := err == nil && len(cs) == len(want12)
	for i := range want12 {
		if same && cs[i] != want12[i] {
			same = false
		}
	}
	check("grapheme: 12-rune string -> 6 clusters with byte offsets", same)
	chained := same && cs[0].Start == 0 && cs[len(cs)-1].End == len(s12)
	for i := 1; chained && i < len(cs); i++ {
		chained = cs[i-1].End == cs[i].Start
	}
	check("grapheme: offsets chained, last End == len(s)", chained)
	_, err = grapheme.Decode("ab\xff")
	check("grapheme: invalid UTF-8 rejected with offset",
		errors.Is(err, grapheme.ErrInvalidUTF8) && err.(*grapheme.InvalidUTF8Error).Offset == 2)

	g := api.New()
	check("api: SelfCheck passes", g.SelfCheck() == nil)
	_, e1 := g.Segments("")
	_, e2 := g.Segments("\xff")
	_, e3 := g.At(s12, 99)
	check("api: empty/invalid/oob are 3 distinct sentinel errors",
		errors.Is(e1, api.ErrEmpty) && errors.Is(e2, grapheme.ErrInvalidUTF8) &&
			errors.Is(e3, api.ErrOutOfRange) && e1 != e2 && e2 != e3 && e1 != e3)
	n, err := g.Count(s12)
	check("api: state unchanged after rejections", err == nil && n == 6)

	const m = 9999
	big := strings.Repeat("aé👍", m/3) + strings.Repeat("b", m%3)
	bcs, err := g.Segments(big)
	total := 0
	for _, c := range bcs {
		total += c.Runes
	}
	check("api: large-m single pass, runes sum == m, offsets incremental",
		err == nil && total == m && bcs[len(bcs)-1].End == len(big))

	const P = 64
	res := make([][]api.Cluster, P)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for p := 0; p < P; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			res[p], _ = g.Segments(s12)
		}(p)
	}
	close(start)
	wg.Wait()
	ident := true
	for p := 1; p < P; p++ {
		if len(res[p]) != len(res[0]) {
			ident = false
		}
		for i := range res[0] {
			if ident && res[p][i] != res[0][i] {
				ident = false
			}
		}
	}
	check("api: 64 goroutines concurrent Segments identical", ident)
	if failed {
		os.Exit(1)
	}
}
