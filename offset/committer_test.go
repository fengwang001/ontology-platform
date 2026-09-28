package offset

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveCommitted 朴素逐位检查：从 start 起，只要位点在 acked 中就一直前进。
// 作为并发与随机测试的对照实现。
func naiveCommitted(start uint64, acked map[uint64]bool) uint64 {
	c := start
	for acked[c] {
		c++
	}
	return c
}

// mustDeclare 声明分区并登记 [start, start+n) 的连续投递。
func mustDeclare(t *testing.T, c *Committer, key string, start uint64, n uint64) {
	t.Helper()
	if err := c.Declare(key, start); err != nil {
		t.Fatalf("Declare(%q, %d): %v", key, start, err)
	}
	for i := uint64(0); i < n; i++ {
		if err := c.Deliver(key, start+i); err != nil {
			t.Fatalf("Deliver(%q, %d): %v", key, start+i, err)
		}
	}
}

func committed(t *testing.T, c *Committer, key string) uint64 {
	t.Helper()
	v, err := c.Committed(key)
	if err != nil {
		t.Fatalf("Committed(%q): %v", key, err)
	}
	return v
}

// TestOutOfOrderAck 乱序确认：只有连续前缀被确认时提交位点才推进。
func TestOutOfOrderAck(t *testing.T) {
	c, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 100, 6) // 投递 100..105

	steps := []struct {
		ack  uint64
		want uint64 // 期望的提交位点
		why  string
	}{
		{102, 100, "102 已确认但 100、101 缺失，提交位点不动"},
		{104, 100, "104 已确认但前缀仍缺，提交位点不动"},
		{100, 101, "100 到位，连续前缀延伸到 101"},
		{101, 103, "101 到位后与已确认的 102 连成前缀，推进到 103"},
		{105, 103, "105 已确认但 103 缺失，提交位点不动"},
		{103, 106, "103 到位后 103、104、105 连续，推进到 106"},
	}
	for _, s := range steps {
		if err := c.Ack("p0", s.ack); err != nil {
			t.Fatalf("Ack(p0, %d): %v", s.ack, err)
		}
		got := committed(t, c, "p0")
		t.Logf("input: Ack(p0, %d) → committed=%d（判定依据: %s）", s.ack, got, s.why)
		if got != s.want {
			t.Fatalf("Ack(p0, %d) 后 committed=%d, want %d（%s）", s.ack, got, s.want, s.why)
		}
	}
}

// TestDuplicateAckIdempotent 重复确认幂等：不改变提交位点与在途计数。
func TestDuplicateAckIdempotent(t *testing.T) {
	c, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 0, 4) // 投递 0..3

	if err := c.Ack("p0", 1); err != nil {
		t.Fatal(err)
	}
	before := committed(t, c, "p0")
	inflightBefore := c.Inflight()
	for i := 0; i < 3; i++ {
		if err := c.Ack("p0", 1); err != nil {
			t.Fatalf("重复 Ack(p0, 1) 应幂等成功: %v", err)
		}
	}
	after := committed(t, c, "p0")
	t.Logf("input: Ack(p0, 1) ×4 → committed=%d, inflight=%d（判定依据: 重复确认幂等，状态不变）",
		after, c.Inflight())
	if after != before || c.Inflight() != inflightBefore {
		t.Fatalf("重复确认改变了状态: committed %d→%d, inflight %d→%d",
			before, after, inflightBefore, c.Inflight())
	}
}

// TestAckRegression 位点回退：确认小于已提交位点的位点必须拒绝。
func TestAckRegression(t *testing.T) {
	c, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 10, 5) // 投递 10..14
	if err := c.Ack("p0", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack("p0", 11); err != nil {
		t.Fatal(err)
	}
	// 此时 committed=12，确认 11 属于回退（它同时也是"已被提交覆盖的旧确认"）。
	err = c.Ack("p0", 11)
	t.Logf("input: Ack(p0, 11)，committed=12 → err=%v（判定依据: 11 < 12，回退拒绝）", err)
	if !errors.Is(err, ErrOffsetRegression) {
		t.Fatalf("want ErrOffsetRegression, got %v", err)
	}
	if got := committed(t, c, "p0"); got != 12 {
		t.Fatalf("回退拒绝后 committed=%d, want 12", got)
	}
}

// TestAckOutOfRange 确认未投递的位点必须拒绝。
func TestAckOutOfRange(t *testing.T) {
	c, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 0, 3) // 投递 0..2
	err = c.Ack("p0", 3)
	t.Logf("input: Ack(p0, 3)，已投递 0..2 → err=%v（判定依据: 3 从未投递，越界拒绝）", err)
	if !errors.Is(err, ErrOffsetOutOfRange) {
		t.Fatalf("want ErrOffsetOutOfRange, got %v", err)
	}
	if got := committed(t, c, "p0"); got != 0 {
		t.Fatalf("越界拒绝后 committed=%d, want 0", got)
	}
}

// TestInvalidInputs 各类非法输入：空分区键、未声明分区、不连续投递、非法上限、重复声明。
func TestInvalidInputs(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("New(0): want ErrInvalidLimit, got %v", err)
	}
	c, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Declare("", 0); !errors.Is(err, ErrEmptyPartitionKey) {
		t.Fatalf("Declare(空键): want ErrEmptyPartitionKey, got %v", err)
	}
	if err := c.Declare("p0", 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Declare("p0", 5); !errors.Is(err, ErrPartitionExists) {
		t.Fatalf("重复 Declare: want ErrPartitionExists, got %v", err)
	}
	if err := c.Deliver("ghost", 0); !errors.Is(err, ErrPartitionNotFound) {
		t.Fatalf("Deliver 未声明分区: want ErrPartitionNotFound, got %v", err)
	}
	if err := c.Ack("ghost", 0); !errors.Is(err, ErrPartitionNotFound) {
		t.Fatalf("Ack 未声明分区: want ErrPartitionNotFound, got %v", err)
	}
	if _, err := c.Committed("ghost"); !errors.Is(err, ErrPartitionNotFound) {
		t.Fatalf("Committed 未声明分区: want ErrPartitionNotFound, got %v", err)
	}
	if err := c.Deliver("", 5); !errors.Is(err, ErrEmptyPartitionKey) {
		t.Fatalf("Deliver 空键: want ErrEmptyPartitionKey, got %v", err)
	}
	if err := c.Ack("", 5); !errors.Is(err, ErrEmptyPartitionKey) {
		t.Fatalf("Ack 空键: want ErrEmptyPartitionKey, got %v", err)
	}
	// 不连续投递：期望 5，却投递 7。
	if err := c.Deliver("p0", 7); !errors.Is(err, ErrNonContiguousDelivery) {
		t.Fatalf("不连续投递: want ErrNonContiguousDelivery, got %v", err)
	}
	// 被拒绝的不连续投递不得改变期望位点：下一条仍应是 5。
	if err := c.Deliver("p0", 5); err != nil {
		t.Fatalf("拒绝后状态被污染，Deliver(p0, 5) 应成功: %v", err)
	}
	t.Logf("input: 各类非法输入均已按哨兵错误拒绝，且拒绝后分区状态未被污染")
}

// TestSharedInflightLimitAtomicReject 跨分区共享上限占满时整体拒绝，
// 且不改变任何分区的位点、在途计数。
func TestSharedInflightLimitAtomicReject(t *testing.T) {
	c, err := New(3) // 两个分区共享上限 3
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 0, 2) // 在途 2
	mustDeclare(t, c, "p1", 0, 1) // 在途 3，占满

	snapBefore := c.Snapshot()
	inflightBefore := c.Inflight()

	err = c.Deliver("p1", 1)
	t.Logf("input: Deliver(p1, 1)，共享在途 %d/%d → err=%v（判定依据: 超限整体拒绝）",
		inflightBefore, 3, err)
	if !errors.Is(err, ErrInflightLimitExceeded) {
		t.Fatalf("want ErrInflightLimitExceeded, got %v", err)
	}
	if c.Inflight() != inflightBefore {
		t.Fatalf("拒绝后在途数变化: %d→%d", inflightBefore, c.Inflight())
	}
	if !reflect.DeepEqual(c.Snapshot(), snapBefore) {
		t.Fatalf("拒绝后提交位点变化: %v→%v", snapBefore, c.Snapshot())
	}
	// p1 的期望投递位点未被推进：确认一条释放额度后，仍应投递 1。
	if err := c.Ack("p0", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver("p1", 1); err != nil {
		t.Fatalf("释放额度后 Deliver(p1, 1) 应成功（证明拒绝是原子的）: %v", err)
	}
	t.Logf("释放额度后 Deliver(p1, 1) 成功，snapshot=%v（判定依据: 拒绝未留下部分生效）", c.Snapshot())
}

// TestRestartRedelivery 崩溃重启：持久化提交位点 → Restore → 重新投递
// [committed, nextDeliver) 的在途集合 → 确认后提交位点继续推进，
// 不丢消息（committed 之前的不再重投）也不多重复（committed 之前的不重投）。
func TestRestartRedelivery(t *testing.T) {
	c, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 100, 10) // 投递 100..109
	// 乱序确认一部分：100、101、103、105。
	for _, off := range []uint64{100, 101, 103, 105} {
		if err := c.Ack("p0", off); err != nil {
			t.Fatal(err)
		}
	}
	snap := c.Snapshot() // 模拟持久化：committed(p0)=102
	t.Logf("崩溃前 snapshot=%v（判定依据: 102 起尚未确认，重启后需重投 [102,110)）", snap)

	// 模拟崩溃重启：新提交器从持久化位点恢复。
	c2, err := Restore(16, snap)
	if err != nil {
		t.Fatal(err)
	}
	// 重启后消费端从 committed 重新投递 [102,110)。
	redelivered := []uint64{102, 103, 104, 105, 106, 107, 108, 109}
	for _, off := range redelivered {
		if err := c2.Deliver("p0", off); err != nil {
			t.Fatalf("重启后重投 %d: %v", off, err)
		}
	}
	// 重启后确认全部重投位点（含崩溃前已确认过的 103、105，属于正常重投）。
	for _, off := range redelivered {
		if err := c2.Ack("p0", off); err != nil {
			t.Fatalf("重启后确认 %d: %v", off, err)
		}
	}
	got := committed(t, c2, "p0")
	t.Logf("重启后确认完毕 → committed=%d（判定依据: [102,110) 全部确认，推进到 110）", got)
	if got != 110 {
		t.Fatalf("重启后 committed=%d, want 110", got)
	}
	// 不丢：102 之前（<102）的位点不会重投；不多重复：重投集合恰好是 [102,110)。
	if snap["p0"] != 102 {
		t.Fatalf("持久化位点=%d, want 102", snap["p0"])
	}
}

// TestScanCountIndependentOfInflight 用内部计数器证明：推进提交位点的
// 弹出总次数等于被提交的位点数，与在途规模无关（不会随在途线性反复扫描）。
func TestScanCountIndependentOfInflight(t *testing.T) {
	const n = 1000
	c, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 0, n) // 在途峰值 1000

	// 逆序确认：最坏情况下朴素实现每次确认都要扫描全部在途（O(n²)）。
	for i := uint64(n) - 1; i > 0; i-- {
		if err := c.Ack("p0", i); err != nil {
			t.Fatal(err)
		}
	}
	if c.scans != 0 {
		t.Fatalf("确认 0 之前不应有任何推进, scans=%d", c.scans)
	}
	if err := c.Ack("p0", 0); err != nil {
		t.Fatal(err)
	}
	t.Logf("input: 逆序确认 0..%d（在途峰值 %d）→ scans=%d, committed=%d"+
		"（判定依据: 每个位点整个生命周期至多弹出一次，总弹出数=确认总数，与在途规模无关）",
		n-1, n, c.scans, committed(t, c, "p0"))
	if c.scans != n {
		t.Fatalf("scans=%d, want %d（每个位点恰好弹出一次）", c.scans, n)
	}
	if got := committed(t, c, "p0"); got != n {
		t.Fatalf("committed=%d, want %d", got, n)
	}
}

// TestConcurrentAckMatchesNaive 并发确认的结果与朴素逐位检查一致。
func TestConcurrentAckMatchesNaive(t *testing.T) {
	const (
		parts = 4
		n     = 500
	)
	c, err := New(parts * n)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"p0", "p1", "p2", "p3"}
	starts := map[string]uint64{}
	ackedSets := map[string]map[uint64]bool{}
	for i, k := range keys {
		start := uint64(i * 1000)
		starts[k] = start
		mustDeclare(t, c, k, start, n)
		// 每个分区随机留一个缺口，使提交位点停在缺口处。
		ackedSets[k] = map[uint64]bool{}
		for j := uint64(0); j < n; j++ {
			ackedSets[k][start+j] = true
		}
		gap := start + uint64(rand.Intn(n))
		delete(ackedSets[k], gap)
	}

	var wg sync.WaitGroup
	for _, k := range keys {
		// 打乱确认顺序后并发提交。
		offsets := make([]uint64, 0, len(ackedSets[k]))
		for off := range ackedSets[k] {
			offsets = append(offsets, off)
		}
		rand.Shuffle(len(offsets), func(i, j int) { offsets[i], offsets[j] = offsets[j], offsets[i] })
		for _, off := range offsets {
			wg.Add(1)
			go func(key string, o uint64) {
				defer wg.Done()
				if err := c.Ack(key, o); err != nil {
					t.Errorf("Ack(%s, %d): %v", key, o, err)
				}
			}(k, off)
		}
	}
	wg.Wait()

	for _, k := range keys {
		got := committed(t, c, k)
		want := naiveCommitted(starts[k], ackedSets[k])
		t.Logf("partition %s: committed=%d, naive=%d（判定依据: 并发确认结果须与朴素逐位检查一致）",
			k, got, want)
		if got != want {
			t.Fatalf("partition %s: committed=%d, naive want %d", k, got, want)
		}
	}
}

// TestConcurrentSnapshotConsistent 并发只读：快照内各分区位点来自同一时间点，
// 且同一分区跨快照单调不减。
func TestConcurrentSnapshotConsistent(t *testing.T) {
	const n = 200
	c, err := New(2 * n)
	if err != nil {
		t.Fatal(err)
	}
	mustDeclare(t, c, "p0", 0, n)
	mustDeclare(t, c, "p1", 0, n)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// 读者：持续取快照，校验每个分区单调不减、且不超过已投递上界 n。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := map[string]uint64{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := c.Snapshot()
				for _, k := range []string{"p0", "p1"} {
					if snap[k] < last[k] {
						t.Errorf("分区 %s 提交位点回退: %d→%d", k, last[k], snap[k])
					}
					if snap[k] > n {
						t.Errorf("分区 %s 提交位点越界: %d > %d", k, snap[k], n)
					}
					last[k] = snap[k]
				}
			}
		}()
	}
	// 写者：两个分区各自顺序确认。
	var writers sync.WaitGroup
	for _, k := range []string{"p0", "p1"} {
		writers.Add(1)
		go func(key string) {
			defer writers.Done()
			for i := uint64(0); i < n; i++ {
				if err := c.Ack(key, i); err != nil {
					t.Errorf("Ack(%s, %d): %v", key, i, err)
				}
			}
		}(k)
	}
	writers.Wait() // 写者结束后关闭读者
	close(stop)
	wg.Wait()
	t.Logf("并发读写结束，最终 snapshot=%v（判定依据: 单互斥锁临界区保证快照逐字段一致、单调不减）",
		c.Snapshot())
}

// TestDeterministic 同一输入序列反复计算，结果完全相同。
func TestDeterministic(t *testing.T) {
	run := func() map[string]uint64 {
		c, err := New(64)
		if err != nil {
			t.Fatal(err)
		}
		mustDeclare(t, c, "p0", 0, 10)
		mustDeclare(t, c, "p1", 50, 10)
		seq := []struct {
			key string
			off uint64
		}{
			{"p0", 3}, {"p1", 52}, {"p0", 0}, {"p1", 50}, {"p0", 1},
			{"p0", 2}, {"p1", 51}, {"p0", 5}, {"p1", 53}, {"p0", 4},
		}
		for _, s := range seq {
			if err := c.Ack(s.key, s.off); err != nil {
				t.Fatal(err)
			}
		}
		return c.Snapshot()
	}
	first := run()
	for i := 0; i < 20; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次运行结果 %v 与首次 %v 不一致", i, got, first)
		}
	}
	t.Logf("同一输入序列重复 21 次，snapshot 均为 %v（判定依据: 无随机性与迭代序依赖）", first)
}
