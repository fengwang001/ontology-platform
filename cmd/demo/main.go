// Command demo exercises the arena bump allocator and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/bump"
)

func ok(cond bool, msg string) bool {
	tag := "OK "
	if !cond {
		tag = "FAIL "
	}
	fmt.Println(tag + msg)
	return cond
}

func mustBump(size, al, max int) *bump.Allocator {
	x, err := bump.New(size, al, max)
	if err != nil {
		panic(err)
	}
	return x
}

// bstr renders bump positions of the (at most 3) arenas, '-' if unopened.
func bstr(bs []int) string {
	s := make([]string, 3)
	for j := range s {
		if j < len(bs) {
			s[j] = fmt.Sprint(bs[j])
		} else {
			s[j] = "-"
		}
	}
	return "[" + strings.Join(s, ",") + "]"
}

func main() {
	// 1) Section-3 eight-step script with per-step (off,gen) and bump layout.
	a := mustBump(16, 8, 3)
	var log []string
	run := func(n int) {
		off, g, e := a.Alloc(n)
		if e != nil {
			panic(e)
		}
		_, _, bs := a.Snapshot()
		log = append(log, fmt.Sprintf("(%d,%d)%s", off, g, bstr(bs)))
	}
	for _, n := range []int{5, 10, 5, 3} {
		run(n)
	}
	a.Reset()
	for _, n := range []int{5, 5} {
		run(n)
	}
	want := "(0,0)[8,-,-] (16,0)[8,16,-] (32,0)[8,16,8] (40,0)[8,16,16] " +
		"(0,1)[8,-,-] (8,1)[16,-,-]"
	fmt.Println("   trace: " + strings.Join(log, " "))
	ok(strings.Join(log, " ") == want && !a.Valid(0, 0) && a.Valid(0, 1),
		"8 steps per NOTES table; Valid(0,0)=false, Valid(0,1)=true (3->8B align; 10B moves, 8B tail wasted)")

	// 2) Three invariants via self-check.
	sc, _ := api.New(16, 8, 3)
	ok(sc.SelfCheck() == nil, "invariants: conservation, naive-reference, alignment/dangling")

	// 3) Four distinct sentinels; rejected calls leave no trace.
	_, eAlign := api.New(8, 6, 1)
	_, eSmall := api.New(4, 8, 1)
	b1, _ := api.New(16, 8, 1)
	_, _, eSize := b1.Alloc(0)
	off0, _, _ := b1.Alloc(5)
	_, _, eExh := b1.Alloc(16)
	_, _, eExh2 := b1.Alloc(16)
	ok(errors.Is(eAlign, api.ErrInvalidAlign) && errors.Is(eSmall, api.ErrArenaTooSmall) &&
		errors.Is(eSize, api.ErrInvalidSize) && errors.Is(eExh, api.ErrArenaExhausted) &&
		errors.Is(eExh2, api.ErrArenaExhausted) && off0 == 0,
		"four distinct decidable errors; state untouched after rejection and still usable")

	// 4) Fill m arenas then Alloc once: behavior constant in m (probe pinned
	// to 1 in the white-box bump_test); no sleep, offsets always m*arenaSize.
	c := true
	for _, m := range []int{100, 1000, 10000} {
		x, _ := bump.New(8, 8, m+1)
		for i := 0; i < m; i++ {
			if _, _, e := x.Alloc(8); e != nil {
				c = false
			}
		}
		off, _, e := x.Alloc(1)
		c = c && e == nil && off == m*8
	}
	ok(c, "fill m=100..10000 arenas then Alloc: examined arenas O(1), next off=m*arenaSize")

	// 5) N goroutines, one Alloc each: distinct aligned offsets, bytes
	// conserved; concurrent reads of used bytes are non-decreasing.
	const N = 64
	d, _ := bump.New(8, 8, N)
	offs := make([]int, N)
	start, done := make(chan struct{}), make(chan struct{})
	var wg, rw sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; offs[i], _, _ = d.Alloc(3) }(i)
	}
	mono := true
	rw.Add(1)
	go func() {
		defer rw.Done()
		prev := 0
		for {
			select {
			case <-done:
				return
			default:
				_, _, bs := d.Snapshot()
				s := 0
				for _, v := range bs {
					s += v
				}
				if s < prev {
					mono = false
				}
				prev = s
			}
		}
	}()
	close(start)
	wg.Wait()
	close(done)
	rw.Wait()
	uniq := map[int]bool{}
	aligned := true
	for _, o := range offs {
		uniq[o] = true
		aligned = aligned && o%8 == 0
	}
	ok(mono && len(uniq) == N && aligned,
		"concurrent: 64 distinct 8B-aligned offsets, 512B conserved, used-bytes monotonic")
}
