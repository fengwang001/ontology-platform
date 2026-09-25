// Command demo 运行 append-only 变更审计日志的自检演示。
// 不读参数、不联网；逐条打印 OK/FAIL，输出不超过 10 行，退出码反映结果。
package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/ent"
	"ontology/log"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s: %s\n", status, name)
}

func main() {
	// 第三节：七个 Append，逐步记录 Seq 与接受/拒绝。
	type req struct {
		ts      int64
		who, op string
	}
	reqs := []req{{10, "A", "put"}, {12, "A", "get"}, {12, "B", "del"}, {9, "A", "put"},
		{15, "B", "put"}, {15, "", "get"}, {20, "A", "del"}}
	wantSeq := []int64{1, 2, 3, -1, 4, -1, 5}
	wantErr := []error{nil, nil, nil, api.ErrTSOutOfOrder, nil, api.ErrEmptyWho, nil}
	a := api.New()
	gotSeq := []int64{}
	okSteps := true
	for i, r := range reqs {
		seq, err := a.Append(r.ts, r.who, r.op)
		if err != nil {
			seq = -1
		}
		gotSeq = append(gotSeq, seq)
		if seq != wantSeq[i] || !errors.Is(err, wantErr[i]) {
			okSteps = false
		}
	}
	check(fmt.Sprintf("seven appends seqs=%v (4=out-of-order,6=empty-who), total=%d",
		gotSeq, len(a.Entries())), okSteps && len(a.Entries()) == 6)

	// 哈希链重算一致：朴素地从创世逐条重算，且 Verify 返回 -1。
	es := a.Entries()
	recalc := es[0].Hash == sha256.Sum256([]byte("genesis"))
	h := es[0].Hash
	for _, e := range es[1:] {
		h = ent.NextHash(h, e)
		if e.Hash != h {
			recalc = false
		}
	}
	check("naive recompute matches every stored hash; Verify=-1", recalc && a.Verify() == -1)

	// 篡改 Seq=3（Op del→put，不动任何存储 Hash）。
	tam := append([]ent.Entry(nil), es...)
	tam[3].Op = "put"
	t := log.FromSnapshot(tam)
	aff := t.Affected(3)
	check(fmt.Sprintf("tamper seq3: Verify=%d Affected=%v", t.Verify(), aff),
		t.Verify() == 3 && len(aff) == 3 && aff[0] == 3 && aff[2] == 5)

	// 四类可判定错误互不相同，且失败不留痕。
	b := api.New()
	before := len(b.Entries())
	_, e1 := b.Append(-1, "w", "op")
	_, e2 := b.Append(1, "", "op")
	_, e3 := b.Append(1, "w", "")
	_, _ = b.Append(5, "w", "op")
	_, e4 := b.Append(4, "w", "op")
	distinct := errors.Is(e1, api.ErrNegativeTS) && errors.Is(e2, api.ErrEmptyWho) &&
		errors.Is(e3, api.ErrEmptyOp) && errors.Is(e4, api.ErrTSOutOfOrder) &&
		e1 != e2 && e2 != e3 && e3 != e4
	after := b.Entries()
	noTrace := len(after) == before+1 && after[len(after)-1].Seq == 1
	check("four distinct sentinel errors; rejected appends leave no trace", distinct && noTrace)

	// 大 m 下链头读取个数不随 m 增长（判定在 SelfCheck 内部，数值不外泄）。
	check("head-hash reads stay O(1) for m in {100,1000,10000}", a.SelfCheck() == nil)

	// 并发只读：N 个 goroutine 并发 Verify/Affected，结果逐一相同（无 sleep）。
	const N = 16
	var wg sync.WaitGroup
	v := make([]int64, N)
	fa := make([][]int64, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v[i] = a.Verify()
			fa[i] = a.Affected(3)
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		if v[i] != v[0] || len(fa[i]) != len(fa[0]) {
			same = false
		}
		for j := range fa[0] {
			if fa[i][j] != fa[0][j] {
				same = false
			}
		}
	}
	check("concurrent Verify/Affected readers all agree", same)

	if failed {
		os.Exit(1)
	}
}
