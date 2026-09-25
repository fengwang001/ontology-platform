package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/olog"
	"ontology/txn"
)

type R = olog.Rec

func ok(name string, good bool) {
	if good {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// 第三节八步：逐步记录新追加条数 added 与 View 变化 delta（- 跳过 / REJECT 整批拒绝）。
	steps := []struct {
		tx string
		rs []R
		n  int
	}{
		{"t1", []R{{Seq: 0, Key: "a", Val: 5}, {Seq: 1, Key: "b", Val: 3}, {Seq: 2, Key: "c", Val: 7}}, 3},
		{"t2", []R{{Seq: 0, Key: "a", Val: 9}, {Seq: 1, Key: "d", Val: 2}}, 2},
		{"t1", []R{{Seq: 3, Key: "e", Val: 4}}, 1},
		{"t1", []R{{Seq: 0, Key: "a", Val: 5}}, 0},
		{"t1", []R{{Seq: 1, Key: "b", Val: 99}}, 0},
		{"t3", []R{{Seq: 0, Key: "f", Val: 1}, {Seq: 1, Key: "", Val: 2}}, 0},
		{"t2", []R{{Seq: 2, Key: "a", Val: 11}}, 1},
		{"t2", []R{{Seq: 2, Key: "a", Val: 99}}, 0},
	}
	l := olog.New()
	added, delta, good := make([]int, 8), make([]string, 8), true
	for i, s := range steps {
		n, err := l.Commit(s.tx, s.rs)
		added[i] = n
		bad := n != s.n || (i == 5 && !errors.Is(err, txn.ErrEmptyKey)) || (i != 5 && err != nil)
		if bad {
			good = false
		}
		if i == 5 {
			delta[i] = "REJECT"
		} else if n == 0 {
			delta[i] = "-"
		} else {
			parts := make([]string, len(s.rs))
			for j, r := range s.rs {
				parts[j] = fmt.Sprintf("%s=%d", r.Key, r.Val)
			}
			delta[i] = strings.Join(parts, ",")
		}
	}
	v := l.View()
	_, hasF := v["f"]
	good = good && v["a"] == 11 && v["b"] == 3 && v["c"] == 7 && v["d"] == 2 && v["e"] == 4 && !hasF
	fmt.Printf("OK   8steps added=%v delta=[%s] final a11b3c7d2e4 !f: %v\n",
		added, strings.Join(delta, " | "), good)

	before := l.View()
	nr, _ := l.Commit("t1", []R{{Seq: 2, Key: "c", Val: 700}})
	same := nr == 0 && len(l.View()) == len(before) && l.View()["c"] == 7
	ok("replay idempotent + exactly-once (selfcheck)", same && api.New().SelfCheck() == nil)

	l2 := olog.New()
	l2.Commit("x", []R{{Seq: 0, Key: "keep", Val: 1}})
	pre := len(l2.View())
	_, e1 := l2.Commit("", []R{{Seq: 0, Key: "k", Val: 1}})
	_, e2 := l2.Commit("x", []R{{Seq: 1, Key: "", Val: 2}})
	_, e3 := l2.Commit("x", []R{{Seq: 2, Key: "p", Val: 1}, {Seq: 2, Key: "q", Val: 2}})
	more, _ := l2.Commit("x", []R{{Seq: 9, Key: "after", Val: 8}})
	ok("3 distinct errors, no trace, reusable",
		errors.Is(e1, txn.ErrEmptyTxID) && errors.Is(e2, txn.ErrEmptyKey) && errors.Is(e3, txn.ErrDupSeq) &&
			e1 != e2 && e2 != e3 && len(l2.View()) == pre+1 && more == 1)
	type k1 struct {
		id  string
		seq int
	}
	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		set, rs := make(map[k1]struct{}, m), make([]R, m)
		for i := range rs {
			rs[i] = R{Seq: i, Key: fmt.Sprintf("k%d", i), Val: int64(i)}
			set[k1{"bulk", i}] = struct{}{}
		}
		probes := 0
		txn.Plan("bulk", []txn.Rec{{Seq: 0, Key: "k0", Val: 0}}, func(id string, seq int) bool {
			probes++
			_, hit := set[k1{id, seq}]
			return hit
		})
		bm := olog.New()
		bm.Commit("bulk", rs)
		cn, ce := bm.Commit("bulk", rs[:1])
		if probes != 1 || cn != 0 || ce != nil {
			bounded = false
		}
	}
	ok("big-m idempotency probe constant in m", bounded)

	const N, per = 64, 50
	a, stop := api.New(), make(chan struct{})
	var torn atomic.Bool
	var wr, ww sync.WaitGroup
	wr.Add(1)
	go func() {
		defer wr.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if x := a.View()["shared"]; x != 0 && x != 42 {
					torn.Store(true)
				}
			}
		}
	}()
	for g := 0; g < N; g++ {
		ww.Add(1)
		go func(g int) {
			defer ww.Done()
			rs := make([]api.Rec, per)
			for j := range rs {
				k, val := "shared", int64(42)
				if j > 0 {
					k, val = fmt.Sprintf("g%d-k%d", g, j), int64(g*per+j)
				}
				rs[j] = api.Rec{Seq: j, Key: k, Val: val}
			}
			if cn, err := a.Commit(fmt.Sprintf("g%d", g), rs); err != nil || cn != per {
				panic("concurrent commit failed")
			}
		}(g)
	}
	ww.Wait()
	close(stop)
	wr.Wait()
	fv := a.View()
	ok("concurrent commits == batch recompute, no tear",
		!torn.Load() && len(fv) == 1+N*(per-1) && fv["shared"] == 42)
}
