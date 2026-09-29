package snapshot

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func logState(t *testing.T, e *Exporter, note string) {
	t.Helper()
	counts := e.Counts()
	align := e.AlignPoint()
	pendings := make([]string, len(counts))
	for i := range counts {
		p, err := e.Pending(i)
		if err != nil {
			t.Fatalf("Pending(%d): %v", i, err)
		}
		pendings[i] = fmt.Sprintf("%d", p)
	}
	t.Logf("%s | 各分区事件数=%v 对齐点=%d(取最小值) 待定缓冲=[%s]",
		note, counts, align, strings.Join(pendings, ","))
}

func mustDeliver(t *testing.T, e *Exporter, partition int, key, value string) {
	t.Helper()
	if err := e.Deliver(partition, key, []byte(value)); err != nil {
		t.Fatalf("Deliver(partition=%d, key=%q): %v", partition, key, err)
	}
	t.Logf("投递成功: partition=%d key=%q value=%q", partition, key, value)
}

func TestAlignPointTakesMin(t *testing.T) {
	e, err := NewExporter(3, 8)
	if err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, e, 0, "a", "v0a")
	mustDeliver(t, e, 0, "b", "v0b")
	mustDeliver(t, e, 0, "c", "v0c")
	mustDeliver(t, e, 1, "a", "v1a")
	mustDeliver(t, e, 1, "b", "v1b")
	mustDeliver(t, e, 2, "a", "v2a")
	logState(t, e, "投递 3/2/1 条后")

	if got, want := e.AlignPoint(), 1; got != want {
		t.Fatalf("对齐点=%d, 期望最小值 %d", got, want)
	}
	t.Logf("判定依据: 对齐点=min(3,2,1)=1, 分区0/1 超出的 2/1 条进入待定缓冲, 不纳入快照")

	snap := e.Snapshot()
	if len(snap.Events) != 3 {
		t.Fatalf("快照事件数=%d, 期望 3(每分区各 1 条)", len(snap.Events))
	}
	for _, ev := range snap.Events {
		if ev.Seq != 1 {
			t.Fatalf("快照含 Seq=%d, 期望仅各分区第 1 条", ev.Seq)
		}
	}
}

func TestBackpressureReject(t *testing.T) {
	e, err := NewExporter(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, e, 0, "k1", "v1")
	mustDeliver(t, e, 0, "k2", "v2")
	logState(t, e, "分区0 待定缓冲已达上限 2")

	before := e.Counts()
	err = e.Deliver(0, "k3", []byte("v3"))
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("期望 ErrBackpressure, 得到 %v", err)
	}
	t.Logf("背压拒收: %v | 判定依据: 投递后待定缓冲 3 > 上限 2, 整体拒绝且状态不变", err)
	logState(t, e, "拒收后")
	if after := e.Counts(); after[0] != before[0] || after[1] != before[1] {
		t.Fatalf("拒收后事件数改变: before=%v after=%v", before, after)
	}

	// 另一分区补齐后对齐点推进, 待定缓冲释放, 可继续投递。
	mustDeliver(t, e, 1, "k1", "w1")
	mustDeliver(t, e, 1, "k2", "w2")
	logState(t, e, "分区1 补齐后对齐点推进")
	mustDeliver(t, e, 0, "k3", "v3")
}

func TestRejectReasonsDistinguishable(t *testing.T) {
	e, err := NewExporter(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := e.Counts()

	err = e.Deliver(5, "k", []byte("v"))
	if !errors.Is(err, ErrPartitionOutOfRange) || errors.Is(err, ErrEmptyKey) || errors.Is(err, ErrBackpressure) {
		t.Fatalf("分区越界原因不可区分: %v", err)
	}
	t.Logf("拒收: %v | 判定依据: 分区号 5 越界 [0,2)", err)

	err = e.Deliver(-1, "k", []byte("v"))
	if !errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("负分区号应报越界: %v", err)
	}

	err = e.Deliver(0, "", []byte("v"))
	if !errors.Is(err, ErrEmptyKey) || errors.Is(err, ErrPartitionOutOfRange) {
		t.Fatalf("空键原因不可区分: %v", err)
	}
	t.Logf("拒收: %v | 判定依据: 键为空串", err)

	mustDeliver(t, e, 0, "k", "v")
	err = e.Deliver(0, "k2", []byte("v2"))
	if !errors.Is(err, ErrBackpressure) || errors.Is(err, ErrEmptyKey) {
		t.Fatalf("背压原因不可区分: %v", err)
	}
	t.Logf("拒收: %v | 判定依据: 待定缓冲 1 已达上限 1", err)

	if after := e.Counts(); after[0] != before[0]+1 || after[1] != before[1] {
		t.Fatalf("失败调用改变了状态: before=%v after=%v", before, after)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestSnapshotEqualContributionAndOverride(t *testing.T) {
	e, err := NewExporter(3, 4)
	if err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, e, 0, "x", "p0-x")
	mustDeliver(t, e, 0, "y", "p0-y")
	mustDeliver(t, e, 1, "x", "p1-x")
	mustDeliver(t, e, 1, "z", "p1-z")
	mustDeliver(t, e, 2, "y", "p2-y")
	mustDeliver(t, e, 2, "w", "p2-w")
	logState(t, e, "三分区各投递 2 条")

	snap := e.Snapshot()
	if snap.AlignPoint != 2 {
		t.Fatalf("对齐点=%d, 期望 2", snap.AlignPoint)
	}
	contrib := make([]int, 3)
	lastPartition := -1
	for _, ev := range snap.Events {
		contrib[ev.Partition]++
		if ev.Partition < lastPartition {
			t.Fatalf("快照未按分区顺序拼接: 分区 %d 出现在 %d 之后", ev.Partition, lastPartition)
		}
		lastPartition = ev.Partition
	}
	for i, c := range contrib {
		if c != snap.AlignPoint {
			t.Fatalf("分区 %d 贡献 %d 条, 期望恰等于对齐点 %d", i, c, snap.AlignPoint)
		}
	}
	t.Logf("判定依据: 各分区贡献=%v, 均等于对齐点 %d, 不存在半个分区被纳入", contrib, snap.AlignPoint)

	view := snap.Materialize()
	if got := string(view["x"]); got != "p1-x" {
		t.Fatalf("键 x=%q, 期望后写的分区1 覆盖分区0: %q", got, "p1-x")
	}
	if got := string(view["y"]); got != "p2-y" {
		t.Fatalf("键 y=%q, 期望后写的分区2 覆盖分区0: %q", got, "p2-y")
	}
	t.Logf("同键覆盖: x=分区1 值, y=分区2 值 | 判定依据: 按分区顺序拼接, 后写覆盖前写")
}

func TestSelfCheck(t *testing.T) {
	e, err := NewExporter(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, e, 0, "a", "1")
	mustDeliver(t, e, 1, "a", "2")
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	t.Logf("自检通过 | 判定依据: 序号连续、对齐点等于最小值、各分区贡献一致")
}

func TestConcurrentDeliverSnapshotSelfCheck(t *testing.T) {
	const (
		numPartitions = 4
		pendingLimit  = 16
		rounds        = 200
	)
	e, err := NewExporter(numPartitions, pendingLimit)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for p := 0; p < numPartitions; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				key := fmt.Sprintf("p%d-k%d", p, i)
				if err := e.Deliver(p, key, []byte(fmt.Sprintf("v%d", i))); err != nil {
					if !errors.Is(err, ErrBackpressure) {
						t.Errorf("非预期拒绝: %v", err)
					}
					continue
				}
			}
		}(p)
	}

	var snapErr error
	var mu sync.Mutex
	stop := make(chan struct{})
	var snapWg sync.WaitGroup
	snapWg.Add(1)
	go func() {
		defer snapWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap := e.Snapshot()
			contrib := make([]int, numPartitions)
			for _, ev := range snap.Events {
				contrib[ev.Partition]++
			}
			for i, c := range contrib {
				if c != snap.AlignPoint {
					mu.Lock()
					snapErr = fmt.Errorf("分区 %d 贡献 %d 条, 对齐点 %d", i, c, snap.AlignPoint)
					mu.Unlock()
				}
			}
			if err := e.SelfCheck(); err != nil {
				mu.Lock()
				snapErr = err
				mu.Unlock()
			}
		}
	}()

	wg.Wait()
	close(stop)
	snapWg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if snapErr != nil {
		t.Fatalf("并发快照出现中间态: %v", snapErr)
	}
	logState(t, e, "并发投递与快照结束后")
	if err := e.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	t.Logf("判定依据: 任一快照内每个分区贡献条数均恰等于该快照对齐点, 无半个分区被纳入")
}
