package coordinator

import (
	"fmt"
	"reflect"
	"testing"
)

// TestSplitRejections 分裂的拒绝次序：不存在 -> 已关闭 -> 分裂点越界。
func TestSplitRejections(t *testing.T) {
	c := New(100, 10, 4)

	_, _, err := c.Split(99, 50)
	checkErr(t, "Split(99,50)", err, ReasonShardNotFound)

	_, _, err = c.Split(0, 0)
	checkErr(t, "Split(0,0)", err, ReasonSplitPointInvalid)
	_, _, err = c.Split(0, 100)
	checkErr(t, "Split(0,100)", err, ReasonSplitPointInvalid)

	l, _, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)

	_, _, err = c.Split(0, 60)
	checkErr(t, "Split(0,60) again", err, ReasonShardClosed)

	_, _, err = c.Split(l, 50)
	checkErr(t, fmt.Sprintf("Split(%d,50) mid==hi", l), err, ReasonSplitPointInvalid)
	_, _, err = c.Split(l, 200)
	checkErr(t, fmt.Sprintf("Split(%d,200) mid>hi", l), err, ReasonSplitPointInvalid)

	snap, _ := c.Snapshot(l)
	if !snap.Open || snap.Appended != 0 || snap.Lo != 0 || snap.Hi != 50 {
		t.Fatalf("rejected splits must not change state: %+v", snap)
	}
}

// TestMergeRejections 合并的拒绝次序：不存在 -> 已关闭 -> 不相邻（含两参数相同）。
func TestMergeRejections(t *testing.T) {
	c := New(100, 10, 4)

	_, err := c.Merge(98, 99)
	checkErr(t, "Merge(98,99)", err, ReasonShardNotFound)

	l, r, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)

	_, err = c.Merge(l, 99)
	checkErr(t, fmt.Sprintf("Merge(%d,99)", l), err, ReasonShardNotFound)

	_, err = c.Merge(0, l)
	checkErr(t, fmt.Sprintf("Merge(0,%d)", l), err, ReasonShardClosed)
	_, err = c.Merge(l, 0)
	checkErr(t, fmt.Sprintf("Merge(%d,0)", l), err, ReasonShardClosed)

	_, err = c.Merge(l, l)
	checkErr(t, fmt.Sprintf("Merge(%d,%d) same", l, l), err, ReasonNotAdjacent)

	// 构造两个开放但不相邻的分片：[0,25) 与 [50,100)。
	m, err := c.Merge(l, r)
	mustOK(t, fmt.Sprintf("Merge(%d,%d)", l, r), err)
	a, b, err := c.Split(m, 50)
	mustOK(t, fmt.Sprintf("Split(%d,50)", m), err)
	a1, _, err := c.Split(a, 25)
	mustOK(t, fmt.Sprintf("Split(%d,25)", a), err)
	_, err = c.Merge(a1, b)
	checkErr(t, fmt.Sprintf("Merge(%d,%d) gap", a1, b), err, ReasonNotAdjacent)

	// 与参数次序无关：交换次序仍可合并。
	m2, err := c.Merge(b, a1)
	checkErr(t, fmt.Sprintf("Merge(%d,%d) gap reversed", b, a1), err, ReasonNotAdjacent)
	_ = m2
}

// TestMergeOrderIrrelevant 合并且相邻时与参数次序无关。
func TestMergeOrderIrrelevant(t *testing.T) {
	c := New(100, 10, 4)
	l, r, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)
	m, err := c.Merge(r, l)
	mustOK(t, fmt.Sprintf("Merge(%d,%d) reversed", r, l), err)
	snap, _ := c.Snapshot(m)
	if snap.Lo != 0 || snap.Hi != 100 {
		t.Fatalf("merged range = [%d,%d), want [0,100)", snap.Lo, snap.Hi)
	}
	t.Logf("input=Merge(%d,%d) output=%d 依据: 相邻与参数次序无关, 并集[0,100)", r, l, m)
}

// TestAcquireRejections 领取的拒绝次序：不存在 -> 已排空 -> 父未排空 -> 已有持有者 -> 工作者超限。
func TestAcquireRejections(t *testing.T) {
	c := New(100, 10, 1)

	_, err := c.Acquire(99, "w")
	checkErr(t, "Acquire(99,w)", err, ReasonShardNotFound)

	if _, _, err = c.Append(10); err != nil {
		t.Fatalf("Append: %v", err)
	}
	l, r, err := c.Split(0, 50)
	mustOK(t, "Split(0,50)", err)

	// 父分片0已关闭但 committed=0 < end=1，未排空。
	_, err = c.Acquire(l, "w1")
	re := checkErr(t, fmt.Sprintf("Acquire(%d,w1)", l), err, ReasonParentsNotDrained)
	if !reflect.DeepEqual(re.Parents, []int{0}) {
		t.Fatalf("undrained parents = %v, want [0]", re.Parents)
	}

	// 排空父分片。
	if _, err = c.Acquire(0, "w0"); err != nil {
		t.Fatalf("Acquire(0,w0): %v", err)
	}
	mustOK(t, "Commit(0,w0,1)", c.Commit(0, "w0", 1))

	// 已排空的分片不可领取。
	_, err = c.Acquire(0, "w0")
	checkErr(t, "Acquire(0,w0) drained", err, ReasonShardDrained)

	// 已有有效持有者。
	if _, err = c.Acquire(l, "w1"); err != nil {
		t.Fatalf("Acquire(%d,w1): %v", l, err)
	}
	_, err = c.Acquire(l, "w2")
	checkErr(t, fmt.Sprintf("Acquire(%d,w2) held", l), err, ReasonLeaseHeld)

	// 工作者超限（上限1，w1 已持有 l）；“已有持有者”先于“工作者超限”。
	_, err = c.Acquire(l, "w1")
	checkErr(t, fmt.Sprintf("Acquire(%d,w1) own-held before limit", l), err, ReasonLeaseHeld)
	_, err = c.Acquire(r, "w1")
	checkErr(t, fmt.Sprintf("Acquire(%d,w1) over limit", r), err, ReasonWorkerLimit)

	// 其他工作者不受 w1 上限影响。
	if _, err = c.Acquire(r, "w2"); err != nil {
		t.Fatalf("Acquire(%d,w2): %v", r, err)
	}
}

// TestCommitRenewRejections 续租与提交的拒绝：非有效持有者（含过期）、进度回退、超过已追加。
func TestCommitRenewRejections(t *testing.T) {
	c := New(10, 5, 2)

	if _, _, err := c.Append(1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, _, err := c.Append(1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	checkErr(t, "Commit(0,w1,1) no lease", c.Commit(0, "w1", 1), ReasonNotHolder)
	if _, err := c.Renew(0, "w1"); true {
		checkErr(t, "Renew(0,w1) no lease", err, ReasonNotHolder)
	}

	if _, err := c.Acquire(0, "w1"); err != nil {
		t.Fatalf("Acquire(0,w1): %v", err)
	}
	checkErr(t, "Commit(0,w2,1) not holder", c.Commit(0, "w2", 1), ReasonNotHolder)
	if _, err := c.Renew(0, "w2"); true {
		checkErr(t, "Renew(0,w2) not holder", err, ReasonNotHolder)
	}

	mustOK(t, "Commit(0,w1,1)", c.Commit(0, "w1", 1))
	checkErr(t, "Commit(0,w1,0) regress", c.Commit(0, "w1", 0), ReasonProgressRegress)
	checkErr(t, "Commit(0,w1,3) overflow", c.Commit(0, "w1", 3), ReasonProgressOverflow)
	mustOK(t, "Commit(0,w1,1) same progress ok", c.Commit(0, "w1", 1))

	// 过期后：续租与提交均被拒。
	mustOK(t, "AdvanceClock(5)", c.AdvanceClock(5))
	checkErr(t, "Commit(0,w1,2) expired", c.Commit(0, "w1", 2), ReasonNotHolder)
	if _, err := c.Renew(0, "w1"); true {
		checkErr(t, "Renew(0,w1) expired", err, ReasonNotHolder)
	}

	// 不存在的分片。
	checkErr(t, "Commit(99,w,0)", c.Commit(99, "w", 0), ReasonShardNotFound)
	if _, err := c.Renew(99, "w"); true {
		checkErr(t, "Renew(99,w)", err, ReasonShardNotFound)
	}

	snap, _ := c.Snapshot(0)
	if snap.Committed != 1 {
		t.Fatalf("rejected commits must not change progress: committed=%d", snap.Committed)
	}
}

// TestClockAndAppendRejections 时钟回退与键越界。
func TestClockAndAppendRejections(t *testing.T) {
	c := New(10, 5, 2)

	mustOK(t, "AdvanceClock(7)", c.AdvanceClock(7))
	mustOK(t, "AdvanceClock(7) same ok", c.AdvanceClock(7))
	checkErr(t, "AdvanceClock(6) regress", c.AdvanceClock(6), ReasonClockRegress)
	if got := c.Clock(); got != 7 {
		t.Fatalf("clock = %d after rejected advance, want 7", got)
	}

	_, _, err := c.Append(10)
	checkErr(t, "Append(10)", err, ReasonKeyOutOfRange)
	_, _, err = c.Append(-1)
	checkErr(t, "Append(-1)", err, ReasonKeyOutOfRange)
}
