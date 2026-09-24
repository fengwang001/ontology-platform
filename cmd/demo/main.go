package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var bad = false

func ok(name string, cond bool) {
	bad = bad || !cond
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[cond], name)
}
func ev(k string, s int64) api.Event { return api.Event{Key: k, Seq: s} }
func recs(rs []api.Record) string {
	s := ""
	for _, r := range rs { // Kind: Applied=0→'+', DeadLettered=1→'x'
		s += fmt.Sprintf("%s%d%c,", r.Key, r.Seq, '+'+byte(r.Kind)*77)
	}
	return strings.TrimSuffix(s, ",")
}
func traceEq(c *api.CDC, ma int, fl map[api.Event]int, cnt map[string]int) bool {
	dl := map[api.Event]bool{}
	for _, e := range c.DeadLetters() {
		dl[e] = true
	}
	n := 0
	for k, c2 := range cnt {
		n += c2
		for s := int64(1); s <= int64(c2); s++ {
			if dl[ev(k, s)] != (fl[ev(k, s)] >= ma) {
				return false
			}
		}
	}
	return len(c.Applied())+len(dl) == n
}
func main() {
	c, _ := api.New(3, 8, map[api.Event]int{ev("A", 2): 1, ev("B", 1): 99, ev("B", 3): 1}) // 1) 11-step
	ops := []api.Event{ev("A", 1), ev("A", 2), ev("B", 1), ev("A", 3), ev("C", 1), ev("B", 2), {}, ev("B", 3), ev("A", 4), {}, {}}
	want := []string{"A1+", "", "", "", "C1+", "", "A2+,A3+", "", "A4+", "B1x,B2+", "B3+"}
	got, match := make([]string, 11), true
	for i, e := range ops {
		if e.Key == "" {
			got[i] = recs(c.Tick())
		} else if rs, err := c.Submit(e); err != nil {
			got[i] = "ERR"
		} else {
			got[i] = recs(rs)
		}
		match = match && got[i] == want[i]
	}
	ok(fmt.Sprintf("11steps[5:%s 7:%s 10:%s]", got[4], got[6], got[9]), match)
	ok(fmt.Sprintf("deadletters:%v", c.DeadLetters()), len(c.DeadLetters()) == 1 && c.DeadLetters()[0] == ev("B", 1)) // 2)
	rng := rand.New(rand.NewSource(7))                                                                                // 3) random vs serial reference
	ma := 1 + rng.Intn(3)
	fl, cnt, all := map[api.Event]int{}, map[string]int{}, []api.Event{}
	left, total := []int{4, 5, 6, 7}, 22
	for total > 0 {
		if i := rng.Intn(4); left[i] > 0 {
			k := string(rune('a' + i))
			e := ev(k, int64(cnt[k]+1))
			cnt[k], left[i], total = cnt[k]+1, left[i]-1, total-1
			all = append(all, e)
			if rng.Intn(3) == 0 {
				fl[e] = rng.Intn(ma + 2)
			}
		}
	}
	c2, _ := api.New(ma, 1000, fl)
	for _, e := range all {
		c2.Submit(e)
		if rng.Intn(2) == 0 {
			c2.Tick()
		}
	}
	for i := 0; i < 3*len(all)+1; i++ {
		c2.Tick()
	}
	ok("random-vs-serial", traceEq(c2, ma, fl, cnt))
	c3, _ := api.New(2, 100, map[api.Event]int{ev("blk", 1): 9}) // 4) isolation
	c3.Submit(ev("blk", 1))
	rs, _ := c3.Submit(ev("free", 1))
	ok("isolation", len(rs) == 1 && rs[0].Kind == api.Applied)
	c4, _ := api.New(3, 1, map[api.Event]int{ev("z", 1): 5}) // 5)+6) errors, no trace
	c4.Submit(ev("z", 1))
	c4.Submit(ev("z", 2))
	_, e1 := c4.Submit(ev("", 1))
	_, e2 := c4.Submit(ev("z", 1))
	_, e3 := c4.Submit(ev("z", 3)) // 3 rejects
	ok("3-error-classes", errors.Is(e1, api.ErrInvalidArgument) && errors.Is(e2, api.ErrSeqNotIncreasing) && errors.Is(e3, api.ErrBufferFull) &&
		!errors.Is(e1, api.ErrSeqNotIncreasing) && !errors.Is(e2, api.ErrBufferFull) && !errors.Is(e3, api.ErrInvalidArgument))
	rs4, _ := c4.Submit(ev("w", 1))
	ok("reject-no-trace", len(c4.Applied()) == 1 && len(c4.DeadLetters()) == 0 && len(c4.Tick()) == 0 && len(rs4) == 1) // no trace
	proxy := true                                                                                                       // 7) Tick touches only blocked keys
	for _, m := range []int{100, 10000} {
		cm, _ := api.New(3, m+10, map[api.Event]int{ev("zz", 1): 5})
		for i := 0; i < m; i++ {
			cm.Submit(ev(fmt.Sprintf("k%05d", i), 1))
		}
		cm.Submit(ev("zz", 1))
		proxy = proxy && len(cm.Tick()) == 0 && len(cm.Applied()) == m
	}
	ok("tick-scans-blocked-only", proxy)
	const N, L = 8, 30 // 8) concurrent per-key
	fl8, cnt8 := map[api.Event]int{}, map[string]int{}
	for i := 0; i < N; i++ {
		k := string(rune('a' + i))
		cnt8[k] = L
		for s := int64(4); s <= L; s += 4 {
			fl8[ev(k, s)] = int(s) % 5
		}
	}
	c8, _ := api.New(3, N*L, fl8)
	var wg sync.WaitGroup
	var stop atomic.Bool
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			for s := int64(1); s <= L; s++ {
				c8.Submit(ev(k, s))
			}
		}(string(rune('a' + i)))
	}
	go func() {
		for !stop.Load() {
			c8.Tick()
		}
	}()
	wg.Wait()
	stop.Store(true)
	for i := 0; i < 3*N*L+1; i++ {
		c8.Tick()
	}
	ok("concurrent-per-key", traceEq(c8, 3, fl8, cnt8))
	c9, _ := api.New(1, 1, nil) // 9) self check
	ok("selfcheck", c9.SelfCheck() == nil)
	os.Exit(map[bool]int{false: 0, true: 1}[bad])
}
