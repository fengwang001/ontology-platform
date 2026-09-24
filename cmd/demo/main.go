// Command demo exercises the first-write-wins dedup register; prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/fww"
	"ontology/reg"
)

type w = api.Write

var six = []w{
	{Key: "K", Seq: 7, Val: "a"}, {Key: "K", Seq: 3, Val: "b"},
	{Key: "K", Seq: 10, Val: "c"}, {Key: "K", Seq: 1, Val: "d"},
	{Key: "K", Seq: 5, Val: "e"}, {Key: "K", Seq: 8, Val: "f"},
}

func line(pass bool, f string, a ...any) {
	p := "OK "
	if !pass {
		p = "FAIL "
	}
	fmt.Println(p + fmt.Sprintf(f, a...))
}

func tok(ch []api.Change) string {
	var b strings.Builder
	for _, c := range ch {
		fmt.Fprintf(&b, "%c%d%s", map[bool]byte{true: '+', false: '-'}[c.Added], c.Seq, c.Val)
	}
	if b.Len() == 0 {
		b.WriteByte('.')
	}
	return b.String()
}

func regRun() ([]string, reg.Entry, int) {
	r, toks, drops := reg.New(), []string{}, 0
	for _, x := range six {
		o, _ := r.Step(x.Seq, x.Val)
		s := ""
		if o.Had {
			s = fmt.Sprintf("-%d%s", o.Old.Seq, o.Old.Val)
		}
		if o.Added {
			s += fmt.Sprintf("+%d%s", x.Seq, x.Val)
		}
		if o.Dropped {
			s, drops = ".", drops+1
		}
		toks = append(toks, s)
	}
	return toks, r.Current(), drops
}

func main() {
	want := []string{"+7a", "-7a+3b", ".", "-3b+1d", ".", "."}
	rt, eff, rd := regRun() // reg: per-step output, effective value, drops
	line(reflect.DeepEqual(rt, want) && eff == (reg.Entry{Seq: 1, Val: "d"}) && rd == 3,
		"reg six-step %v eff=(1,d) dropped=3", rt)
	r := api.New() // fww+api: same steps; each "-" withdraws exactly the live value
	var log []api.Change
	toks := []string{}
	for _, x := range six {
		ch, _ := r.Feed([]w{x})
		toks, log = append(toks, tok(ch)), append(log, ch...)
	}
	live, wok := map[string]api.Change{}, true
	for _, c := range log {
		if c.Added {
			if _, dup := live[c.Key]; dup {
				wok = false
			}
			live[c.Key] = c
			continue
		}
		cur := live[c.Key]
		if !cur.Added || cur.Seq != c.Seq || cur.Val != c.Val {
			wok = false
		}
		delete(live, c.Key)
	}
	line(reflect.DeepEqual(toks, want) && r.View()["K"] == "d" && r.Dropped() == 3 && wok,
		"fww six-step %v view=K:d dropped=3 withdraw-matches-live", toks)
	ls, lv, lh, l3 := int64(0), "", false, "" // LWW contrast: keeps the largest Seq
	for i, x := range six {
		s := ""
		if !lh || x.Seq > ls {
			if lh {
				s = fmt.Sprintf("-%d%s", ls, lv)
			}
			s += fmt.Sprintf("+%d%s", x.Seq, x.Val)
			ls, lv, lh = x.Seq, x.Val, true
		}
		if i == 2 {
			l3 = s
		}
	}
	line(l3 == "-7a+10c" && ls == 10 && lv == "c", "LWW step3=%s final=(10,c) vs FWW (1,d)", l3)
	cases := []struct {
		write w
		seed  bool
		want  error
	}{
		{w{Key: "", Seq: 1}, false, api.ErrEmptyKey},
		{w{Key: "K", Seq: 0}, false, api.ErrNonPositiveSeq},
		{w{Key: "K", Seq: -3}, false, api.ErrNonPositiveSeq},
		{w{Key: "K", Seq: 1}, true, api.ErrDuplicateSeq},
	}
	eok := api.ErrEmptyKey != api.ErrNonPositiveSeq &&
		api.ErrNonPositiveSeq != api.ErrDuplicateSeq && api.ErrEmptyKey != api.ErrDuplicateSeq
	for _, c := range cases {
		t := api.New()
		if c.seed {
			_, _ = t.Feed([]w{{Key: "K", Seq: 1, Val: "x"}})
		}
		_, err := t.Feed([]w{c.write})
		eok = eok && errors.Is(err, c.want)
	}
	line(eok, "three distinct decidable sentinel errors")
	beforeV, beforeD := r.View()["K"], r.Dropped() // atomic rejection: no trace, still usable
	bad, derr := r.Feed([]w{{Key: "K", Seq: 1, Val: "z"}, {Key: "", Seq: 1}})
	_, uerr := r.Feed([]w{{Key: "new", Seq: 1, Val: "go"}})
	line(derr != nil && bad == nil && uerr == nil && r.View()["K"] == beforeV &&
		r.View()["new"] == "go" && r.Dropped() == beforeD, "rejected batch leaves no trace; still usable")
	line(fww.LookupCheck() == nil && r.SelfCheck() == nil, "lookup O(1) for m=100..10000; SelfCheck passes")
	const n = 16 // concurrent read-only callers, no sleeps
	var wg sync.WaitGroup
	views, dps := make([]map[string]string, n), make([]int64, n)
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); views[i], dps[i] = r.View(), r.Dropped() }(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < n; i++ {
		same = same && reflect.DeepEqual(views[0], views[i]) && dps[0] == dps[i]
	}
	line(same, "%d concurrent readers see identical views", n)
}
