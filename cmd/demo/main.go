// Command demo exercises the hinted-handoff packages and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/hh"
	"ontology/hint"
)

var fails int

func bump(p *int64, v int64) bool { *p = v; return true }
func check(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL " + name)
		return
	}
	fmt.Println("OK   " + name)
}

func main() {
	b := hint.NewBuffer(4)
	for _, e := range []hint.Entry{{Key: "k", Ver: 5, Value: "a"}, {Key: "k", Ver: 7, Value: "b"}, {Key: "k", Ver: 6, Value: "c"}, {Key: "k", Ver: 7, Value: "b2"}} {
		b.Append(e)
	}
	var cur int64
	ap, sk := b.Replay(func(e hint.Entry) bool { return hint.ShouldApply(e.Ver, cur) && bump(&cur, e.Ver) })
	full := hint.NewBuffer(1)
	full.Append(hint.Entry{Key: "k", Ver: 1})
	err := full.Append(hint.Entry{Key: "k", Ver: 2})
	check("replay strict-> drops stale/ties; full buffer ErrFull, no trace",
		ap == 2 && sk == 2 && cur == 7 && b.Len() == 0 && errors.Is(err, hint.ErrFull) && full.Len() == 1)
	c, _ := hh.New(3, 3)
	c.Down(1)
	W := func(v string, x int64) func() (int, int, error) {
		return func() (int, int, error) { return 0, 0, c.Write("k", v, x) }
	}
	acts := []func() (int, int, error){W("a", 5), W("b", 7), W("c", 6), W("d", 8),
		func() (int, int, error) { return c.Up(1) },
		func() (int, int, error) { c.Down(1); return W("e", 9)() },
		W("f", 9), func() (int, int, error) { return c.Up(1) }}
	wantS := []string{"a@5|@0|a@5", "b@7|@0|b@7", "b@7|@0|b@7", "b@7|@0|b@7",
		"b@7|b@7|b@7", "e@9|b@7|e@9", "e@9|b@7|e@9", "e@9|e@9|e@9"}
	wantH := []string{"a@5", "a@5,b@7", "a@5,b@7,c@6", "a@5,b@7,c@6", "", "e@9", "e@9,f@9", ""}
	wantAS := [][2]int{{0, 0}, {0, 0}, {0, 0}, {0, 0}, {2, 1}, {0, 0}, {0, 0}, {1, 1}}
	ok8 := true
	for i, act := range acts {
		a, s, e := act()
		snArr, hArr := make([]string, 3), []string{}
		for r, en := range c.Snapshot("k") {
			snArr[r] = fmt.Sprintf("%s@%d", en.Value, en.Ver)
		}
		for _, h := range c.Hints(1) {
			hArr = append(hArr, fmt.Sprintf("%s@%d", h.Value, h.Ver))
		}
		sn, hn := strings.Join(snArr, "|"), strings.Join(hArr, ",")
		if a != wantAS[i][0] || s != wantAS[i][1] || errors.Is(e, hh.ErrHintOverflow) != (i == 3) ||
			(i != 3 && e != nil) || sn != wantS[i] || hn != wantH[i] {
			ok8 = false
		}
	}
	check("eight steps match incl step4 all-or-nothing and ver9 ties", ok8)
	a, _ := api.New(3, 1)
	a.Down(1)
	a.Write("k", "init", 1)
	before := fmt.Sprint(a.Get("k"))
	distinct := api.ErrConfig != api.ErrVersion && api.ErrVersion != api.ErrKey && api.ErrKey != api.ErrHintOverflow && api.ErrConfig != api.ErrKey
	try := []func() error{
		func() error { _, e := api.New(0, 1); return e },
		func() error { return a.Write("", "x", 1) },
		func() error { return a.Write("k", "x", 0) },
		func() error { return a.Write("k", "x", 2) },
		func() error { _, _, e := a.Up(9); return e },
	}
	wantE := []error{api.ErrConfig, api.ErrKey, api.ErrVersion, api.ErrHintOverflow, api.ErrConfig}
	noTrace := distinct
	for i, fn := range try {
		if !errors.Is(fn(), wantE[i]) || fmt.Sprint(a.Get("k")) != before {
			noTrace = false
		}
	}
	gap, gsk, _ := a.Up(1)
	check("four distinct sentinels; rejects leave no trace, still usable",
		noTrace && gap == 1 && gsk == 0 && a.Write("k", "next", 2) == nil)
	o1 := true
	for _, m := range []int{100, 1000, 10000} {
		buf := hint.NewBuffer(m + 1)
		for i := 0; i < m; i++ {
			buf.Append(hint.Entry{Key: "k", Ver: int64(i + 1)})
		}
		if e := buf.Append(hint.Entry{Ver: int64(m + 1)}); e != nil || buf.Len() != m+1 {
			o1 = false
		}
	}
	check("large-m append exact length (scan O(1) pinned in test)", o1)
	const nK, nW = 16, 5
	cc, _ := api.New(3, nK*nW+1)
	cc.Down(1)
	var regress int32
	stop := make(chan struct{})
	go func() {
		last := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
				v := cc.Get("k0")[0].Ver
				if v < last {
					atomic.StoreInt32(&regress, 1)
				}
				last = v
			}
		}
	}()
	var wg sync.WaitGroup
	wg.Add(nK)
	for i := 0; i < nK; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 1; j <= nW; j++ {
				cc.Write(fmt.Sprintf("k%d", i), fmt.Sprintf("%d:%d", i, j), int64(i*10+j))
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	cap2, csk, _ := cc.Up(1)
	conv := cap2 == nK*nW && csk == 0 && atomic.LoadInt32(&regress) == 0
	for i := 0; i < nK && conv; i++ {
		for _, e := range cc.Get(fmt.Sprintf("k%d", i)) {
			if e.Ver != int64(i*10+nW) || e.Value != fmt.Sprintf("%d:%d", i, nW) {
				conv = false
			}
		}
	}
	check("concurrent writes converge to naive reference; reads monotonic", conv)
	a2, _ := api.New(3, 3)
	check("api.SelfCheck built-in invariant sequence passes", a2.SelfCheck() == nil)
	if fails > 0 {
		panic("demo failed")
	}
}
