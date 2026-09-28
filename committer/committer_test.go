package committer

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// mustDeclare 声明分区，失败则终止测试。
func mustDeclare(t *testing.T, c *Committer, key string, start uint64) {
	t.Helper()
	if err := c.DeclarePartition(key, start); err != nil {
		t.Fatalf("DeclarePartition(%q, %d): %v", key, start, err)
	}
}

// mustDeliver 投递连续位点段 [start, start+n)，失败则终止测试。
func mustDeliver(t *testing.T, c *Committer, key string, start uint64, n int) {
	t.Helper()
	offs := make([]uint64, n)
	for i := range offs {
		offs[i] = start + uint64(i)
	}
	if err := c.Deliver(key, offs); err != nil {
		t.Fatalf("Deliver(%q, [%d..%d]): %v", key, start, start+uint64(n)-1, err)
	}
}

// TestOutOfOrderAck 乱序确认：已提交位点只能越过连续已确认前缀。
func TestOutOfOrderAck(t *testing.T) {
	c := NewCommitter(100)
	mustDeclare(t, c, "p0", 0)
	mustDeliver(t, c, "p0", 0, 5) // 投递 0..4

	// 乱序确认 2、4、1：已提交位点应停在 0。
	for _, off := range []uint64{2, 4, 1} {
		if err := c.Ack("p0", off); err != nil {
			t.Fatalf("Ack(p0, %d): %v", off, err)
		}
		st, _ := c.Snapshot("p0")
		t.Logf("输入=Ack(%d) 提交位点=%d 判定依据: 位点0未确认, 连续前缀为空", off, st.Committed)
		if st.Committed != 0 {
			t.Fatalf("committed = %d, want 0", st.Committed)
		}
	}

	// 确认 0：连续前缀变为 0..2，已提交位点推进到 3。
	if err := c.Ack("p0", 0); err != nil {
		t.Fatalf("Ack(p0, 0): %v", err)
	}
	st, _ := c.Snapshot("p0")
	t.Logf("输入=Ack(0) 提交位点=%d 判定依据: 0..2连续已确认, 3未确认", st.Committed)
	if st.Committed != 3 {
		t.Fatalf("committed = %d, want 3", st.Committed)
	}

	// 确认 3：3、4 连续，推进到 5（全部确认完毕）。
	if err := c.Ack("p0", 3); err != nil {
		t.Fatalf("Ack(p0, 3): %v", err)
	}
	st, _ = c.Snapshot("p0")
	t.Logf("输入=Ack(3) 提交位点=%d 在途=%d 判定依据: 0..4全部确认", st.Committed, st.InFlight)
	if st.Committed != 5 || st.InFlight != 0 || c.InFlight() != 0 {
		t.Fatalf("state = %+v, global inFlight = %d; want committed=5, inFlight=0", st, c.InFlight())
	}
}

// TestDuplicateAckIdempotent 重复确认幂等：不改变位点与在途计数。
func TestDuplicateAckIdempotent(t *testing.T) {
	c := NewCommitter(10)
	mustDeclare(t, c, "p0", 0)
	mustDeliver(t, c, "p0", 0, 3)

	if err := c.Ack("p0", 1); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	before := c.SnapshotAll()
	beforeGlobal := c.InFlight()
	for i := 0; i < 3; i++ {
		if err := c.Ack("p0", 1); err != nil {
			t.Fatalf("duplicate Ack(%d): %v", i, err)
		}
	}
	after := c.SnapshotAll()
	t.Logf("输入=Ack(1)x4 提交位点=%d 在途=%d 判定依据: 重复确认幂等",
		after["p0"].Committed, after["p0"].InFlight)
	if !reflect.DeepEqual(before, after) || c.InFlight() != beforeGlobal {
		t.Fatalf("duplicate ack changed state: before=%v/%d after=%v/%d",
			before, beforeGlobal, after, c.InFlight())
	}
}

// TestAckRegression 位点回退：确认小于已提交位点的位点必须拒绝。
func TestAckRegression(t *testing.T) {
	c := NewCommitter(10)
	mustDeclare(t, c, "p0", 0)
	mustDeliver(t, c, "p0", 0, 3)
	for _, off := range []uint64{0, 1, 2} {
		if err := c.Ack("p0", off); err != nil {
			t.Fatalf("Ack(%d): %v", off, err)
		}
	}
	// 已提交位点现为 3，确认 1 属于回退。
	err := c.Ack("p0", 1)
	t.Logf("输入=Ack(1) 提交位点=3 判定依据: 1 < 3 回退, 拒绝(%v)", err)
	if !errors.Is(err, ErrAckRegression) {
		t.Fatalf("err = %v, want ErrAckRegression", err)
	}
	st, _ := c.Snapshot("p0")
	if st.Committed != 3 {
		t.Fatalf("rejected ack changed committed to %d", st.Committed)
	}
}

// TestRestartRedelivery 重启后重新投递：从已提交位点起重新投递的集合
// 恰好是 [committed, nextDeliver)，确认后不多不少推进到原终点。
func TestRestartRedelivery(t *testing.T) {
	c := NewCommitter(100)
	mustDeclare(t, c, "p0", 0)
	mustDeclare(t, c, "p1", 100)
	mustDeliver(t, c, "p0", 0, 6)
	mustDeliver(t, c, "p1", 100, 4)
	// p0 确认 0,1,3,5（乱序）→ 提交位点 2；p1 确认 100 → 提交位点 101。
	for _, off := range []uint64{0, 1, 3, 5} {
		if err := c.Ack("p0", off); err != nil {
			t.Fatalf("Ack p0/%d: %v", off, err)
		}
	}
	if err := c.Ack("p1", 100); err != nil {
		t.Fatalf("Ack p1/100: %v", err)
	}
	snap := c.SnapshotAll()
	t.Logf("崩溃前快照: %+v", snap)

	// 模拟重启：新提交器从已提交位点恢复，重新投递 [committed, nextDeliver)。
	c2 := NewCommitter(100)
	redeliver := map[string][]uint64{}
	for key, st := range snap {
		mustDeclare(t, c2, key, st.Committed)
		for off := st.Committed; off < st.NextDeliver; off++ {
			redeliver[key] = append(redeliver[key], off)
		}
		if err := c2.Deliver(key, redeliver[key]); err != nil {
			t.Fatalf("redeliver %s: %v", key, err)
		}
	}
	t.Logf("重启后重新投递集合: %v 判定依据: [已提交位点, 期望投递位点)", redeliver)
	// 重新投递集合必须恰好覆盖崩溃前未确认的位点。
	if got, want := fmt.Sprint(redeliver["p0"]), "[2 3 4 5]"; got != want {
		t.Fatalf("p0 redeliver = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(redeliver["p1"]), "[101 102 103]"; got != want {
		t.Fatalf("p1 redeliver = %s, want %s", got, want)
	}
	// 全部确认后推进到原投递终点：不丢（未确认的重新投递了）不多（已确认的不再投递）。
	for key, offs := range redeliver {
		for _, off := range offs {
			if err := c2.Ack(key, off); err != nil {
				t.Fatalf("Ack %s/%d after restart: %v", key, off, err)
			}
		}
	}
	final := c2.SnapshotAll()
	t.Logf("重启确认后快照: %+v", final)
	if final["p0"].Committed != 6 || final["p1"].Committed != 104 {
		t.Fatalf("final committed = %v, want p0=6 p1=104", final)
	}
	if c2.InFlight() != 0 {
		t.Fatalf("inFlight = %d after full ack, want 0", c2.InFlight())
	}
}

// TestSharedLimitAtomicRejection 跨分区共享上限占满时整体拒绝，且状态不变。
func TestSharedLimitAtomicRejection(t *testing.T) {
	c := NewCommitter(5) // 全局共享上限 5
	mustDeclare(t, c, "a", 0)
	mustDeclare(t, c, "b", 0)
	mustDeliver(t, c, "a", 0, 3) // 在途 3/5
	mustDeliver(t, c, "b", 0, 2) // 在途 5/5，占满

	before := c.SnapshotAll()
	beforeGlobal := c.InFlight()

	// b 再投递 1 条：超限，整体拒绝。
	err := c.Deliver("b", []uint64{2})
	t.Logf("输入=Deliver(b,[2]) 在途=%d/5 判定依据: 5+1>5 超限整体拒绝(%v)", beforeGlobal, err)
	if !errors.Is(err, ErrInFlightLimitExceeded) {
		t.Fatalf("err = %v, want ErrInFlightLimitExceeded", err)
	}
	// a 投递 3 条同样超限（即使 a 自己一条都没超）。
	if err := c.Deliver("a", []uint64{3, 4, 5}); !errors.Is(err, ErrInFlightLimitExceeded) {
		t.Fatalf("err = %v, want ErrInFlightLimitExceeded", err)
	}
	after := c.SnapshotAll()
	if !reflect.DeepEqual(before, after) || c.InFlight() != beforeGlobal {
		t.Fatalf("rejected deliver changed state: before=%v/%d after=%v/%d",
			before, beforeGlobal, after, c.InFlight())
	}

	// 确认 1 条释放在途额度后，投递恢复可用。
	if err := c.Ack("a", 0); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	mustDeliver(t, c, "b", 2, 1)
	t.Logf("确认释放额度后 Deliver(b,[2]) 成功, 在途=%d/5", c.InFlight())
	if c.InFlight() != 5 {
		t.Fatalf("inFlight = %d, want 5", c.InFlight())
	}
}

// TestInvalidInputs 各类非法输入：错误可用 errors.Is 区分，且状态不变。
func TestInvalidInputs(t *testing.T) {
	c := NewCommitter(10)
	mustDeclare(t, c, "p0", 5)
	mustDeliver(t, c, "p0", 5, 3) // 投递 5,6,7

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"空分区键-声明", func() error { return c.DeclarePartition("", 0) }, ErrEmptyPartitionKey},
		{"空分区键-投递", func() error { return c.Deliver("", []uint64{0}) }, ErrEmptyPartitionKey},
		{"空分区键-确认", func() error { return c.Ack("", 0) }, ErrEmptyPartitionKey},
		{"未声明分区-投递", func() error { return c.Deliver("ghost", []uint64{0}) }, ErrPartitionNotDeclared},
		{"未声明分区-确认", func() error { return c.Ack("ghost", 0) }, ErrPartitionNotDeclared},
		{"重复声明", func() error { return c.DeclarePartition("p0", 0) }, ErrPartitionAlreadyDeclared},
		{"不连续投递-跳号", func() error { return c.Deliver("p0", []uint64{9}) }, ErrNonContiguousDelivery},
		{"不连续投递-批内乱序", func() error { return c.Deliver("p0", []uint64{9, 8}) }, ErrNonContiguousDelivery},
		{"不连续投递-重复", func() error { return c.Deliver("p0", []uint64{7}) }, ErrNonContiguousDelivery},
		{"确认越界-未投递", func() error { return c.Ack("p0", 8) }, ErrAckOutOfRange},
		{"确认回退", func() error { return c.Ack("p0", 4) }, ErrAckRegression},
	}
	before := c.SnapshotAll()
	beforeGlobal := c.InFlight()
	for _, tc := range cases {
		err := tc.op()
		t.Logf("输入=%s 提交位点=%d 判定依据: %v", tc.name, before["p0"].Committed, err)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want errors.Is %v", tc.name, err, tc.want)
		}
	}
	after := c.SnapshotAll()
	if !reflect.DeepEqual(before, after) || c.InFlight() != beforeGlobal {
		t.Fatalf("rejected ops changed state: before=%v/%d after=%v/%d",
			before, beforeGlobal, after, c.InFlight())
	}
	// 哨兵错误之间必须可区分。
	if errors.Is(ErrAckOutOfRange, ErrAckRegression) || errors.Is(ErrInFlightLimitExceeded, ErrNonContiguousDelivery) {
		t.Fatal("sentinel errors must be distinguishable")
	}
}

// TestAdvanceScansIndependentOfInFlight 推进扫描次数与在途规模无关：
// 总扫描步数恒等于已提交位点的累计推进量。
func TestAdvanceScansIndependentOfInFlight(t *testing.T) {
	const n = 10000
	c := NewCommitter(n)
	mustDeclare(t, c, "p0", 0)
	mustDeliver(t, c, "p0", 0, n)

	// 先乱序确认 1..n-1（在途积压到最大），最后确认 0 一次性推进。
	for off := uint64(1); off < n; off++ {
		if err := c.Ack("p0", off); err != nil {
			t.Fatalf("Ack(%d): %v", off, err)
		}
	}
	scansBefore := c.advanceScans
	if err := c.Ack("p0", 0); err != nil {
		t.Fatalf("Ack(0): %v", err)
	}
	st, _ := c.Snapshot("p0")
	t.Logf("在途峰值=%d 提交位点=%d 总扫描步数=%d 判定依据: 步数==累计推进量, 与在途规模无关",
		n-1, st.Committed, c.advanceScans)
	if st.Committed != n {
		t.Fatalf("committed = %d, want %d", st.Committed, n)
	}
	// 每次确认最多推进 1 步（确认的不是洞），确认 0 后推进剩余 n-1 步：
	// 总步数 == 总推进量 == n，绝不随在途规模 O(n) 地反复扫描（否则会是 O(n^2)）。
	if c.advanceScans != n {
		t.Fatalf("advanceScans = %d, want %d (== total committed advance)", c.advanceScans, n)
	}
	if scansBefore != 0 {
		t.Fatalf("scans before final ack = %d, want 0 (no contiguous prefix)", scansBefore)
	}
}

// TestConcurrentReadsConsistent 并发只读：快照逐字段一致
// （Committed <= NextDeliver 且 InFlight 与全局计数协调）。
func TestConcurrentReadsConsistent(t *testing.T) {
	c := NewCommitter(1000)
	keys := []string{"a", "b", "c"}
	for _, k := range keys {
		mustDeclare(t, c, k, 0)
	}
	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	// 写方：循环投递+确认。
	for _, k := range keys {
		writers.Add(1)
		go func(k string) {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				if err := c.Deliver(k, []uint64{uint64(i)}); err != nil {
					return
				}
				_ = c.Ack(k, uint64(i))
			}
		}(k)
	}
	// 读方：并发快照，校验字段间一致性。
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := c.SnapshotAll()
				for k, st := range snap {
					if st.Committed > st.NextDeliver {
						t.Errorf("%s: committed %d > nextDeliver %d (torn read)", k, st.Committed, st.NextDeliver)
					}
					if st.InFlight < 0 {
						t.Errorf("%s: negative inFlight %d", k, st.InFlight)
					}
					// 在途 = 已投递未确认 <= 已投递未提交（乱序确认使后者含已确认未越过的位点）。
					if uint64(st.InFlight) > st.NextDeliver-st.Committed {
						t.Errorf("%s: inFlight %d > nextDeliver-committed %d (inconsistent fields)",
							k, st.InFlight, st.NextDeliver-st.Committed)
					}
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
}

// TestConcurrentAcksMatchNaive 并发确认的最终结果与朴素逐位检查一致。
func TestConcurrentAcksMatchNaive(t *testing.T) {
	const (
		parts = 8
		per   = 500
	)
	c := NewCommitter(parts * per)
	for p := 0; p < parts; p++ {
		mustDeclare(t, c, fmt.Sprintf("p%d", p), 0)
		mustDeliver(t, c, fmt.Sprintf("p%d", p), 0, per)
	}
	// 并发乱序确认全部位点。
	var wg sync.WaitGroup
	for p := 0; p < parts; p++ {
		key := fmt.Sprintf("p%d", p)
		perm := rand.New(rand.NewSource(int64(p))).Perm(per)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, i := range perm {
				if err := c.Ack(key, uint64(i)); err != nil {
					t.Errorf("Ack(%s, %d): %v", key, i, err)
				}
			}
		}()
	}
	wg.Wait()

	// 朴素逐位检查：全部确认 ⇒ 提交位点 == per，在途 == 0。
	snap := c.SnapshotAll()
	t.Logf("并发确认后快照: %v 判定依据: 全部位点已确认, 朴素逐位检查提交位点应为%d", snap, per)
	for p := 0; p < parts; p++ {
		st := snap[fmt.Sprintf("p%d", p)]
		if st.Committed != per || st.InFlight != 0 {
			t.Fatalf("p%d: %+v, want committed=%d inFlight=0", p, st, per)
		}
	}
	if c.InFlight() != 0 {
		t.Fatalf("global inFlight = %d, want 0", c.InFlight())
	}
}

// TestDeterministic 同一输入序列反复计算得到完全相同的结果。
func TestDeterministic(t *testing.T) {
	run := func() map[string]PartitionState {
		c := NewCommitter(64)
		mustDeclare(t, c, "x", 10)
		mustDeclare(t, c, "y", 0)
		mustDeliver(t, c, "x", 10, 6)
		mustDeliver(t, c, "y", 0, 4)
		seq := []struct {
			key string
			off uint64
		}{{"x", 12}, {"y", 1}, {"x", 10}, {"y", 0}, {"x", 11}, {"y", 3}, {"x", 14}, {"y", 2}, {"x", 13}, {"x", 15}}
		for _, s := range seq {
			if err := c.Ack(s.key, s.off); err != nil {
				t.Fatalf("Ack(%s, %d): %v", s.key, s.off, err)
			}
		}
		return c.SnapshotAll()
	}
	first := run()
	t.Logf("确定性基准结果: %+v", first)
	for i := 0; i < 20; i++ {
		if got := run(); !reflect.DeepEqual(first, got) {
			t.Fatalf("run %d differs: %+v vs %+v", i, first, got)
		}
	}
}
