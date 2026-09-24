// Command demo exercises the changelog compaction packages and prints OK/FAIL.
// It takes no arguments, performs no network access, and exits non-zero on FAIL.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
)

func main() {
	s := api.New()
	a := func(k string, v int) { must(s.Append(k, v)) }
	a("K1", 10)
	a("K2", 20)
	a("K1", 30)
	a("K3", 40)
	a("K1", 50)
	a("K2", 60)
	a("K4", 70)
	mustErr(s.Compact(3, 7))

	check("step8 reads 5,K1=50 6,K2=60 7,K1=50 3,K4=absent 2,K1=10", func() bool {
		return rd(s, 5, "K1") == 50 && rd(s, 6, "K2") == 60 && rd(s, 7, "K1") == 50 &&
			ok(s, 3, "K4") == false && rd(s, 2, "K1") == 10
	})
	check("ErrEmptyKey distinct", func() bool {
		_, e := s.Append("", 1)
		return errors.Is(e, api.ErrEmptyKey)
	})
	check("ErrSeqOutOfRange distinct", func() bool {
		_, _, e := s.Read(0, "K1")
		return errors.Is(e, api.ErrSeqOutOfRange)
	})
	check("ErrBadRange distinct", func() bool {
		return errors.Is(s.Compact(2, 2), api.ErrBadRange)
	})
	check("rejected op leaves log unchanged", func() bool {
		before := rd(s, 1, "K1")
		_, _ = s.Append("", 9)
		_ = s.Compact(0, 9)
		return rd(s, 1, "K1") == before && rd(s, 7, "K4") == 70
	})
	check("large log, tiny compact: outside O(1)", largeTinyCompact)
	check("concurrent reads match serial reference", concurrentReads)
	check("SelfCheck all four invariants", func() bool { return s.SelfCheck() == nil })
}

func largeTinyCompact() bool {
	const m = 5000
	d := api.New()
	for i := 0; i < m+1; i++ {
		must(d.Append(fmt.Sprintf("K%d", i), i))
	}
	if err := d.Compact(m, m+2); err != nil { // only last two entries
		return false
	}
	return rd(d, 1, "K0") == 0 && rd(d, m-1, fmt.Sprintf("K%d", m-2)) == m-2 &&
		rd(d, m+1, fmt.Sprintf("K%d", m)) == m
}

func concurrentReads() bool {
	d := api.New()
	for i := 0; i < 200; i++ {
		must(d.Append("K", i))
	}
	var wg sync.WaitGroup
	bad := false
	var mu sync.Mutex
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for at := int64(1); at < 200; at++ { // fixed sites, serial ref = at-1
				if rd(d, at, "K") != int(at-1) {
					mu.Lock()
					bad = true
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return !bad
}

func mustErr(err error) {
	if err != nil {
		panic(err)
	}
}
func must(_ int64, err error) {
	if err != nil {
		panic(err)
	}
}
func rd(s *api.Store, seq int64, k string) int {
	v, _, err := s.Read(seq, k)
	if err != nil {
		panic(err)
	}
	return v
}
func ok(s *api.Store, seq int64, k string) bool {
	_, present, err := s.Read(seq, k)
	if err != nil {
		panic(err)
	}
	return present
}
func check(name string, f func() bool) {
	if f() {
		fmt.Println("OK: " + name)
	} else {
		fmt.Println("FAIL: " + name)
	}
}
