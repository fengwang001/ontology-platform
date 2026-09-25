package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/olog"
	"ontology/txn"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func rec(s int, k string, v int64) api.Rec { return api.Rec{Seq: s, Key: k, Val: v} }

func eq(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func main() {
	// 1) 八步：每步新追加条数与 View 变化。
	s := api.New()
	type step struct {
		tx string
		rs []api.Rec
		n  int
	}
	steps := []step{
		{"t1", []api.Rec{rec(0, "a", 5), rec(1, "b", 3), rec(2, "c", 7)}, 3},
		{"t2", []api.Rec{rec(0, "a", 9), rec(1, "d", 2)}, 2},
		{"t1", []api.Rec{rec(3, "e", 4)}, 1},
		{"t1", []api.Rec{rec(0, "a", 5)}, 0},
		{"t1", []api.Rec{rec(1, "b", 99)}, 0},
		{"t3", []api.Rec{rec(0, "f", 1), rec(1, "", 2)}, 0},
		{"t2", []api.Rec{rec(2, "a", 11)}, 1},
		{"t2", []api.Rec{rec(2, "a", 99)}, 0},
	}
	got := make([]int, len(steps))
	okCounts, step6Rej := true, true
	for i, st := range steps {
		n, err := s.Commit(st.tx, st.rs)
		got[i] = n
		if i == 5 {
			step6Rej = errors.Is(err, txn.ErrEmptyKey)
		} else if err != nil {
			okCounts = false
		}
		okCounts = okCounts && n == st.n
	}
	check(fmt.Sprintf("eight steps appended=%v step6 rejected=%v final=%v", got, step6Rej, s.View()),
		okCounts && step6Rej && eq(s.View(), map[string]int64{"a": 11, "b": 3, "c": 7, "d": 2, "e": 4}))

	// 2) 重放幂等 + 3) (txID,Seq) 精确一次、首次提交者胜出。
	before := s.View()
	n1, _ := s.Commit("t1", []api.Rec{rec(0, "a", 5), rec(1, "b", 99)})
	noDup := n1 == 0
	for i := 0; i < 50; i++ {
		if n, _ := s.Commit("t1", []api.Rec{rec(2, "c", 7)}); n != 0 {
			noDup = false
		}
	}
	check("replay no-op; (txID,Seq) exactly-once, first writer wins", noDup && eq(s.View(), before) && before["b"] == 3)

	// 4) 三类可判定错误互不相同；5) 被拒后不留痕且仍可继续。
	s2 := api.New()
	s2.Commit("x", []api.Rec{rec(0, "k", 1)})
	pre := s2.View()
	_, e1 := s2.Commit("", []api.Rec{rec(0, "k", 1)})
	_, e2 := s2.Commit("x", []api.Rec{rec(1, "", 1)})
	_, e3 := s2.Commit("x", []api.Rec{rec(2, "k", 1), rec(2, "j", 2)})
	untraced := eq(s2.View(), pre)
	nAfter, errAfter := s2.Commit("x", []api.Rec{rec(1, "z", 8)})
	check("three distinct errors, rejected batch leaves no trace",
		errors.Is(e1, txn.ErrEmptyTxID) && errors.Is(e2, txn.ErrEmptyKey) && errors.Is(e3, txn.ErrDuplicateSeq) &&
			e1 != e2 && e2 != e3 && e1 != e3 && untraced && nAfter == 1 && errAfter == nil && s2.View()["z"] == 8)

	// 6) 大 m 下幂等判定不随 m 增长（只拿成败，读不到计数）。
	check("replay dedup lookup stays constant as m grows", olog.CheckReplayLookup() == nil)

	// 7) 并发提交后等于批量重算，并发读无撕裂。
	c := api.New()
	const N, per = 20, 50
	var writing atomic.Bool
	writing.Store(true)
	var rg sync.WaitGroup
	for r := 0; r < 4; r++ {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for writing.Load() {
				v := c.View()
				if len(v)%per != 0 {
					check("torn view", false)
				}
			}
		}()
	}
	want := map[string]int64{}
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			b := make([]api.Rec, per)
			for i := range b {
				b[i] = rec(i, fmt.Sprintf("g%d-k%d", g, i), int64(g*per+i))
			}
			c.Commit(fmt.Sprintf("tx%d", g), b)
		}(g)
		for i := 0; i < per; i++ {
			want[fmt.Sprintf("g%d-k%d", g, i)] = int64(g*per + i)
		}
	}
	wg.Wait()
	writing.Store(false)
	rg.Wait()
	check("concurrent commits equal batch recompute, no torn view", eq(c.View(), want) && c.SelfCheck() == nil)

	if failed {
		fmt.Println("DEMO FAILED")
		os.Exit(1)
	}
}
