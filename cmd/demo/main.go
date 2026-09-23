// Command demo exercises the deterministic skip list end to end,
// printing one OK/FAIL line per required property.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/iter"
	"ontology/key"
	"ontology/list"
	"ontology/mset"
	"ontology/rank"
)

var failed bool

func ok(name string, cond bool) {
	mark := "OK   "
	if !cond {
		mark, failed = "FAIL ", true
	}
	fmt.Println(mark + name)
}

func build(keys []key.Key) *mset.Mset {
	m := mset.New(32, 1<<20)
	for _, k := range keys {
		_ = m.Insert(k)
	}
	return m
}

func main() {
	base := []key.Key{"a", "b", "c", "d", "e", "b", "d", "b"}
	rev := slices.Clone(base)
	slices.Reverse(rev)
	rot := append(slices.Clone(base[3:]), base[:3]...)
	m0, m1, m2 := build(base), build(rev), build(rot)
	ok("order-independent structure", m0.SameStructure(m1) && m1.SameStructure(m2))
	ok("span self-check", m0.Check() == nil)

	u := build([]key.Key{"k1", "k2", "k3", "k4", "k5", "k6"})
	inv := true
	for i := 0; i < u.Len(); i++ {
		k, err := u.At(i)
		back, err2 := u.At(u.RankOf(k))
		inv = inv && err == nil && err2 == nil && u.RankOf(k) == i && back == k
	}
	ok("at/rankof inverse", inv)

	d := build([]key.Key{"x", "dup", "z", "dup", "dup"})
	r0, c0 := d.RankOf("dup"), d.Count("dup")
	ok("duplicate rank/count/delete", r0 == 0 && c0 == 3 && d.Delete("dup") == nil && d.Count("dup") == 2 && d.RankOf("dup") == 0)

	big := make([]key.Key, 300)
	for i := range big {
		big[i] = key.Key(fmt.Sprintf("k%04d", (i*137)%300))
	}
	mb := build(big)
	for i := 0; i < 300; i += 3 {
		_ = mb.Delete(key.Key(fmt.Sprintf("k%04d", i)))
	}
	ok("spans exact after deletes", mb.Check() == nil)

	n, err := m0.Range("b", "e")
	ok("range equals rank diff", err == nil && n == 6 && n == m0.RankOf("e")-m0.RankOf("b"))

	it := m0.Iterate()
	_, _, _ = it.Next()
	_ = m0.Insert("zz")
	_, _, ierr := it.Next()
	ok("iterator fail-fast on write", errors.Is(ierr, iter.ErrInvalidated))

	_, e1 := m0.At(-1)
	_, e2 := m0.At(m0.Len())
	_, e3 := m0.Range("z", "a")
	e4 := m0.Delete("missing")
	lim := mset.New(4, 2)
	_ = lim.Insert("a")
	_ = lim.Insert("b")
	e5 := lim.Insert("c")
	tall, short := key.Key(""), key.Key("")
	for i := 0; tall == "" || short == ""; i++ {
		if k := key.Key(fmt.Sprintf("t%d", i)); k.Level() > 1 && tall == "" {
			tall = k
		} else if k.Level() == 1 && short == "" {
			short = k
		}
	}
	lm := mset.New(1, 100)
	e6 := lm.Insert(tall)
	ok("detectable errors", errors.Is(e1, rank.ErrOutOfRange) && errors.Is(e2, rank.ErrOutOfRange) &&
		errors.Is(e3, rank.ErrBadRange) && errors.Is(e4, list.ErrNotFound) &&
		errors.Is(e5, list.ErrFull) && errors.Is(e6, list.ErrMaxLevel))
	_ = lim.Delete("a")
	ok("usable after rejection", lim.Insert("c") == nil && lm.Insert(short) == nil && m0.Check() == nil)

	ckeys := make([]key.Key, 5000)
	for i := range ckeys {
		ckeys[i] = key.Key(fmt.Sprintf("k%06d", (i*7919)%5000))
	}
	mc := build(ckeys)
	const g = 8
	got := make([][]key.Key, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < g; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				k, _ := mc.At((j * 13) % mc.Len())
				got[idx] = append(got[idx], k)
				_ = mc.RankOf(k)
				_, _ = mc.Range("k000100", "k000200")
				_ = mc.Count(k)
			}
		}(w)
	}
	close(start)
	wg.Wait()
	same := true
	for w := 1; w < g; w++ {
		same = same && slices.Equal(got[0], got[w])
	}
	ok("concurrent reads identical", same)

	visited := func(size int) int64 {
		l := list.New(32, 1<<20)
		for i := 0; i < size; i++ {
			_ = l.Insert(key.Key(fmt.Sprintf("k%06d", i)))
		}
		r := rank.New(l)
		_, _ = r.At(size / 2)
		v := r.LastVisited()
		_ = r.RankOf(key.Key(fmt.Sprintf("k%06d", size/2)))
		return max(v, r.LastVisited())
	}
	v1, v2 := visited(1000), visited(100000)
	ok(fmt.Sprintf("visited n=1000:%d n=100000:%d <=80", v1, v2), v1 <= 80 && v2 <= 80)

	if failed {
		os.Exit(1)
	}
}
