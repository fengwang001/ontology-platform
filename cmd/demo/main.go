package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	"ontology/snapshot"
	"ontology/txn"
	"ontology/visible"
)

var failed bool

func report(name string, ok bool) {
	failed = failed || !ok
	fmt.Println(map[bool]string{true: "OK   ", false: "FAIL "}[ok] + name)
}
func main() {
	tb := txn.NewTable()
	for i := 1; i <= 4; i++ {
		tb.Begin(txn.ID(i))
	}
	tb.Commit(1, 5) // seq 5, yet still in the snapshot's active set
	tb.Commit(2, 10)
	tb.Abort(4)
	snap, _ := snapshot.New(3, 10, []txn.ID{1, 3})
	vis, _ := visible.Visible(tb, snap, 1)
	report("反例: 朴素规则判可见而本实现判不可见", 5 < snap.Watermark() && !vis)
	vis, _ = visible.Visible(tb, snap, 2)
	report("提交号等于水位不可见(左闭右开)", !vis)
	vis, _ = visible.Visible(tb, snap, 3)
	report("自己写的自己可见", vis)
	vis, _ = visible.Visible(tb, snap, 4)
	report("已回滚永不可见", !vis)
	big, act := buildBig()
	bs, _ := snapshot.New(0, 10001, act)
	_, lookups, _ := visible.Counted(big, bs, 5000)
	report("单次判定查找次数不超过2", lookups <= 2)
	_, err := snapshot.New(0, 1, make([]txn.ID, snapshot.MaxActive+1))
	report("活跃集超限被拒", errors.Is(err, snapshot.ErrTooManyActive) && bs.Len() == 1000)
	report("一万组随机对拍与朴素参考一致", randomOK())
	report("并发判定结果一致", concurrentOK(big, bs))
	fmt.Println("TOTAL " + map[bool]string{true: "OK", false: "FAIL"}[!failed])
}
func buildBig() (*txn.Table, []txn.ID) {
	tb, act := txn.NewTable(), make([]txn.ID, 1000)
	for i := 1; i <= 10000; i++ {
		tb.Begin(txn.ID(i))
		if i <= 1000 {
			act[i-1] = txn.ID(i)
			continue
		}
		tb.Commit(txn.ID(i), uint64(i))
	}
	return tb, act
}
func randomOK() bool {
	rng := rand.New(rand.NewSource(42))
	for range 10000 {
		tb, act, seq, n := txn.NewTable(), []txn.ID(nil), uint64(0), 1+rng.Intn(20)
		for i := 1; i <= n; i++ {
			tb.Begin(txn.ID(i))
			switch rng.Intn(4) {
			case 0:
				act = append(act, txn.ID(i))
			case 1:
				tb.Abort(txn.ID(i))
			default:
				seq += 1 + uint64(rng.Intn(3))
				tb.Commit(txn.ID(i), seq)
			}
		}
		s, _ := snapshot.New(txn.ID(1+rng.Intn(n)), uint64(rng.Intn(int(seq)+2)), act)
		q := txn.ID(rng.Intn(n + 2))
		got, ge := visible.Visible(tb, s, q)
		if want, we := visible.Naive(tb, s, q); got != want || (ge == nil) != (we == nil) {
			return false
		}
	}
	return true
}
func concurrentOK(tb *txn.Table, s *snapshot.Snapshot) bool {
	bad, wg := new(atomic.Bool), new(sync.WaitGroup)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= 10000; i++ {
				if v, _ := visible.Visible(tb, s, txn.ID(i)); v != (i > 1000) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	return !bad.Load()
}
