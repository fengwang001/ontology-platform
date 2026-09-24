// Command demo prints one OK/FAIL line per required check; exit 0 iff all OK.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/reb"
	"ontology/ring"
)

var failed bool

func report(ok bool, msg string) {
	s := "OK  "
	if !ok {
		s, failed = "FAIL", true
	}
	fmt.Println(s, msg)
}

func enc(snap []api.Partition) (s string) {
	for _, pt := range snap {
		if pt.Owner < 0 {
			s += "-"
		} else {
			s += string(rune('0' + pt.Owner))
		}
		s += string("UCR"[pt.State])
	}
	return s
}

func checkPkg() {
	var s ring.Set
	s.Add(5)
	s.Add(2)
	ok := true
	for p, want := range []int{2, 2, 2, 5, 5, 5, 2, 2} {
		got, _ := s.Target(p)
		ok = ok && got == want
	}
	report(ok, "ring: target >= boundary + wrap to min")
	r, _ := reb.New(8)
	r.Join(5)
	r.Join(2)
	s1 := enc(r.Snapshot())
	r.RevokeAck(5)
	report(s1 == "5R5R5R5C5C5C5R5R" && enc(r.Snapshot()) == "2C2C2C5C5C5C2C2C",
		"reb: two-round, withheld until ack")
}

func checkTrace() {
	g, _ := api.New(8)
	ops := map[int]func(int) error{0: g.Join, 1: g.Leave, 2: g.RevokeAck}
	seq := []struct{ k, x int }{{0, 5}, {0, 2}, {2, 5}, {0, 4}, {0, 7}, {2, 5}, {1, 4}, {2, 2}}
	want := []string{
		"5C5C5C5C5C5C5C5C", "5R5R5R5C5C5C5R5R", "2C2C2C5C5C5C2C2C", "2C2C2C5R5R5C2C2C",
		"2C2C2C5R5R5C2R2R", "2C2C2C-U-U5C2R2R", "2C2C2C-U-U5C2R2R", "2C2C2C5C5C5C7C7C",
	}
	got := make([]string, 0, 8)
	for _, o := range seq {
		ops[o.k](o.x)
		got = append(got, enc(g.Snapshot()))
	}
	ok := true
	for i := range want {
		ok = ok && got[i] == want[i]
	}
	report(ok, "trace s1-4: "+strings.Join(got[:4], " "))
	report(ok, "trace s5-8: "+strings.Join(got[4:], " "))
	report(got[1] == "5R5R5R5C5C5C5R5R", "step2 revoke set {0,1,2,6,7} incl wrap 6,7")
	report(got[5][6] == '-' && got[5][8] == '-', "step6 no round-2: p3,p4 unowned")
}

func checkErrors() {
	g, _ := api.New(8)
	g.Join(3)
	before := g.Snapshot()
	sentinels := []error{api.ErrBadParam, api.ErrDuplicate, api.ErrNoMember, api.ErrNoRevoke}
	ops := []func() error{func() error { return g.Join(9) }, func() error { return g.Join(3) },
		func() error { return g.Leave(4) }, func() error { return g.RevokeAck(3) }}
	ok, seen := true, map[error]bool{}
	for i, op := range ops {
		err := op()
		ok = ok && errors.Is(err, sentinels[i]) && !seen[sentinels[i]] && slices.Equal(before, g.Snapshot())
		seen[sentinels[i]] = true
	}
	_, errNew := api.New(0)
	report(ok && errors.Is(errNew, api.ErrBadParam) && g.Join(4) == nil, "four decidable errors, no-trace on reject")
}

func checkLargeM() {
	ok := true
	for _, m := range []int{100, 1000, 10000} {
		g, _ := api.New(m)
		g.Join(m - 1)
		g.Join(5)
		g.RevokeAck(m - 1)
		g.Leave(5)
		for _, pt := range g.Snapshot() {
			ok = ok && pt.State == api.Consuming && pt.Owner == m-1
		}
	}
	report(ok, "large-m sequence correct (checked-count bound in reb test)")
}

func checkConcurrent() {
	const P = 64
	g, _ := api.New(P)
	var wg sync.WaitGroup
	for _, op := range []func(int) error{g.Join, g.RevokeAck} {
		for i := 0; i < P/2; i++ {
			wg.Add(1)
			go func(x int) {
				defer wg.Done()
				op(x)
			}(i)
		}
		wg.Wait()
	}
	ok := true
	for p, pt := range g.Snapshot() {
		want := p // members are 0..P/2-1: target is p, wrapping to 0 above
		if p >= P/2 {
			want = 0
		}
		ok = ok && pt.State == api.Consuming && pt.Owner == want
	}
	report(ok, "concurrent join/ack: final == naive")
}

func main() {
	checkPkg()
	checkTrace()
	g, _ := api.New(8)
	report(g.SelfCheck() == nil, "selfcheck: 4 invariants on random seqs")
	checkErrors()
	checkLargeM()
	checkConcurrent()
	if failed {
		os.Exit(1)
	}
}
