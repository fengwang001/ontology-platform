package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/eff"
	"ontology/txn"
)

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
		return
	}
	fmt.Println("FAIL " + name)
	os.Exit(1)
}

func main() {
	// 1) eff：覆盖写、读取、Clone 彼此独立。
	s := eff.New()
	s.Put(1, 10)
	s.Put(1, 20)
	v, has := s.Get(1)
	cl := s.Clone()
	s.Put(1, 99)
	cv, _ := cl.Get(1)
	check("eff overwrite+clone", has && v == 20 && cv == 20)

	// 2) 十三步轨迹：每步 C/错误符合 NOTES.md；store 逐项由 SelfCheck 对拍朴素参照保证。
	t := txn.New()
	type step struct {
		op     byte
		seq, w int64
		wantC  int64
		wantE  error
	}
	steps := []step{
		{'a', 1, 10, 0, nil}, {'c', 1, 0, 1, nil},
		{'a', 2, 20, 1, nil}, {'a', 2, 20, 1, nil},
		{'r', 0, 0, 1, nil}, {'a', 2, 20, 1, nil},
		{'c', 2, 0, 2, nil}, {'c', 4, 0, 2, txn.ErrOffsetJump},
		{'a', 3, 30, 2, nil}, {'c', 3, 0, 3, nil},
		{'c', 4, 0, 3, txn.ErrEffectMissing},
		{'a', 4, 40, 3, nil}, {'c', 4, 0, 4, nil},
	}
	ok := true
	for _, st := range steps {
		var e error
		if st.op == 'a' {
			e = t.Apply(st.seq, st.w)
		} else if st.op == 'c' {
			e = t.Commit(st.seq)
		} else {
			t.Restart()
		}
		if t.Committed() != st.wantC || !errors.Is(e, st.wantE) {
			ok = false
		}
	}
	check("txn 13-step C/err trace", ok && t.SelfCheck() == nil)

	// 3) 崩在两阶段之间：效果保留、C 不进、Pending=[2]，只重复不丢。
	q := txn.New()
	_ = q.Apply(1, 10)
	_ = q.Commit(1)
	_ = q.Apply(2, 20)
	p := q.Restart()
	check("crash between phases: effect kept, C=1, Pending=[2]",
		q.Committed() == 1 && len(p) == 1 && p[0] == 2)

	// 4) 跳跃被拒（C 不动）、缺效果（全新状态机）被拒，随后正常路径可提交；重复 Apply 幂等。
	jumpOK := errors.Is(q.Commit(4), txn.ErrOffsetJump) && q.Committed() == 1
	mt := txn.New()
	missOK := errors.Is(mt.Commit(1), txn.ErrEffectMissing) && mt.Committed() == 0
	_ = q.Apply(2, 20)
	recoverOK := q.Commit(2) == nil && q.Committed() == 2
	_ = q.Apply(3, 30)
	_ = q.Restart()
	_ = q.Apply(3, 30)
	_ = q.Apply(3, 30)
	pd := q.Pending()
	idemOK := len(pd) == 1 && pd[0] == 3 && q.Commit(3) == nil && q.Committed() == 3
	check("jump/missing rejected; dup Apply idempotent, C advances once",
		jumpOK && missOK && recoverOK && idemOK)

	// 5) 四类错误互不相同、被拒不留痕、之后仍可用。
	z := txn.New()
	got := []error{z.Apply(0, 1), z.Apply(3, 3), z.Commit(2), z.Commit(1)}
	distinct := map[string]bool{}
	for _, e := range got {
		distinct[e.Error()] = true
	}
	usable := z.Apply(1, 11) == nil && z.Commit(1) == nil && z.Committed() == 1
	check("4 distinct sentinel errors; no trace; usable after reject", len(distinct) == 4 && usable)

	// 6) 经对外门面跑完整生命周期（含崩溃恢复重投）；多档 m 下 O(1) 由 SelfCheck 核验。
	f := api.New()
	facadeOK := f.Apply(1, 10) == nil && f.Commit(1) == nil && f.Apply(2, 20) == nil &&
		f.Restart() == nil && len(f.Pending()) == 1 && f.Apply(2, 20) == nil &&
		f.Commit(2) == nil && f.Committed() == 2 && f.SelfCheck() == nil
	check("api facade lifecycle + O(1) check at m=100/1000/10000", facadeOK)

	// 7) N goroutine 并发重复 Apply 同一在途序号后一次 Commit：C 恰 +1、Pending 空、读单调。无 sleep。
	w := txn.New()
	_ = w.Apply(1, 1)
	_ = w.Commit(1)
	const N = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _ = w.Apply(2, 20) }()
	}
	stop := make(chan struct{})
	var mono atomic.Bool
	var rd sync.WaitGroup
	rd.Add(1)
	go func() {
		defer rd.Done()
		prev := w.Committed()
		for {
			select {
			case <-stop:
				return
			default:
				if cur := w.Committed(); cur < prev {
					mono.Store(true)
				} else {
					prev = cur
				}
			}
		}
	}()
	close(start)
	wg.Wait()
	commitOK := w.Commit(2) == nil && w.Committed() == 2 && len(w.Pending()) == 0
	close(stop)
	rd.Wait()
	check("concurrent dup Apply then one Commit: C+1, monotonic reads", commitOK && !mono.Load())
}
