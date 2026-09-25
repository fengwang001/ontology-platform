package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lsm"
	"ontology/mem"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK:", name)
	} else {
		failed = true
		fmt.Println("FAIL:", name)
	}
}

func main() {
	s, _ := api.New(2) // 第三节八步场景
	mustPut := func(k string, v int64) {
		if err := s.Put(k, v); err != nil {
			panic(err)
		}
	}
	mustPut("k1", 1)
	mustPut("k2", 2)
	mustPut("k1", 3)
	if err := s.Del("k2"); err != nil {
		panic(err)
	}
	mustPut("k3", 4)
	v, ok, del, _ := s.Get("k1")
	check("step6 Get(k1)=C=3", ok && !del && v == 3)
	if err := s.Del("k1"); err != nil {
		panic(err)
	}
	_, ok, del, _ = s.Get("k1")
	check("step8 Get(k1)=deleted", ok && del)
	_, okK2, delK2, _ := s.Get("k2")
	_, okNone, delNone, _ := s.Get("never")
	check("deleted != not-exist", okK2 && delK2 && !okNone && !delNone)
	_, _, _, _ = s.Get("absent")
	before := s.ReadAmp() // 两张 SSTable 都查过 → 2
	s.Compact()
	_, okK2, delK2, _ = s.Get("k2")
	k3, _, _, _ := s.Get("k3")
	_, _, _, _ = s.Get("absent")
	check("compact: k2 tombstone kept, k3=D=4", okK2 && delK2 && k3 == 4)
	check("readAmp 2 -> 1", before == 2 && s.ReadAmp() == 1)

	distinct := errors.Is(s.Put("", 0), api.ErrEmptyKey) &&
		errors.Is(s.Put(string(make([]byte, 65)), 0), api.ErrKeyTooLong)
	_, err := api.New(0)
	distinct = distinct && errors.Is(err, api.ErrInvalidMaxMem) &&
		api.ErrEmptyKey != api.ErrKeyTooLong && api.ErrKeyTooLong != api.ErrInvalidMaxMem
	check("3 distinct sentinel errors", distinct)

	t, _ := api.New(1)
	_ = t.Put("x", 9)
	amp := t.ReadAmp()
	rejectOK := t.Put("", 1) != nil && t.Del("") != nil
	_, _, _, gerr := t.Get("")
	xv, xok, _, _ := t.Get("x")
	check("rejected op leaves no trace", rejectOK && gerr != nil &&
		xok && xv == 9 && t.ReadAmp() == amp)

	probeOK := true
	for _, m := range []int{100, 1000, 10000} {
		kvs := make([]mem.KV, 0, m)
		for i := 0; i < m; i++ {
			kvs = append(kvs, mem.KV{Key: fmt.Sprintf("k%05d", i),
				Entry: mem.Entry{Value: int64(i), Seq: int64(i + 1)}})
		}
		l := lsm.New()
		l.Freeze(kvs)
		l.Get("k05000")
		probeOK = probeOK && l.ProbeBounded()
	}
	check("in-SST probe O(1) for m up to 10000", probeOK)

	c, _ := api.New(64)
	_ = c.Put("anchor", 777)
	const nW, nR, nRead = 64, 8, 5000
	var wg sync.WaitGroup
	for i := 0; i < nR; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < nRead; j++ { // 与写并发：已写 key 的值必须始终一致
				if av, aok, _, _ := c.Get("anchor"); !aok || av != 777 {
					panic("concurrent read saw inconsistent value")
				}
			}
		}()
	}
	for i := 0; i < nW; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.Put(fmt.Sprintf("key%d", i), int64(i))
		}(i)
	}
	wg.Wait()
	allGet := true
	for i := 0; i < nW; i++ {
		if gv, gok, _, _ := c.Get(fmt.Sprintf("key%d", i)); !gok || gv != int64(i) {
			allGet = false
		}
	}
	check("concurrent puts consistent", allGet && s.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
