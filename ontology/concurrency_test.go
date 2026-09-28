package ontology

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestConcurrentReadWrite 在持续写入下并发读取：
//   - 竞态检测器保证无数据竞争（go test -race）；
//   - 读到的任何视图都不含计数非正的组；
//   - 写入全部结束后，增量视图、行表批量重算、日志逐条折叠三者一致。
func TestConcurrentReadWrite(t *testing.T) {
	agg := New(0)
	const writers = 8
	const cycles = 25

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：持续取快照，校验不变量。
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			view := agg.Snapshot()
			for _, v := range view {
				if v.Count <= 0 {
					t.Errorf("并发视图出现计数非正的组: %+v", v)
					return
				}
			}
		}
	}()

	// 写者：各自拥有不相交的行 ID 前缀，独立完成
	// 插入→同组更新→改键→删除 的生命周期，任何交错下都合法。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := func(k int) string { return fmt.Sprintf("w%d-r%d", w, k) }
			for k := 0; k < cycles; k++ {
				rid := id(k)
				batches := [][]Op{
					{{Kind: OpInsert, RowID: rid, Group: "g0", Value: int64(w + 1)}},
					{{Kind: OpUpdate, RowID: rid, Group: "g0", Value: -int64(w + 1)}},
					{{Kind: OpUpdate, RowID: rid, Group: fmt.Sprintf("g%d", (k%3)+1), Value: int64(k)}},
					{{Kind: OpDelete, RowID: rid}},
				}
				for _, b := range batches {
					if _, err := agg.Apply(b); err != nil {
						t.Errorf("写入失败: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	<-readerDone

	// 所有行最终都已删除：视图应为空，且与重算一致。
	assertConsistent(t, agg)
	if len(agg.Snapshot()) != 0 {
		t.Fatalf("全部行删除后视图应为空, got=%v", agg.Snapshot())
	}

	// 关键校验：按顺序折叠日志，必须与当前视图（批量重算）一致。
	assertLogReplay(t, agg)
}

// assertLogReplay 模拟下游：只按 Seq 顺序应用每条日志的增量，
// 计数归零即移除组，最终视图必须与 Snapshot/批量重算一致。
func assertLogReplay(t *testing.T, agg *Aggregator) {
	t.Helper()
	folded := make(map[string]GroupView)
	var lastSeq int
	for _, e := range agg.Log() {
		if e.Seq != lastSeq+1 {
			t.Fatalf("日志序号不连续: 上一条=%d 当前=%d", lastSeq, e.Seq)
		}
		lastSeq = e.Seq
		v := folded[e.Group]
		v.Group = e.Group
		v.Sum += e.Value
		v.Count += e.Count
		if v.Count == 0 {
			delete(folded, e.Group)
		} else {
			folded[e.Group] = v
		}
	}
	got := agg.Snapshot()
	if len(got) != len(folded) {
		t.Fatalf("日志折叠结果组数 %d 与视图 %d 不一致", len(folded), len(got))
	}
	for _, v := range got {
		if folded[v.Group] != v {
			t.Fatalf("日志折叠结果与视图不一致: group=%s folded=%+v view=%+v", v.Group, folded[v.Group], v)
		}
	}
	logReason(t, "下游仅按序应用日志增量即得到与批量重算完全一致的视图（序号 1..%d 连续）", lastSeq)
}

// TestDeterminism 同一输入序列在多个独立聚合器上反复计算，
// 日志条目与最终视图必须逐条/逐项完全相同。
func TestDeterminism(t *testing.T) {
	script := func(agg *Aggregator) {
		must := func(ops []Op) {
			t.Helper()
			if _, err := agg.Apply(ops); err != nil {
				t.Fatalf("脚本执行失败: %v", err)
			}
		}
		must([]Op{
			{Kind: OpInsert, RowID: "a", Group: "g1", Value: 10},
			{Kind: OpInsert, RowID: "b", Group: "g1", Value: -10},
			{Kind: OpInsert, RowID: "c", Group: "g2", Value: 7},
		})
		must([]Op{{Kind: OpUpdate, RowID: "a", Group: "g2", Value: 3}})
		must([]Op{{Kind: OpDelete, RowID: "b"}})
		must([]Op{{Kind: OpInsert, RowID: "d", Group: "g3", Value: 0}})
	}

	var refLog []LogEntry
	var refView []GroupView
	for run := 0; run < 3; run++ {
		agg := New(0)
		script(agg)
		l, v := agg.Log(), agg.Snapshot()
		if run == 0 {
			refLog, refView = l, v
			continue
		}
		if fmt.Sprint(l) != fmt.Sprint(refLog) {
			t.Fatalf("第 %d 次运行日志与首次不一致:\n%v\nvs\n%v", run, l, refLog)
		}
		if fmt.Sprint(v) != fmt.Sprint(refView) {
			t.Fatalf("第 %d 次运行视图与首次不一致: %v vs %v", run, v, refView)
		}
	}
	logReason(t, "同一输入序列连续 3 次独立计算，日志与视图完全相同")
}

// TestStreamHistoryAndLive 订阅前的历史日志与订阅后的实时日志必须
// 无重无漏地按序投递，与 agg.Log() 完全一致。
func TestStreamHistoryAndLive(t *testing.T) {
	agg := New(0)
	// 预写 3 条历史日志。
	agg.Apply([]Op{
		{Kind: OpInsert, RowID: "h1", Group: "g1", Value: 1},
		{Kind: OpInsert, RowID: "h2", Group: "g1", Value: 2},
		{Kind: OpInsert, RowID: "h3", Group: "g2", Value: 3},
	})

	ch := make(chan LogEntry, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agg.Stream(ctx, ch)

	var mu sync.Mutex
	var got []LogEntry
	collectDone := make(chan struct{})
	go func() {
		for e := range ch {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		}
		close(collectDone)
	}()

	// 订阅后再产生新日志（改键 2 条 + 删除 1 条）。
	agg.Apply([]Op{{Kind: OpUpdate, RowID: "h1", Group: "g2", Value: 1}})
	agg.Apply([]Op{{Kind: OpDelete, RowID: "h3"}})

	want := agg.Log() // 共 7 条
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= len(want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("流投递超时: got=%d want=%d", n, len(want))
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		mu.Unlock()
		t.Fatalf("流投递与日志不一致:\n got=%v\nwant=%v", got, want)
	}
	mu.Unlock()
	logReason(t, "先回放 %d 条历史、再接续 %d 条实时日志，顺序与内容与 Log() 完全一致",
		3, len(want)-3)

	cancel()
	// 取消后通道最终应被收集者排空完毕（这里通道不关闭也不影响：取消后停止追加）。
	_ = collectDone
}

// TestStreamOnEmpty 空聚合器上先订阅再写入，实时日志也应送达。
func TestStreamOnEmpty(t *testing.T) {
	agg := New(0)
	ch := make(chan LogEntry, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agg.Stream(ctx, ch)

	agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 5}})
	select {
	case e := <-ch:
		if e.Seq != 1 || e.Group != "g1" || e.Value != 5 {
			t.Fatalf("实时日志错误: %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("空聚合器订阅后的实时日志未送达")
	}
}

// TestStreamCancelUnblocksActor 已取消的订阅者不得卡住 actor：
// 后续 Apply 必须照常返回，被拒绝的批也不产生投递。
func TestStreamCancelUnblocksActor(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{{Kind: OpInsert, RowID: "h", Group: "g1", Value: 1}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()                                // 注册前即取消
	go agg.Stream(ctx, make(chan LogEntry)) // 无缓冲且无人接收

	done := make(chan struct{})
	go func() {
		// 历史投递应因 ctx 取消而立即放弃，Apply 不被阻塞。
		if _, err := agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g2", Value: 2}}); err != nil {
			t.Errorf("取消订阅后 Apply 失败: %v", err)
		}
		// 非法批同样正常返回且不落日志。
		if _, err := agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g3", Value: 2}}); err == nil {
			t.Errorf("重复插入应被拒绝")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("已取消的订阅者阻塞了 actor")
	}

	if l := len(agg.Log()); l != 2 {
		t.Fatalf("被拒绝的批不应产生日志, got=%d", l)
	}
}
