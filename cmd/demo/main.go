package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/buf"
	"ontology/replay"
)

var seq = []buf.Change{
	{Op: buf.Set, Key: "a", Val: "1"}, {Op: buf.Set, Key: "b", Val: "2"}, {Op: buf.Set, Key: "c", Val: "3"}, {Op: buf.Del, Key: "b"},
	{Op: buf.Set, Key: "d", Val: "4"}, {Op: buf.Set, Key: "a", Val: "9"}, {Op: buf.Del, Key: "c"}, {Op: buf.Set, Key: "e", Val: "5"},
}

func steps() (string, bool) {
	b, ok, out := buf.NewBuffer(3), true, ""
	enc := func(cs []buf.Change) string {
		p := make([]string, len(cs))
		for i, c := range cs {
			if c.Op == buf.Del {
				p[i] = "D" + c.Key
			} else {
				p[i] = c.Key + "=" + c.Val
			}
		}
		return strings.Join(p, " ")
	}
	want := []string{"[a=1]", "[a=1 b=2]", "[a=1 b=2 c=3]", "[Db]|^[a=1 b=2 c=3]", "[Db d=4]", "[Db d=4 a=9]", "[Dc]|^[Db d=4 a=9]", "[Dc e=5]"}
	for i, m := range seq {
		blk, s := b.Append(m), "["+enc(b.Pending())+"]"
		if blk != nil {
			s += "|^[" + enc(blk) + "]"
		}
		ok, out = ok && s == want[i], out+fmt.Sprintf(" %d%s", i+1, s)
	}
	return out, ok
}
func replayLine() (string, bool) {
	b := buf.NewBuffer(3)
	var bs [][]buf.Change
	for _, m := range seq {
		if blk := b.Append(m); blk != nil {
			bs = append(bs, blk)
		}
	}
	tail := b.Pending()
	play := func(blocks [][]buf.Change, tl []buf.Change) map[string]string {
		st := replay.New()
		for _, x := range blocks {
			st.Spill(x)
		}
		m := map[string]string{}
		for _, kv := range st.Replay(tl) {
			m[kv.Key] = kv.Val
		}
		return m
	}
	got := play(bs, tail)
	rev := append([][]buf.Change(nil), bs...)
	slices.Reverse(rev)
	lifo := play(append([][]buf.Change{tail}, rev...), nil) // trap 甲: tail then blocks newest->oldest
	setOnly := make([][]buf.Change, len(bs))
	for i, x := range bs { // trap 乙: Del dropped only inside spilled blocks
		setOnly[i] = slices.DeleteFunc(append([]buf.Change(nil), x...), func(c buf.Change) bool { return c.Op != buf.Set })
	}
	drop := play(setOnly, tail)
	zero := map[string]string{"a": got["a"], "d": got["d"], "e": got["e"], "b": "", "c": ""} // trap 丙
	_, hb := zero["b"]
	_, dc := drop["c"]
	ok := len(got) == 3 && got["a"] == "9" && got["d"] == "4" && got["e"] == "5" && len(bs) == 2
	ok = ok && lifo["a"] == "1" && lifo["b"] == "2" && lifo["c"] == "3"
	ok = ok && drop["b"] == "2" && !dc && hb && zero["b"] == "" && zero["c"] == ""
	return fmt.Sprintf("view=[a=9 d=4 e=5] LIFO(a,b,c=1,2,3) dropDel(b=2) delAsZero(b,c=empty,exists=%v)", hb), ok
}
func errorsOK() bool {
	_, e0 := api.New(0)
	live, _ := api.New(2)
	e1, e2 := live.Mutate(api.Op(9), "z", "1"), live.Mutate(api.Set, "", "1")
	ok := errors.Is(e0, api.ErrBadLimit) && errors.Is(e1, api.ErrInvalidOp) && errors.Is(e2, api.ErrEmptyKey)
	ok = ok && e0 != e1 && e1 != e2 && e0 != e2 && live.Mutate(api.Set, "y", "2") == nil
	done, _ := api.New(2)
	done.Mutate(api.Set, "a", "1")
	done.Commit()
	n, nv := done.Spilled(), len(done.View())
	ok = ok && errors.Is(done.Mutate(api.Set, "z", "1"), api.ErrClosed)
	return ok && done.Spilled() == n && len(done.View()) == nv && done.SelfCheck() == nil
}
func probesOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		b, st := buf.NewBuffer(8), replay.New()
		for i := 0; i < m; i++ {
			if blk := b.Append(buf.Change{Op: buf.Set, Key: fmt.Sprintf("k%05d", i), Val: "v"}); blk != nil {
				st.Spill(blk)
			}
		}
		if st.Replay(b.Pending()); !st.ProbesBounded() { // bool only; number hidden
			return false
		}
	}
	return true
}
func concOK() bool {
	tx, _ := api.New(4)
	for i := 0; i < 50; i++ {
		tx.Mutate(api.Set, fmt.Sprintf("k%02d", i), "v")
	}
	tx.Commit()
	fp := func() string {
		var b strings.Builder
		for _, kv := range tx.View() {
			b.WriteString(kv.Key + "=" + kv.Val + ",")
		}
		v, _ := tx.Get("k00")
		fmt.Fprintf(&b, "%s%d", v, tx.Spilled())
		return b.String()
	}
	want := fp()
	const N = 16
	var wg sync.WaitGroup
	res, start := make([]string, N), make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); <-start; res[g] = fp() }(g)
	}
	close(start)
	wg.Wait()
	for i := 1; i < N; i++ {
		if res[i] != want {
			return false
		}
	}
	return true
}
func main() {
	s1, ok1 := steps()
	s2, ok2 := replayLine()
	tag := map[bool]string{true: "OK", false: "FAIL"}
	fmt.Printf("%s steps:%s\n", tag[ok1], s1)
	fmt.Printf("%s replay: %s\n", tag[ok2], s2)
	fmt.Printf("%s four-distinct-errors + no-trace + SelfCheck\n", tag[errorsOK()])
	fmt.Printf("%s probes-bounded m=100,1000,10000\n", tag[probesOK()])
	fmt.Printf("%s concurrent-readers identical (N=16)\n", tag[concOK()])
}
