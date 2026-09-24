// Command demo exercises the MVCC read-view and reclaimer semantics.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"ontology/store"
)

var failures int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func put(s *store.Store, key, val string) {
	t := s.Begin()
	if err := t.Write(key, []byte(val)); err != nil {
		panic(err)
	}
	if err := t.Commit(); err != nil {
		panic(err)
	}
}

func read(s *store.Store, key string) (store.State, []byte) {
	snap, err := s.OpenSnapshot()
	if err != nil {
		panic(err)
	}
	defer snap.Close()
	return s.Get(snap, key)
}

func buildHot(n int) *store.Store {
	s := store.New(store.Options{})
	for i := 0; i < n; i++ {
		put(s, fmt.Sprintf("cold-%d", i), "x")
	}
	for v := 0; v < 10; v++ {
		for h := 0; h < 5; h++ {
			put(s, fmt.Sprintf("hot-%d", h), fmt.Sprintf("v%d", v))
		}
	}
	return s
}

func crashCommit(t *store.Tx) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	_ = t.Commit()
	return false
}

func main() {
	// 1. Snapshot isolation and repeatable reads.
	s1 := store.New(store.Options{})
	put(s1, "k", "v1")
	snap, _ := s1.OpenSnapshot()
	put(s1, "k", "v2")
	st, b1 := s1.Get(snap, "k")
	_, b2 := s1.Get(snap, "k")
	check("snapshot isolation + repeatable read", st == store.Present && string(b1) == "v1" && bytes.Equal(b1, b2))
	snap.Close()

	// 2. Uncommitted invisible to others, read-own-writes.
	s2 := store.New(store.Options{})
	put(s2, "k", "base")
	tx := s2.Begin()
	_ = tx.Write("k", []byte("mine"))
	_, other := read(s2, "k")
	stOwn, own := tx.Get("k")
	check("uncommitted invisible + read-own-writes", string(other) == "base" && stOwn == store.Present && string(own) == "mine")

	// 3. Rollback leaves no residue.
	s3 := store.New(store.Options{})
	tx3 := s3.Begin()
	_ = tx3.Write("gone", []byte("x"))
	_ = tx3.Rollback()
	st3, _ := read(s3, "gone")
	check("rollback leaves no residue", st3 == store.NeverExisted && s3.Stats().TotalVersions == 0 && s3.KeyStats("gone") == 0)

	// 4. Deleted vs never existed are distinguishable.
	s4 := store.New(store.Options{})
	put(s4, "d", "v")
	td := s4.Begin()
	_ = td.Delete("d")
	_ = td.Commit()
	stDel, _ := read(s4, "d")
	stNever, _ := read(s4, "nope")
	check("deleted != never existed", stDel == store.Deleted && stNever == store.NeverExisted)

	// 5. Commit exactly at the snapshot point is invisible.
	s5 := store.New(store.Options{})
	snap5, _ := s5.OpenSnapshot()
	put(s5, "k", "after")
	st5, _ := s5.Get(snap5, "k")
	check("commit at snapshot point invisible", st5 == store.NeverExisted)
	snap5.Close()

	// 6. Reclaim never changes an active snapshot's reads.
	s6 := store.New(store.Options{})
	put(s6, "k", "old")
	snap6, _ := s6.OpenSnapshot()
	put(s6, "k", "new")
	_, before := s6.Get(snap6, "k")
	s6.Reclaim()
	_, after := s6.Get(snap6, "k")
	check("reclaim keeps active snapshot reads", bytes.Equal(before, after) && string(after) == "old")
	snap6.Close()

	// 7. Incremental reclaim: examined count independent of N.
	e100 := buildHot(100).Reclaim()
	e10000 := buildHot(10000).Reclaim()
	check(fmt.Sprintf("incremental reclaim examined N=100:%d N=10000:%d", e100, e10000), e100 == e10000)

	// 8. Watermark never decreases.
	s8 := buildHot(10)
	snap8, _ := s8.OpenSnapshot()
	s8.Reclaim()
	w1 := s8.Stats().Watermark
	snap8.Close()
	put(s8, "hot-0", "extra")
	s8.Reclaim()
	w2 := s8.Stats().Watermark
	check("watermark never decreases", w2 >= w1 && w2.Valid())

	// 9. Every crash point is safe.
	safe := true
	for p := 0; p <= 3; p++ {
		s9 := store.New(store.Options{CrashHook: func(stage int) {
			if stage == p {
				panic("crash")
			}
		}})
		t9 := s9.Begin()
		_ = t9.Write("k", []byte("v"))
		crashCommit(t9)
		s9.Recover()
		st9, _ := read(s9, "k")
		if p < 3 {
			safe = safe && st9 == store.NeverExisted && s9.Stats().TotalVersions == 0 && s9.KeyStats("k") == 0
		} else {
			safe = safe && st9 == store.Present && s9.Stats().TotalVersions == 1
		}
	}
	check("all crash points safe", safe)

	// 10. Three distinguishable limit errors, state unchanged on reject.
	sa := store.New(store.Options{MaxChainLen: 1})
	put(sa, "k", "v")
	errChain := sa.Begin().Write("k", []byte("x"))
	sb := store.New(store.Options{MaxSnapshots: 1})
	keep, _ := sb.OpenSnapshot()
	_, errSnap := sb.OpenSnapshot()
	sc := store.New(store.Options{MaxVersions: 1})
	put(sc, "k", "v")
	beforeC := sc.Stats()
	tver := sc.Begin()
	errVer := tver.Write("other", []byte("x"))
	_ = tver.Rollback()
	check("limit errors distinguishable, state intact",
		errors.Is(errChain, store.ErrChainTooLong) && errors.Is(errSnap, store.ErrTooManySnapshots) &&
			errors.Is(errVer, store.ErrTooManyVersions) && sc.Stats() == beforeC)
	keep.Close()

	// 11. Read-only queries are stable.
	s11 := buildHot(3)
	q1, q2 := s11.Stats(), s11.Stats()
	check("queries stable and zero for unknown keys", q1 == q2 && s11.KeyStats("missing") == 0)
	// 12. A long transaction never blocks other keys.
	s12 := store.New(store.Options{})
	long := s12.Begin()
	_ = long.Write("a", []byte("held"))
	done := make(chan bool, 1)
	go func() {
		put(s12, "b", "free")
		_, v := read(s12, "b")
		done <- string(v) == "free"
	}()
	ok := false
	select {
	case ok = <-done:
	case <-time.After(5 * time.Second):
	}
	check("long transaction does not block other keys", ok)
	_ = long.Rollback()

	fmt.Printf("TOTAL 12 checks, %d failed\n", failures)
	if failures > 0 {
		panic("demo failed")
	}
}
