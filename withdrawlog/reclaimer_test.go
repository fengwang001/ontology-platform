package withdrawlog

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// boundOf 是测试参照实现：依据存活快照水位集合计算回收上界，
// 与 Reclaimer.upperBoundLocked 的规则完全一致。
func boundOf(alive map[SnapshotID]int64, lastSeq int64) int64 {
	if len(alive) == 0 {
		return lastSeq
	}
	bound := lastSeq
	for _, w := range alive {
		if w < bound {
			bound = w
		}
	}
	return bound
}

// TestReclaimInclusiveBoundary 验证回收含等号边界：上界处记录被收，上界+1 保留。
func TestReclaimInclusiveBoundary(t *testing.T) {
	r := New(8)
	for i := 0; i < 5; i++ {
		r.Append(fmt.Sprintf("r%d", i+1))
	}

	s1, err := r.OpenSnapshot() // 水位 5
	if err != nil {
		t.Fatalf("OpenSnapshot: %v", err)
	}
	r.Append("r6")
	r.Append("r7")

	s2, err := r.OpenSnapshot() // 水位 7
	if err != nil {
		t.Fatalf("OpenSnapshot: %v", err)
	}

	bound := r.ReclaimUpperBound()
	t.Logf("输入: s1水位=5, s2水位=7; 结果: 回收上界=%d; 判定依据: 活跃快照水位最小值=min(5,7)=5", bound)
	if bound != 5 {
		t.Fatalf("上界应为 5，实际 %d", bound)
	}

	reclaimed := r.Reclaim()
	t.Logf("输入: Reclaim(); 结果: 回收至 %d（含等号）; 判定依据: seq<=5 全删，seq>5 保留", reclaimed)
	if reclaimed != 5 {
		t.Fatalf("回收返回值应为 5，实际 %d", reclaimed)
	}

	if _, err := r.Replay(s1, 5); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("seq=5（=上界）应返回 ErrReplayReclaimed，实际 %v", err)
	}
	if _, err := r.Replay(s1, 1); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("seq=1 应返回 ErrReplayReclaimed，实际 %v", err)
	}
	if rec, err := r.Replay(s2, 6); err != nil || rec.Payload != "r6" {
		t.Fatalf("seq=6 应可重放为 r6，实际 rec=%v err=%v", rec, err)
	}
	if rec, err := r.Replay(s2, 7); err != nil || rec.Payload != "r7" {
		t.Fatalf("seq=7 应可重放为 r7，实际 rec=%v err=%v", rec, err)
	}
	t.Logf("输入: Replay(s1,5)/Replay(s2,6..7); 结果: 等号位点拒绝、6/7 可重放; 判定依据: 回收区间含等号")
}

// TestReclaimAllWithoutSnapshots 验证无活跃快照时全部回收，且回收后永久不可重放。
func TestReclaimAllWithoutSnapshots(t *testing.T) {
	r := New(4)
	for i := 0; i < 4; i++ {
		r.Append(fmt.Sprintf("payload-%d", i+1))
	}

	bound := r.ReclaimUpperBound()
	t.Logf("输入: 无活跃快照, lastSeq=4; 结果: 上界=%d; 判定依据: 无活跃快照上界=当前最大序号", bound)
	if bound != 4 {
		t.Fatalf("无快照上界应为 4，实际 %d", bound)
	}
	if reclaimed := r.Reclaim(); reclaimed != 4 {
		t.Fatalf("无快照应全部回收至 4，实际 %d", reclaimed)
	}

	s, err := r.OpenSnapshot()
	if err != nil {
		t.Fatalf("OpenSnapshot: %v", err)
	}
	if _, err := r.Replay(s, 4); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("回收后的 seq=4 应永久不可重放，实际 %v", err)
	}
	t.Logf("输入: 回收后 OpenSnapshot + Replay(s,4); 结果: ErrReplayReclaimed; 判定依据: 回收不可逆")

	seq5 := r.Append("payload-5")
	// s 的水位固定为 4，存活期间上界仍为 4（不回退、也不错杀 seq=5）。
	if got := r.Reclaim(); got != 4 {
		t.Fatalf("s 存活时回收上界应保持 4，实际 %d", got)
	}
	if _, err := r.Replay(s, seq5); !errors.Is(err, ErrReplayOutOfRange) {
		t.Fatalf("seq=5 超过 s 的水位 4，应返回 ErrReplayOutOfRange，实际 %v", err)
	}
	// 关闭最后一个活跃快照后，上界跳到当前最大序号 5。
	if err := r.CloseSnapshot(s); err != nil {
		t.Fatalf("CloseSnapshot: %v", err)
	}
	if got := r.Reclaim(); got != seq5 {
		t.Fatalf("关闭快照后回收上界应为 %d，实际 %d", seq5, got)
	}
	t.Logf("输入: 追加 seq=5、s存活时 Reclaim、关闭s后 Reclaim; 结果: 上界先保持4再推进至5; 判定依据: 快照水位固定且水位单调不减")
}

// TestCloseReestimatesMinWatermark 验证关闭快照后最小水位被重估，回收上界随之变化。
func TestCloseReestimatesMinWatermark(t *testing.T) {
	r := New(8)
	r.Append("a")
	r.Append("b")

	low, err := r.OpenSnapshot() // 水位 2
	if err != nil {
		t.Fatalf("OpenSnapshot low: %v", err)
	}
	r.Append("c")
	high, err := r.OpenSnapshot() // 水位 3
	if err != nil {
		t.Fatalf("OpenSnapshot high: %v", err)
	}
	r.Append("d") // lastSeq = 4

	if got := r.ReclaimUpperBound(); got != 2 {
		t.Fatalf("min(2,3) 应为 2，实际 %d", got)
	}
	r.Reclaim()
	t.Logf("输入: 快照水位 {2,3}, lastSeq=4; 结果: 回收至 2; 判定依据: 活跃水位最小值")

	if err := r.CloseSnapshot(low); err != nil {
		t.Fatalf("CloseSnapshot low: %v", err)
	}
	if got := r.ReclaimUpperBound(); got != 3 {
		t.Fatalf("关闭低水位后上界应为 3，实际 %d", got)
	}
	if got := r.Reclaim(); got != 3 {
		t.Fatalf("第二次回收应至 3，实际 %d", got)
	}
	if _, err := r.Replay(high, 3); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("seq=3 此时应被回收，实际 %v", err)
	}
	t.Logf("输入: CloseSnapshot(水位2) 后 Reclaim(); 结果: 上界重估为 3 并回收; 判定依据: 存活快照最小水位重算")

	if err := r.CloseSnapshot(high); err != nil {
		t.Fatalf("CloseSnapshot high: %v", err)
	}
	if got := r.ReclaimUpperBound(); got != 4 {
		t.Fatalf("无活跃快照上界应为 4，实际 %d", got)
	}
	if got := r.Reclaim(); got != 4 {
		t.Fatalf("应收至 4，实际 %d", got)
	}
}

// TestInvalidInputsRejectedWithoutStateChange 覆盖四类非法输入，
// 校验返回互不相同的哨兵错误，且失败前后状态不变、被拒后仍可正常使用。
func TestInvalidInputsRejectedWithoutStateChange(t *testing.T) {
	r := New(2)
	r.Append("x")
	r.Append("y")

	s, err := r.OpenSnapshot() // 水位 2
	if err != nil {
		t.Fatalf("OpenSnapshot: %v", err)
	}
	// 先制造已回收位点：关闭快照后全量回收，再重开。
	if err := r.CloseSnapshot(s); err != nil {
		t.Fatalf("CloseSnapshot: %v", err)
	}
	r.Reclaim() // 1..2 全部回收

	r.Append("z") // seq=3
	s2, err := r.OpenSnapshot()
	if err != nil {
		t.Fatalf("OpenSnapshot s2: %v", err)
	}

	snapshotCount := len(r.snapshots)
	nextSeqBefore := r.nextSeq

	// 1) 重放越界：<=0 与 >水位。
	if _, err := r.Replay(s2, 0); !errors.Is(err, ErrReplayOutOfRange) {
		t.Fatalf("Replay(seq=0) 应返回 ErrReplayOutOfRange，实际 %v", err)
	}
	if _, err := r.Replay(s2, 4); !errors.Is(err, ErrReplayOutOfRange) {
		t.Fatalf("Replay(seq=4, 水位3) 应返回 ErrReplayOutOfRange，实际 %v", err)
	}
	t.Logf("输入: Replay(s2,0)、Replay(s2,4); 结果: ErrReplayOutOfRange; 判定依据: seq<=0 或 seq>快照水位")

	// 2) 重放已回收位点：seq=1 在水位 3 之内但已永久回收。
	if _, err := r.Replay(s2, 1); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("Replay(seq=1) 应返回 ErrReplayReclaimed，实际 %v", err)
	}
	t.Logf("输入: Replay(s2,1); 结果: ErrReplayReclaimed; 判定依据: seq<=watermark(2)")

	// 3) 关闭不存在的快照。
	missing := s2 + 100
	if err := r.CloseSnapshot(missing); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("CloseSnapshot(不存在) 应返回 ErrSnapshotNotFound，实际 %v", err)
	}
	t.Logf("输入: CloseSnapshot(%d); 结果: ErrSnapshotNotFound; 判定依据: 快照标识不在活跃集合", missing)

	// 4) 打开快照数超上限：maxSnapshots=2，当前 1 个，再开 1 个成功、第 2 个被拒。
	extra1, err := r.OpenSnapshot()
	if err != nil {
		t.Fatalf("OpenSnapshot extra1: %v", err)
	}
	if _, err := r.OpenSnapshot(); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("超限打开应返回 ErrTooManySnapshots，实际 %v", err)
	}
	t.Logf("输入: 已有 2 个活跃快照时 OpenSnapshot(); 结果: ErrTooManySnapshots; 判定依据: len(snapshots)>=maxSnapshots(2)")

	// 四类错误互不相同。
	errs := []error{ErrReplayOutOfRange, ErrReplayReclaimed, ErrSnapshotNotFound, ErrTooManySnapshots}
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("错误 %v 与 %v 必须可区分", errs[i], errs[j])
			}
		}
	}

	// 失败不改变状态：超限打开未占用标识、未推进任何序号。
	if len(r.snapshots) != snapshotCount+1 {
		t.Fatalf("被拒后快照数异常: 期望 %d，实际 %d", snapshotCount+1, len(r.snapshots))
	}
	if r.nextSeq != nextSeqBefore {
		t.Fatalf("被拒后 nextSeq 被改动: %d vs %d", r.nextSeq, nextSeqBefore)
	}

	// 被拒后仍可继续正常使用。
	if err := r.CloseSnapshot(extra1); err != nil {
		t.Fatalf("被拒后 CloseSnapshot 应正常，实际 %v", err)
	}
	if seq := r.Append("w"); seq != 4 {
		t.Fatalf("被拒后追加序号应为 4，实际 %d", seq)
	}
	if _, err := r.Replay(s2, 3); err != nil {
		t.Fatalf("被拒后 Replay(s2,3) 应正常，实际 %v", err)
	}
	if got := r.ReclaimUpperBound(); got != 3 {
		t.Fatalf("s2 仍存活，上界应为 3，实际 %d", got)
	}
	t.Logf("输入: 被拒后追加/重放/查询上界; 结果: 全部正常; 判定依据: 失败路径无状态写入")
}

// TestReplayOnUnknownSnapshot 重放不存在的快照同样被明确拒绝。
func TestReplayOnUnknownSnapshot(t *testing.T) {
	r := New(2)
	r.Append("m")
	if _, err := r.Replay(SnapshotID(999), 1); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("重放不存在快照应返回 ErrSnapshotNotFound，实际 %v", err)
	}
	t.Logf("输入: Replay(不存在快照,1); 结果: ErrSnapshotNotFound; 判定依据: 快照标识无效")
}

// TestConcurrentSnapshotsMatchReference 并发打开/关闭快照并穿插追加，
// 每个变更线性化点的回收上界必须与顺序参照实现一致。
func TestConcurrentSnapshotsMatchReference(t *testing.T) {
	const maxSnap = 32
	r := New(maxSnap)

	// stepMu 将每个写步骤（append/open/close）与其参照更新绑定为同一个线性化点；
	// 上界读者不持有 stepMu，从而真正并发地检验可线性化。
	var stepMu sync.Mutex
	refAlive := map[SnapshotID]int64{}
	var refLastSeq int64
	totalOpens := 0

	var readers sync.WaitGroup
	readersDone := make(chan struct{})
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-readersDone:
					return
				default:
				}
				got := r.ReclaimUpperBound()
				stepMu.Lock()
				last := refLastSeq
				stepMu.Unlock()
				// 读者读到的必是某个合法线性化点的值：非负且不超过当前最大序号。
				if got < 0 || got > last {
					t.Errorf("回收上界越界: got=%d lastSeq=%d", got, last)
				}
			}
		}()
	}

	var wg sync.WaitGroup
	const writers = 64
	const opensEach = 4
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()

			// 每个 goroutine 只追加自己的一条记录。
			stepMu.Lock()
			r.Append(fmt.Sprintf("g%d", g))
			refLastSeq++
			if refLastSeq != r.lastSeq {
				t.Errorf("追加后参照 lastSeq=%d 与实现 %d 不一致", refLastSeq, r.lastSeq)
			}
			stepMu.Unlock()

			for k := 0; k < opensEach; k++ {
				var id SnapshotID
				for {
					stepMu.Lock()
					gotID, err := r.OpenSnapshot()
					if errors.Is(err, ErrTooManySnapshots) {
						if len(refAlive) < maxSnap {
							t.Errorf("实现判定超限但参照未满")
						}
						if got := r.ReclaimUpperBound(); got != boundOf(refAlive, refLastSeq) {
							t.Errorf("超限行上界不一致: got=%d want=%d", got, boundOf(refAlive, refLastSeq))
						}
						stepMu.Unlock()
						runtime.Gosched()
						continue
					}
					if err != nil {
						stepMu.Unlock()
						t.Errorf("OpenSnapshot: %v", err)
						return
					}
					if len(refAlive) >= maxSnap {
						stepMu.Unlock()
						t.Errorf("实现打开成功但参照已满")
						return
					}
					id = gotID
					// 快照水位 = 打开这一刻的当前最大序号。
					refAlive[id] = refLastSeq
					totalOpens++
					if got := r.ReclaimUpperBound(); got != boundOf(refAlive, refLastSeq) {
						stepMu.Unlock()
						t.Errorf("打开后上界不一致: got=%d want=%d", got, boundOf(refAlive, refLastSeq))
						return
					}
					stepMu.Unlock()
					break
				}

				runtime.Gosched()

				stepMu.Lock()
				if err := r.CloseSnapshot(id); err != nil {
					stepMu.Unlock()
					t.Errorf("CloseSnapshot(%d): %v", id, err)
					return
				}
				delete(refAlive, id)
				if got := r.ReclaimUpperBound(); got != boundOf(refAlive, refLastSeq) {
					stepMu.Unlock()
					t.Errorf("关闭后上界不一致: got=%d want=%d", got, boundOf(refAlive, refLastSeq))
					return
				}
				stepMu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	close(readersDone)
	readers.Wait()

	stepMu.Lock()
	want := boundOf(refAlive, refLastSeq)
	alive := len(refAlive)
	stepMu.Unlock()
	if want != writers || alive != 0 || totalOpens != writers*opensEach {
		t.Fatalf("参照终态异常: bound=%d alive=%d opens=%d", want, alive, totalOpens)
	}
	if got := r.ReclaimUpperBound(); got != writers {
		t.Fatalf("全部快照关闭后上界应为 %d，实际 %d", writers, got)
	}
	if got := r.Reclaim(); got != writers {
		t.Fatalf("全部快照关闭后应回收至 %d，实际 %d", writers, got)
	}
	t.Logf("输入: %d 个 goroutine 各 Append+Open/Close×%d（上限%d）+ 4 个并发上界读者; 结果: 每步上界与顺序参照一致、终态回收至 %d; 判定依据: 变更步骤与参照 boundOf(存活快照水位集合,lastSeq) 在同一临界区", writers, opensEach, maxSnap, writers)
}

// TestConcurrentReclaimAndReplay 回收与重放并发执行：
// 已回收位点必须返回 ErrReplayReclaimed，未回收位点必须读到完整数据，绝不出现撕裂。
func TestConcurrentReclaimAndReplay(t *testing.T) {
	r := New(8)
	const earlyBound = 50
	const n = 200
	for i := 0; i < earlyBound; i++ {
		r.Append(fmt.Sprintf("p%d", i+1))
	}

	// 在追加到 50 时打开 early 快照，水位即 50。
	early, err := r.OpenSnapshot()
	if err != nil {
		t.Fatalf("OpenSnapshot early: %v", err)
	}
	for i := earlyBound; i < n; i++ {
		r.Append(fmt.Sprintf("p%d", i+1))
	}
	// full 快照水位为 n。
	full, err := r.OpenSnapshot()
	if err != nil {
		t.Fatalf("OpenSnapshot full: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 重放者：在 full 快照上重放 51..n（这些位点在上界推进到 n 之前始终可读）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for seq := int64(51); seq <= n; seq++ {
					rec, err := r.Replay(full, seq)
					if errors.Is(err, ErrReplayReclaimed) {
						// 回收推进后允许，继续其它位点。
						continue
					}
					if err != nil {
						t.Errorf("Replay(full,%d): %v", seq, err)
						return
					}
					want := fmt.Sprintf("p%d", seq)
					if rec.Payload != want || rec.Seq != seq {
						t.Errorf("重放数据不一致 seq=%d: %+v", seq, rec)
						return
					}
				}
			}
		}()
	}

	// 回收推进：先在 early 存活时回收（上界 50），再关闭 early 回收剩余。
	wg.Add(1)
	go func() {
		defer wg.Done()
		if got := r.Reclaim(); got != 50 {
			t.Errorf("early 存活时回收上界应为 50，实际 %d", got)
		}
		if _, err := r.Replay(full, 50); !errors.Is(err, ErrReplayReclaimed) {
			t.Errorf("seq=50 回收后应不可重放，实际 %v", err)
		}
		if err := r.CloseSnapshot(early); err != nil {
			t.Errorf("CloseSnapshot early: %v", err)
		}
		if got := r.Reclaim(); got != n {
			t.Errorf("关闭 early 后回收上界应为 %d，实际 %d", n, got)
		}
		close(stop)
	}()

	<-stop
	wg.Wait()
	t.Logf("输入: 4 个重放者与回收/关闭并发（快照水位 50、%d）; 结果: 可读位点数据完整、回收位点稳定报错; 判定依据: 读锁保证重放与回收互斥且结果二选一", n)
}
