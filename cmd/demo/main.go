package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/store"
	"ontology/txid"
)

var fails int

func check(name string, ok bool) {
	tag := "OK  "
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Println(tag, name)
}
func mustTx(s *store.Store, key, val string) {
	t := s.Begin()
	_ = t.Put(key, []byte(val))
	if err := t.Commit(); err != nil {
		panic(err)
	}
}
func main() {
	s := store.New(txid.NewCounter(), store.Config{})
	mustTx(s, "a", "v1")
	v1, _ := s.BeginView()
	mustTx(s, "a", "v2")
	r1, e1 := v1.Get("a")
	r2, e2 := v1.Get("a")
	check("快照隔离与可重复读", e1 == nil && e2 == nil && string(r1) == "v1" && bytes.Equal(r1, r2))
	v1.Close()
	tw := s.Begin()
	_ = tw.Put("u", []byte("uncommitted"))
	vu, _ := s.BeginView()
	_, errOther := vu.Get("u")
	_, errSelf := tw.Get("u")
	check("未提交不可见与读己之写", errors.Is(errOther, store.ErrNotFound) && errSelf == nil)
	vu.Close()
	before := s.Stats("u").TotalVersions
	_ = tw.Rollback()
	vr, _ := s.BeginView()
	_, errRB := vr.Get("u")
	st := s.Stats("u")
	check("回滚无残留", errors.Is(errRB, store.ErrNotFound) && st.TotalVersions == before && st.KeyVersions == 0)
	vr.Close()
	mustTx(s, "d", "x")
	td := s.Begin()
	_ = td.Delete("d")
	_ = td.Commit()
	vd, _ := s.BeginView()
	check("删除与不存在可判定", vd.Status("d") == store.Deleted &&
		vd.Status("ghost") == store.NeverExisted && vd.Status("a") == store.Exists)
	vd.Close()
	mustTx(s, "b", "old")
	vb, _ := s.BeginView()
	mustTx(s, "b", "new")
	rb, _ := vb.Get("b")
	check("快照点边界左闭右开", string(rb) == "old")
	vb.Close()
	mustTx(s, "g", "g1")
	vg, _ := s.BeginView()
	mustTx(s, "g", "g2")
	mustTx(s, "g", "g3")
	pre, _ := vg.Get("g")
	s.Collect()
	post, _ := vg.Get("g")
	check("回收不改变活跃快照读结果", bytes.Equal(pre, post) && string(post) == "g1")
	vg.Close()
	d100, d10000 := churnExamined(100), churnExamined(10000)
	check(fmt.Sprintf("增量回收考察数 N=100:%d N=10000:%d", d100, d10000), d100 == d10000)
	check("水位只升不降", watermarkMonotonic())
	check("崩溃点遍历全部安全", crashSweep())
	check("三类超限可判定", limitsCheck())
	q1, q2 := s.Stats("a"), s.Stats("a")
	check("只读查询连查一致", q1 == q2 && s.Stats("ghost").KeyVersions == 0)
	check("长事务不阻塞其他键", nonBlocking())
	fmt.Printf("TOTAL %d checks, %d failed\n", 12, fails)
	if fails > 0 {
		os.Exit(1)
	}
}
func churnExamined(n int) int {
	s := store.New(txid.NewCounter(), store.Config{})
	for i := 0; i < n; i++ {
		for j := 0; j < 3; j++ {
			mustTx(s, fmt.Sprintf("k%06d", i), "v")
		}
	}
	s.Collect()
	base := s.Examined()
	for i := 0; i < 5; i++ {
		mustTx(s, fmt.Sprintf("k%06d", i), "x")
	}
	s.Collect()
	return s.Examined() - base
}
func watermarkMonotonic() bool {
	s := store.New(txid.NewCounter(), store.Config{})
	var last txid.T
	for i := 0; i < 5; i++ {
		mustTx(s, "w", fmt.Sprintf("v%d", i))
		v, _ := s.BeginView()
		s.Collect()
		v.Close()
		s.Collect()
		cur := s.Stats("w").Watermark
		if cur < last {
			return false
		}
		last = cur
	}
	return last > 0
}
func crashSweep() bool {
	for crashAt := 0; crashAt < 6; crashAt++ {
		s := store.New(txid.NewCounter(), store.Config{})
		calls := 0
		s.SetCrashHook(func(int) {
			if calls == crashAt {
				panic("crash")
			}
			calls++
		})
		t := s.Begin()
		_ = t.Put("c1", []byte("x"))
		_ = t.Put("c2", []byte("y"))
		func() {
			defer func() { recover() }()
			_ = t.Commit()
		}()
		s.Recover()
		s.SetCrashHook(nil)
		v, _ := s.BeginView()
		_, e1 := v.Get("c1")
		_, e2 := v.Get("c2")
		v.Close()
		wantVisible := crashAt == 5
		if (e1 == nil) != wantVisible || (e2 == nil) != wantVisible {
			return false
		}
		if got := s.Stats("c1").TotalVersions; got != map[bool]int{true: 2}[wantVisible] {
			return false
		}
		mustTx(s, "c1", "after")
	}
	return true
}
func limitsCheck() bool {
	s1 := store.New(txid.NewCounter(), store.Config{MaxVersionsPerKey: 1})
	mustTx(s1, "k", "v1")
	t1 := s1.Begin()
	_ = t1.Put("k", []byte("v2"))
	errChain := t1.Commit()
	s2 := store.New(txid.NewCounter(), store.Config{MaxSnapshots: 1})
	vk, _ := s2.BeginView()
	_, errSnap := s2.BeginView()
	vk.Close()
	s3 := store.New(txid.NewCounter(), store.Config{MaxTotalVersions: 1})
	mustTx(s3, "k1", "v")
	t3 := s3.Begin()
	_ = t3.Put("k2", []byte("v"))
	errTotal := t3.Commit()
	ok := errors.Is(errChain, store.ErrChainTooLong) &&
		errors.Is(errSnap, store.ErrTooManySnapshots) &&
		errors.Is(errTotal, store.ErrTooManyVersions) &&
		!errors.Is(errChain, store.ErrTooManyVersions) &&
		!errors.Is(errSnap, store.ErrChainTooLong)
	return ok && s1.Stats("k").TotalVersions == 1 && s3.Stats("k2").TotalVersions == 1
}
func nonBlocking() bool {
	s := store.New(txid.NewCounter(), store.Config{})
	long := s.Begin()
	_ = long.Put("k1", []byte("held"))
	done := make(chan bool, 1)
	go func() {
		mustTx(s, "k2", "free")
		v, _ := s.BeginView()
		_, err := v.Get("k2")
		v.Close()
		done <- err == nil
	}()
	select {
	case ok := <-done:
		_ = long.Rollback()
		return ok
	case <-time.After(2 * time.Second):
		return false
	}
}
