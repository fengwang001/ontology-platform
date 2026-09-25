// Command demo exercises the materialized-view staleness detector.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/stale"
)

type snap struct{ a, w, now, hb int64 }
type step struct {
	name string
	rec  any
	tick int64
}

var tg = map[bool]string{true: "OK", false: "FAIL"}

func main() {
	d := stale.NewDetector(5)
	ops := []step{
		{"Data{1,10}", stale.Data{Seq: 1, Val: 10}, -1},
		{"Watermark{4}", stale.Watermark{UpTo: 4}, -1},
		{"Data{2,20}", stale.Data{Seq: 2, Val: 20}, -1},
		{"Data{3,30}", stale.Data{Seq: 3, Val: 30}, -1},
		{"Data{4,40}", stale.Data{Seq: 4, Val: 40}, -1},
		{"Tick(5)", nil, 5}, {"Tick(6)", nil, 6},
		{"Heartbeat{}", stale.Heartbeat{}, -1},
	}
	wantStale := []bool{false, true, true, true, false, false, true, false}
	var sn [8]snap
	bad := false
	for i, o := range ops {
		var err error
		if o.tick >= 0 {
			err = d.Tick(o.tick)
		} else {
			err = d.Feed(o.rec)
		}
		sn[i] = snap{d.Applied(), d.Watermark(), d.Now(), d.LastBeat()}
		ok := err == nil && d.Stale() == wantStale[i]
		bad = bad || !ok
		fmt.Printf("%s %d:%s A=%d W=%d now=%d hb=%d stale=%t\n",
			tg[ok], i+1, o.name, sn[i].a, sn[i].w, sn[i].now, sn[i].hb, d.Stale())
	}
	wLE, wGE, wLag := sn[4].a <= sn[4].w, sn[5].now-sn[5].hb >= 5, sn[6].a < sn[6].w
	boundsOK := wLE && wGE && !wLag && d.View() == 100
	fmt.Printf("%s boundary wrong-bools <=W@5=%t >=to@6=%t lagonly@7=%t viewSum=%t\n",
		tg[boundsOK], wLE, wGE, wLag, d.View() == 100)
	mono := true
	for i := 1; i < 8; i++ {
		mono = mono && sn[i].w >= sn[i-1].w && sn[i].hb >= sn[i-1].hb && sn[i].now >= sn[i-1].now
	}
	errs4, noTrace := rejectionChecks()
	o1, conc := bigMCheck(), concurrentCheck()
	allOK := mono && errs4 && noTrace && o1 && conc && !bad
	fmt.Printf("%s checks monotonic=%t sentinels4=%t noTrace=%t O1-largeM=%t concurrent=%t\n",
		tg[allOK], mono, errs4, noTrace, o1, conc)
	if !allOK {
		os.Exit(1)
	}
}
func rejectionChecks() (bool, bool) {
	s, _ := api.New(5)
	_ = s.Feed(api.Data{Seq: 1, Val: 10})
	_ = s.Feed(api.Watermark{UpTo: 1})
	snapshot := func() [4]int64 {
		return [4]int64{s.Applied(), s.Watermark(), s.View(), map[bool]int64{true: 1}[s.Stale()]}
	}
	ws := []error{api.ErrDataGap, api.ErrWatermarkBacktrack, api.ErrTickBacktrack}
	acts := []func() error{
		func() error { return s.Feed(api.Data{Seq: 5, Val: 1}) },
		func() error { return s.Feed(api.Watermark{UpTo: -1}) },
		func() error { return s.Tick(-1) },
	}
	got := map[error]bool{}
	noTrace := true
	for i := range ws {
		b := snapshot()
		ok := errors.Is(acts[i](), ws[i])
		got[ws[i]] = ok
		noTrace = noTrace && ok && snapshot() == b
	}
	_, e := api.New(0)
	got[api.ErrInvalidTimeout] = errors.Is(e, api.ErrInvalidTimeout)
	distinct := len(got) == 4
	for _, ok := range got {
		distinct = distinct && ok
	}
	return distinct, noTrace
}
func bigMCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		s, _ := api.New(5)
		for i := 1; i <= m; i++ {
			if err := s.Feed(api.Data{Seq: int64(i), Val: 1}); err != nil {
				return false
			}
		}
		if s.Feed(api.Watermark{UpTo: int64(m)}) != nil || s.Tick(int64(m)) != nil ||
			s.Feed(api.Heartbeat{}) != nil {
			return false
		}
		if s.Applied() != int64(m) || s.Watermark() != int64(m) ||
			s.View() != int64(m) || s.Stale() {
			return false
		}
	}
	return true
}
func concurrentCheck() bool {
	s, _ := api.New(5)
	for i := 1; i <= 4; i++ {
		_ = s.Feed(api.Data{Seq: int64(i), Val: int64(i)})
	}
	_ = s.Feed(api.Watermark{UpTo: 4})
	const N = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	got := make([][3]int64, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			for k := 0; k < 64; k++ {
				got[g] = [3]int64{map[bool]int64{true: 1}[s.Stale()], s.Applied(), s.View()}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if got[g] != got[0] {
			return false
		}
	}
	return true
}
