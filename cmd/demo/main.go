// Command demo exercises hinted handoff and prints OK/FAIL lines (<=10).
package main

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/hint"
	"os"
	"strconv"
	"strings"
	"sync"
)

var fails int

func report(name, detail string, ok bool) {
	tag := "OK"
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Printf("%s %s: %s\n", tag, name, detail)
}
func main() {
	s, _ := api.New(3, 3)
	_ = s.Down(1)
	E := func(i int) string {
		if e := s.Get("k")[i]; e.Ver != 0 {
			return e.Value + "@" + strconv.FormatInt(e.Ver, 10)
		}
		return "-"
	}
	H := func() string {
		b := "["
		for _, e := range s.Hints(1) {
			b += e.Value + strconv.FormatInt(e.Ver, 10) + ","
		}
		return strings.TrimRight(b, ",") + "]"
	}
	row := func(res string) string {
		return fmt.Sprintf("%s R0=%s R1=%s R2=%s h=%s", res, E(0), E(1), E(2), H())
	}
	ops := []struct {
		v  string
		n  int64
		on string
	}{{"a", 5, "a@5"}, {"b", 7, "b@7"}, {"c", 6, "b@7"}, {"d", 8, "b@7"}}
	var t1 strings.Builder
	ok8 := true
	for i, o := range ops {
		err := s.Write("k", o.v, o.n)
		res := "write"
		if errors.Is(err, api.ErrHintOverflow) {
			res = "REJECT"
		}
		ok8 = ok8 && (err == nil || errors.Is(err, api.ErrHintOverflow)) && E(0) == o.on && E(2) == o.on
		t1.WriteString(row(res))
		if i < 3 {
			t1.WriteString(" || ")
		}
	}
	report("steps1-4", t1.String(), ok8)
	a, sk, _ := s.Up(1)
	ok8 = ok8 && a == 2 && sk == 1
	_ = s.Down(1)
	_ = s.Write("k", "e", 9)
	r6 := "write e9 " + row("")
	_ = s.Write("k", "f", 9)
	r7 := "write f9 tie: online e@9 h=" + H()
	a, sk, _ = s.Up(1)
	ok8 = ok8 && a == 1 && sk == 1 && E(1) == "e@9" && E(2) == "e@9"
	report("steps5-8", "Up a=2 s=1(stale c6)->b@7 || "+r6+" || "+r7+
		fmt.Sprintf(" || Up a=%d s=%d(tie f9)->all %s", a, sk, E(0)), ok8)
	d, _ := api.New(2, 2)
	_, ecfg := api.New(0, 1)
	ever, ekey := d.Write("k", "v", 0), d.Write("", "v", 1)
	_ = d.Down(0)
	_ = d.Write("k", "v1", 1)
	_ = d.Write("k", "v2", 2)
	eovf := d.Write("k", "v3", 3)
	distinct := map[error]bool{ecfg: true, ever: true, ekey: true, eovf: true}
	badRange := errors.Is(d.Down(-1), api.ErrBadConfig)
	notrace := len(d.Hints(0)) == 2 && d.Get("k")[1].Ver == 2
	ad, _, _ := d.Up(0)
	usable := ad == 2 && d.Get("k")[0].Value == "v2"
	report("4-errors", "config/version/key/overflow distinct; range=config", len(distinct) == 4 && badRange)
	report("no-trace", "overflow changed nothing; replica still recovers to v2@2", notrace && usable)
	report("append-O(1)", "depths 100/1000/10000, dedup scan stays 0", hint.O1AppendCheck())
	c, _ := api.New(3, 100000)
	_ = c.Down(1)
	const G, W = 40, 25
	stop := make(chan struct{})
	mono := true
	var rd sync.WaitGroup
	rd.Add(1) // Getter must never observe a version going backwards.
	go func() {
		defer rd.Done()
		last := make([]int64, G)
		for {
			select {
			case <-stop:
				return
			default:
				for g := 0; g < G; g++ {
					if v := c.Get("key" + strconv.Itoa(g))[0].Ver; v < last[g] {
						mono = false
					} else {
						last[g] = v
					}
				}
			}
		}
	}()
	var wr sync.WaitGroup
	for g := 0; g < G; g++ {
		k := "key" + strconv.Itoa(g)
		wr.Add(1)
		go func(g int) {
			defer wr.Done()
			for j := 1; j <= W; j++ {
				_ = c.Write(k, "v"+strconv.Itoa(j), int64(g*1000+j))
			}
		}(g)
	}
	wr.Wait()
	close(stop)
	rd.Wait()
	au, _, _ := c.Up(1)
	conv := true
	for g := 0; g < G; g++ {
		for _, e := range c.Get("key" + strconv.Itoa(g)) {
			if e.Ver != int64(g*1000+W) || e.Value != "v"+strconv.Itoa(W) {
				conv = false
			}
		}
	}
	report("concurrent", fmt.Sprintf("%d keys x%d, replay applied=%d/%d, Gets monotonic",
		G, W, au, G*W), conv && mono && au == G*W && len(c.Hints(1)) == 0)
	if err := s.SelfCheck(); err != nil {
		report("selfcheck", err.Error(), false)
	} else {
		report("selfcheck", "four invariants on the built-in sequence", true)
	}
	if fails > 0 {
		fmt.Println("FAIL demo")
		os.Exit(1)
	}
}
