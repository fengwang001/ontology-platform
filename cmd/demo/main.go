// Command demo exercises the stream compaction packages.
package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/ev"
	"ontology/fold"
)

func line(ok bool, msg string) {
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[ok] + msg)
}

func main() {
	I, R := ev.OpInsert, ev.OpRetract
	s := []ev.Event{
		{Key: "g", Val: 7, Op: I}, {Key: "g", Val: 7, Op: I},
		{Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: R},
		{Key: "g", Val: 7, Op: I}, {Key: "g", Val: 3, Op: I},
	}
	// gfmt renders group g; this demo's streams carry only 3 and 7.
	gfmt := func(st fold.State) string {
		p := []string{}
		for _, v := range []int64{3, 7} {
			if st["g"][v] > 0 {
				p = append(p, fmt.Sprintf("%d:%d", v, st["g"][v]))
			}
		}
		return "{" + strings.Join(p, ",") + "}"
	}
	steps := []string{}
	for i := range s {
		steps = append(steps, gfmt(fold.Terminal(nil, s[:i+1])))
	}
	line(strings.Join(steps, " ") == "{7:1} {7:2} {7:1} {} {7:1} {3:1,7:1}", "六步流逐步多重集 "+strings.Join(steps, " "))
	c, _ := fold.Compact(s)
	line(reflect.DeepEqual(fold.Terminal(nil, s), fold.Terminal(nil, c)), "压实流与原流终态相同")
	line(len(c) <= len(s), "压实后长度不增")
	c2, _ := fold.Compact(c)
	line(reflect.DeepEqual(c, c2), "Compact 幂等")
	ri := []ev.Event{{Key: "g", Val: 7, Op: R}, {Key: "g", Val: 7, Op: I}}
	cri, _ := fold.Compact(ri)
	line(reflect.DeepEqual(cri, ri), "Retract紧跟Insert 在I=空 上不可消")
	rng, okR := rand.New(rand.NewSource(42)), true
	for i := 0; i < 200; i++ {
		t := fold.GenLegal(rng, 1+rng.Intn(50))
		cs, _ := fold.Compact(t)
		if !reflect.DeepEqual(fold.Terminal(nil, t), fold.Terminal(nil, cs)) {
			okR = false
		}
	}
	line(okR, "随机流全量重放逐组一致")
	cp := api.New(2)
	_, e1 := cp.Replay(nil, []ev.Event{{Key: "g", Val: 1, Op: I}, {Key: "g", Val: 2, Op: I}, {Key: "g", Val: 3, Op: I}})
	_, e2 := cp.Replay(nil, []ev.Event{{Key: "", Val: 1, Op: I}})
	_, e3 := cp.Replay(nil, []ev.Event{{Key: "g", Val: 1, Op: R}})
	line(e1 == api.ErrTooLong && e2 == ev.ErrInvalidEvent && e3 == api.ErrIllegalRetract, "三类哨兵错误互不相同")
	st, e4 := cp.Replay(nil, []ev.Event{{Key: "g", Val: 1, Op: I}})
	line(e4 == nil && reflect.DeepEqual(st, fold.State{"g": {1: 1}}), "被拒后状态不变仍可用")
	okB := true
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		fold.Compact(fold.GenLegal(rand.New(rand.NewSource(1)), n))
		okB = okB && fold.Compared() <= 2*int64(n)
	}
	line(okB, "长流比较次数 <= c*n")
	var wg sync.WaitGroup
	res := make([][]ev.Event, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], _ = fold.Compact(s) }(g)
	}
	wg.Wait()
	okC := true
	for g := 1; g < 8; g++ {
		okC = okC && reflect.DeepEqual(res[0], res[g])
	}
	line(okC, "并发压实逐事件相同")
}
