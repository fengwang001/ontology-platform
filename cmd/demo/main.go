package main

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/txn"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fails++
		fmt.Printf("FAIL %s\n", name)
	}
}

func ev(k txn.Kind, tx int64, d string) txn.Event { return txn.Event{Kind: k, Tx: tx, Data: d} }

func steps13() []txn.Event {
	return []txn.Event{
		ev(txn.Begin, 1, ""), ev(txn.Begin, 2, ""), ev(txn.Row, 2, "a"), ev(txn.Row, 1, "b"),
		ev(txn.Begin, 3, ""), ev(txn.Row, 3, "c"), ev(txn.Row, 2, "d"), ev(txn.Commit, 2, ""),
		ev(txn.Row, 1, "e"), ev(txn.Rollback, 3, ""), ev(txn.Row, 1, "f"), ev(txn.Commit, 1, ""),
		ev(txn.Commit, 3, ""),
	}
}

func main() {
	// 1. txn：事件合法性判定
	check("txn-validate", txn.Event{Kind: txn.Begin, Tx: 1}.Validate() == nil &&
		txn.Event{Kind: txn.Kind(9), Tx: 1}.Validate() == txn.ErrInvalidEvent &&
		txn.Event{Kind: txn.Row, Tx: 0}.Validate() == txn.ErrInvalidEvent &&
		txn.Event{Kind: txn.Commit, Tx: -3}.Validate() == txn.ErrInvalidEvent)

	// 2. 十三步：每步接受/拒绝与 Buffered
	wantErr := []error{nil, nil, nil, nil, nil, nil, txn.ErrBufferFull, nil, nil, nil, nil, nil, txn.ErrUnknownTx}
	wantBuf := []int{0, 0, 1, 2, 2, 3, 3, 2, 3, 2, 3, 0, 0}
	p := api.New(3)
	ok := true
	for i, e := range steps13() {
		_, err := p.Feed(e)
		ok = ok && err == wantErr[i] && p.Buffered() == wantBuf[i]
	}
	check("thirteen-steps", ok)

	// 3. 输出按 COMMIT 顺序、回滚行不出现、与朴素参照一致
	out := p.Output()
	check("commit-order-output", len(out) == 2 &&
		out[0].Tx == 2 && slices.Equal(out[0].Rows, []string{"a"}) &&
		out[1].Tx == 1 && slices.Equal(out[1].Rows, []string{"b", "e", "f"}))

	// 4. 四类哨兵错误互不相同
	q := api.New(1)
	q.Feed(ev(txn.Begin, 1, ""))
	q.Feed(ev(txn.Row, 1, "x"))
	errs := []error{
		func() error { _, e := q.Feed(txn.Event{Kind: 7, Tx: 1}); return e }(),
		func() error { _, e := q.Feed(ev(txn.Begin, 1, "")); return e }(),
		func() error { _, e := q.Feed(ev(txn.Commit, 9, "")); return e }(),
		func() error { _, e := q.Feed(ev(txn.Row, 1, "y")); return e }(),
	}
	seen := map[error]bool{}
	ok = len(errs) == 4
	for _, e := range errs {
		ok = ok && e != nil && !seen[e]
		seen[e] = true
	}
	check("four-distinct-errors", ok)

	// 5. 被拒后状态不变且可继续正常使用
	before := q.Buffered()
	q.Feed(txn.Event{Kind: 7, Tx: 1})
	q.Feed(ev(txn.Begin, 1, ""))
	q.Feed(ev(txn.Row, 2, "z"))
	q.Feed(ev(txn.Row, 1, "overflow"))
	_, err := q.Feed(ev(txn.Commit, 1, ""))
	check("reject-leaves-no-trace", q.Buffered() == before-1 && err == nil &&
		len(q.Output()) == 1 && slices.Equal(q.Output()[0].Rows, []string{"x"}))

	// 6. 大 m 场景功能正确（检查个数不随 m 增长的断言在 regroup 包内测试）
	big := api.New(20000)
	big.Feed(ev(txn.Begin, 1, ""))
	for i := 0; i < 10000; i++ {
		big.Feed(ev(txn.Row, 1, "r"))
	}
	big.Feed(ev(txn.Begin, 2, ""))
	big.Feed(ev(txn.Row, 2, "s"))
	big.Feed(ev(txn.Commit, 2, ""))
	big.Feed(ev(txn.Commit, 1, ""))
	bo := big.Output()
	check("large-m-scenario", len(bo) == 2 && bo[0].Tx == 2 && len(bo[1].Rows) == 10000 &&
		big.Buffered() == 0)

	// 7. 并发：N 个 goroutine 各驱动一个 tx
	const n = 32
	c := api.New(1 << 20)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(tx int64) {
			defer wg.Done()
			c.Feed(ev(txn.Begin, tx, ""))
			for j := 0; j < 20; j++ {
				c.Feed(ev(txn.Row, tx, fmt.Sprintf("t%d-r%d", tx, j)))
			}
			c.Feed(ev(txn.Commit, tx, ""))
		}(int64(g + 1))
	}
	wg.Wait()
	co := c.Output()
	ok = len(co) == n && c.Buffered() == 0
	for _, t := range co {
		ok = ok && len(t.Rows) == 20 && t.Rows[0] == fmt.Sprintf("t%d-r0", t.Tx) &&
			t.Rows[19] == fmt.Sprintf("t%d-r19", t.Tx)
	}
	check("concurrent-txns", ok)

	// 8. 自检（含朴素参照一致性）
	check("self-check", api.New(3).SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
