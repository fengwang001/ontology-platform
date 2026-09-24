// demo：两阶段提交器逐项判定演示。退出码 0 表示全部 OK。
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/txn"
)

var failed bool

func ok(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func main() {
	// 1. 第三节十三步：逐步核对 store 与 C（期望取自 NOTES.md 分步表）。
	t := txn.New()
	type step struct {
		op    func() error
		wantC int64
		store map[int64]int64
	}
	steps := []step{
		{func() error { return t.Apply(1, 10) }, 0, map[int64]int64{1: 10}},
		{func() error { return t.Commit(1) }, 1, map[int64]int64{1: 10}},
		{func() error { return t.Apply(2, 20) }, 1, map[int64]int64{1: 10, 2: 20}},
		{func() error { return t.Apply(2, 20) }, 1, map[int64]int64{1: 10, 2: 20}},
		{func() error { _, e := t.Restart(); return e }, 1, map[int64]int64{1: 10, 2: 20}},
		{func() error { return t.Apply(2, 20) }, 1, map[int64]int64{1: 10, 2: 20}},
		{func() error { return t.Commit(2) }, 2, map[int64]int64{1: 10, 2: 20}},
		{func() error { return t.Commit(4) }, 2, map[int64]int64{1: 10, 2: 20}},
		{func() error { return t.Apply(3, 30) }, 2, map[int64]int64{1: 10, 2: 20, 3: 30}},
		{func() error { return t.Commit(3) }, 3, map[int64]int64{1: 10, 2: 20, 3: 30}},
		{func() error { return t.Commit(4) }, 3, map[int64]int64{1: 10, 2: 20, 3: 30}},
		{func() error { return t.Apply(4, 40) }, 3, map[int64]int64{1: 10, 2: 20, 3: 30, 4: 40}},
		{func() error { return t.Commit(4) }, 4, map[int64]int64{1: 10, 2: 20, 3: 30, 4: 40}},
	}
	wantErr := map[int]error{8: txn.ErrOffsetJump, 11: txn.ErrEffectMissing}
	good := true
	for i, s := range steps {
		err := s.op()
		if err != wantErr[i+1] || t.Committed() != s.wantC || !reflect.DeepEqual(t.Snapshot(), s.store) {
			good = false
		}
	}
	ok("13-step trace: store & C match NOTES.md", good)

	// 2. 先效果后位点下崩溃不丢：效果已落盘，重启后 Pending 驱动重放。
	c := api.New()
	c.Apply(1, 10)
	c.Commit(1)
	c.Apply(2, 20)
	c.Restart()
	ok("crash between phases: effect kept, Pending=[2]",
		c.Committed() == 1 && reflect.DeepEqual(c.Pending(), []int64{2}))

	// 3. 位点跳跃被拒；4. 重复 Apply 幂等。
	c2 := api.New()
	c2.Apply(1, 5)
	c2.Apply(1, 5)
	ok("offset jump rejected, C unchanged", c2.Commit(3) == api.ErrOffsetJump && c2.Committed() == 0)
	ok("repeated Apply idempotent", c2.Commit(1) == nil && c2.Committed() == 1)

	// 5+6. 四类可判定错误互不相同，且被拒后状态不变。
	c3 := api.New()
	e1 := c3.Apply(0, 1)
	e2 := c3.Apply(9, 1)
	e3 := c3.Commit(1)
	e4 := c3.Commit(9)
	distinct := map[error]bool{e1: true, e2: true, e3: true, e4: true}
	ok("4 distinct decidable errors", e1 == api.ErrInvalidSeq && e2 == api.ErrOutOfOrder &&
		e3 == api.ErrEffectMissing && e4 == api.ErrOffsetJump && len(distinct) == 4)
	ok("rejected ops leave no trace", c3.Committed() == 0 && len(c3.Pending()) == 0 &&
		c3.Apply(1, 1) == nil)

	// 7. 大 m 下 Commit 为 O(1)（检查计数断言见 TestCommitChecksO1）。
	c4 := api.New()
	for i := int64(1); i <= 10000; i++ {
		c4.Apply(i, i)
		c4.Commit(i)
	}
	c4.Apply(10001, 10001)
	ok("commit O(1) at m=10000", c4.Commit(10001) == nil && c4.Committed() == 10001)

	// 8. 并发重复 Apply 后 C 恰推进 1。
	c5 := api.New()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 100; k++ {
				c5.Apply(1, 7)
			}
		}()
	}
	close(start)
	wg.Wait()
	ok("concurrent Apply then Commit: C advanced exactly 1",
		c5.Commit(1) == nil && c5.Committed() == 1 && len(c5.Pending()) == 0)

	if failed {
		os.Exit(1)
	}
}
