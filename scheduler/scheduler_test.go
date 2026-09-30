package scheduler_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ontology/scheduler"
)

// newScheduled 构造一个把判定日志写入 buf 的调度器，便于检查输入、输出与判定依据。
func newScheduled(t *testing.T, k int) (*scheduler.Scheduler, *bytes.Buffer) {
	t.Helper()
	var logBuf bytes.Buffer
	return scheduler.New(k, scheduler.WithLogger(&logBuf)), &logBuf
}

func mustArrive(t *testing.T, s *scheduler.Scheduler, wantID int, r, w []string) []int {
	t.Helper()
	id, released, err := s.Arrive(r, w)
	if err != nil {
		t.Fatalf("Arrive #%d unexpected error: %v", wantID, err)
	}
	if id != wantID {
		t.Fatalf("Arrive id = %d, want %d", id, wantID)
	}
	return released
}

func assertStatus(t *testing.T, s *scheduler.Scheduler, id int, want string) {
	t.Helper()
	got, ok := s.Status(id)
	if !ok || got != want {
		t.Fatalf("txn %d status = %q (ok=%v), want %q", id, got, ok, want)
	}
}

func eqInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 读者在等待的写者之后到达：即使它与运行中的事务都不冲突，也必须排在仍未完成的
// 更早写者之后等待，任何事务都不得越过仍未完成的冲突事务。
func TestReaderWaitsBehindPendingWriter(t *testing.T) {
	s, logBuf := newScheduled(t, 3)

	mustArrive(t, s, 1, nil, []string{"a"}) // 写者 T1 运行
	mustArrive(t, s, 2, nil, []string{"b"}) // 写者 T2 运行
	mustArrive(t, s, 3, nil, []string{"c"}) // 写者 T3 运行，占满 K=3
	if rel := mustArrive(t, s, 4, nil, []string{"d"}); len(rel) != 0 {
		t.Fatalf("T4 should be held by limit, released=%v", rel)
	}
	// T5 只读 d：与运行中的 T1/T2/T3 均不冲突，唯一阻挡它的是仍在等待的更早写者 T4。
	if rel := mustArrive(t, s, 5, []string{"d"}, nil); len(rel) != 0 {
		t.Fatalf("T5 must not jump over pending writer T4, released=%v", rel)
	}
	assertStatus(t, s, 4, scheduler.StatusWaiting)
	assertStatus(t, s, 5, scheduler.StatusWaiting)

	blockers, err := s.Blockers(5)
	if err != nil {
		t.Fatalf("Blockers(5): %v", err)
	}
	if !eqInts(blockers, []int{4}) {
		t.Fatalf("Blockers(5) = %v, want [4]", blockers)
	}

	// T1 完成腾出名额：取批只放行 T4；T5 与 T4 冲突，同批内仍不能越过它。
	released, err := s.Complete(1)
	if err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if !eqInts(released, []int{4}) {
		t.Fatalf("after Complete(1) released=%v, want [4]", released)
	}
	assertStatus(t, s, 4, scheduler.StatusRunning)
	assertStatus(t, s, 5, scheduler.StatusWaiting)

	// T4 完成后 T5 才放行：冲突对的放行先后等于到达先后。
	released, err = s.Complete(4)
	if err != nil {
		t.Fatalf("Complete(4): %v", err)
	}
	if !eqInts(released, []int{5}) {
		t.Fatalf("after Complete(4) released=%v, want [5]", released)
	}

	logText := logBuf.String()
	for _, want := range []string{
		"arrive id=5",
		"stop scan before id=4", // 到达时已满，取批在队首即停止；T5 不被越过规则评估
		"release id=4",          // T1 完成后队首 T4 先放行，随后再次达上限停止
		"blockers id=5 blockers=[4]",
		"release id=5",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, logText)
		}
	}
	t.Logf("判定依据日志（读者不越过等待写者）:\n%s", logText)
}

// 被并发上限挡下的更早事务（本身与运行集无冲突）仍阻挡后来的冲突者。
func TestLimitedEarlierTxnBlocksLaterConflict(t *testing.T) {
	s, _ := newScheduled(t, 2)

	mustArrive(t, s, 1, nil, []string{"a"}) // T1 运行
	mustArrive(t, s, 2, nil, []string{"b"}) // T2 运行，占满 K=2
	mustArrive(t, s, 3, nil, []string{"x"}) // T3 与 T1/T2 不冲突，被上限挡下
	mustArrive(t, s, 4, []string{"x"}, nil) // T4 与 T1/T2 不冲突，与等待中的 T3 冲突

	blockers, err := s.Blockers(4)
	if err != nil {
		t.Fatalf("Blockers(4): %v", err)
	}
	if !eqInts(blockers, []int{3}) {
		t.Fatalf("Blockers(4) = %v, want [3]", blockers)
	}

	released, err := s.Complete(1)
	if err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if !eqInts(released, []int{3}) {
		t.Fatalf("released=%v, want [3] only; T4 must not pass T3", released)
	}
	assertStatus(t, s, 4, scheduler.StatusWaiting)
}

// 取消等待中的事务后阻挡立即解除；名额受限时继续等待，一旦有名额即放行。
func TestCancelReleasesBlockedWaiters(t *testing.T) {
	s, logBuf := newScheduled(t, 2)

	mustArrive(t, s, 1, nil, []string{"a"}) // T1 运行
	mustArrive(t, s, 2, nil, []string{"b"}) // T2 运行
	mustArrive(t, s, 3, nil, []string{"c"}) // T3 等待（上限）
	mustArrive(t, s, 4, []string{"c"}, nil) // T4 等待，阻塞者 T3

	// 取消 T3：它不再阻挡 T4，但运行名额仍满，T4 继续受上限约束。
	released, err := s.Cancel(3)
	if err != nil {
		t.Fatalf("Cancel(3): %v", err)
	}
	if !eqInts(released, nil) {
		t.Fatalf("released after cancel=%v, want none (limit full)", released)
	}
	assertStatus(t, s, 3, scheduler.StatusCompleted)

	blockers, err := s.Blockers(4)
	if err != nil {
		t.Fatalf("Blockers(4): %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("T4 blockers after cancel = %v, want none", blockers)
	}

	released, err = s.Complete(1)
	if err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if !eqInts(released, []int{4}) {
		t.Fatalf("released=%v, want [4]", released)
	}

	logText := logBuf.String()
	if !strings.Contains(logText, "cancel id=3") {
		t.Fatalf("log missing cancel record:\n%s", logText)
	}
	t.Logf("取消等待者后阻挡解除日志:\n%s", logText)
}

// 完成触发连锁放行：一个写者完成后，一批互不冲突、仅被它阻挡的读者同批放行；
// 而与其中某个新放行读者冲突的后来写者，仍须在该读者完成后才能放行。
func TestCompleteChainRelease(t *testing.T) {
	s, logBuf := newScheduled(t, 5)

	mustArrive(t, s, 1, nil, []string{"a", "b", "c"}) // T1 运行
	mustArrive(t, s, 2, []string{"a"}, nil)           // T2 等待，阻塞者 T1
	mustArrive(t, s, 3, []string{"b"}, nil)           // T3 等待，阻塞者 T1；与 T2 只读共享
	mustArrive(t, s, 4, []string{"c"}, nil)           // T4 等待，阻塞者 T1
	mustArrive(t, s, 5, nil, []string{"b"})           // T5 等待，阻塞者 T1 与 T3(R b)

	for _, id := range []int{2, 3, 4, 5} {
		assertStatus(t, s, id, scheduler.StatusWaiting)
	}

	// T1 完成：T2/T3/T4 与所有更早未完成者均不冲突，同批按序放行；
	// T5 写 b，与本批刚放行的 T3 冲突，继续等待（不越过同批更早者）。
	released, err := s.Complete(1)
	if err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if !eqInts(released, []int{2, 3, 4}) {
		t.Fatalf("chain released=%v, want [2 3 4]", released)
	}
	assertStatus(t, s, 5, scheduler.StatusWaiting)
	blockers, err := s.Blockers(5)
	if err != nil {
		t.Fatalf("Blockers(5): %v", err)
	}
	if !eqInts(blockers, []int{3}) {
		t.Fatalf("Blockers(5) = %v, want [3] (released earlier in same batch)", blockers)
	}

	released, err = s.Complete(3)
	if err != nil {
		t.Fatalf("Complete(3): %v", err)
	}
	if !eqInts(released, []int{5}) {
		t.Fatalf("after Complete(3) released=%v, want [5]", released)
	}

	logText := logBuf.String()
	for _, want := range []string{
		"hold id=5 blockers=[1 3]",
		"release id=2",
		"release id=3",
		"release id=4",
		"hold id=5 blockers=[3]",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, logText)
		}
	}
	t.Logf("完成触发连锁放行日志:\n%s", logText)
}

// 只读事务之间不冲突：多个读者即使读同一个键也可同批/并行放行。
func TestReadOnlyDoNotConflict(t *testing.T) {
	s, _ := newScheduled(t, 3)

	mustArrive(t, s, 1, []string{"a"}, nil)
	rel := mustArrive(t, s, 2, []string{"a", "b"}, nil)
	if !eqInts(rel, []int{2}) {
		t.Fatalf("reader after reader released=%v, want [2] (read/read never conflicts)", rel)
	}
	rel = mustArrive(t, s, 3, []string{"b"}, nil)
	if !eqInts(rel, []int{3}) {
		t.Fatalf("third reader released=%v, want [3]", rel)
	}
}

// 到达校验：读写集皆空优先；其次空串键；拒绝不分配编号、不改变任何状态。
func TestArriveValidation(t *testing.T) {
	s, _ := newScheduled(t, 2)

	if _, _, err := s.Arrive(nil, nil); !errors.Is(err, scheduler.ErrEmptyReadWriteSet) {
		t.Fatalf("nil/nil err=%v, want ErrEmptyReadWriteSet", err)
	}
	if _, _, err := s.Arrive([]string{}, []string{}); !errors.Is(err, scheduler.ErrEmptyReadWriteSet) {
		t.Fatalf("empty slices err=%v, want ErrEmptyReadWriteSet", err)
	}
	if _, _, err := s.Arrive([]string{"x", ""}, []string{"y"}); !errors.Is(err, scheduler.ErrEmptyKey) {
		t.Fatalf("empty read key err=%v, want ErrEmptyKey", err)
	}
	if _, _, err := s.Arrive([]string{"y"}, []string{"x", ""}); !errors.Is(err, scheduler.ErrEmptyKey) {
		t.Fatalf("empty write key err=%v, want ErrEmptyKey", err)
	}
	// 写集非空但含空串时报“空串键”，而非“皆空”。
	if _, _, err := s.Arrive(nil, []string{""}); !errors.Is(err, scheduler.ErrEmptyKey) {
		t.Fatalf("write empty key err=%v, want ErrEmptyKey", err)
	}

	id, _, err := s.Arrive(nil, []string{"a"})
	if err != nil {
		t.Fatalf("Arrive after rejections: %v", err)
	}
	if id != 1 {
		t.Fatalf("id after rejected arrivals = %d, want 1 (rejection must not allocate id)", id)
	}
}

// 完成校验顺序：编号不存在 → 已完成 → 并非运行中；拒绝不改变状态。
func TestCompleteValidation(t *testing.T) {
	s, _ := newScheduled(t, 1)

	if _, err := s.Complete(42); !errors.Is(err, scheduler.ErrIDNotFound) {
		t.Fatalf("complete missing err=%v, want ErrIDNotFound", err)
	}
	mustArrive(t, s, 1, nil, []string{"a"})
	mustArrive(t, s, 2, nil, []string{"b"}) // 等待：与 T1 不冲突但受 K=1 限制

	if _, err := s.Complete(2); !errors.Is(err, scheduler.ErrNotRunning) {
		t.Fatalf("complete waiting err=%v, want ErrNotRunning", err)
	}
	if _, err := s.Complete(1); err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	assertStatus(t, s, 1, scheduler.StatusCompleted)
	// 完成 T1 的同批取批已放行 T2，完成它后再验证重复完成报“已完成”。
	if _, err := s.Complete(2); err != nil {
		t.Fatalf("Complete(2): %v", err)
	}
	if _, err := s.Complete(1); !errors.Is(err, scheduler.ErrAlreadyCompleted) {
		t.Fatalf("re-complete err=%v, want ErrAlreadyCompleted", err)
	}
	if _, err := s.Complete(99); !errors.Is(err, scheduler.ErrIDNotFound) {
		t.Fatalf("complete unknown err=%v, want ErrIDNotFound", err)
	}
}

// 取消校验顺序：编号不存在 → 已完成 → 正在运行；等待者可取消且不再阻挡他人。
func TestCancelValidation(t *testing.T) {
	s, _ := newScheduled(t, 1)

	if _, err := s.Cancel(7); !errors.Is(err, scheduler.ErrIDNotFound) {
		t.Fatalf("cancel missing err=%v, want ErrIDNotFound", err)
	}
	mustArrive(t, s, 1, nil, []string{"a"})
	mustArrive(t, s, 2, nil, []string{"a"}) // 等待，阻塞者 T1

	if _, err := s.Cancel(1); !errors.Is(err, scheduler.ErrRunning) {
		t.Fatalf("cancel running err=%v, want ErrRunning", err)
	}
	if _, err := s.Cancel(2); err != nil {
		t.Fatalf("Cancel(2) waiting: %v", err)
	}
	assertStatus(t, s, 2, scheduler.StatusCompleted)
	if _, err := s.Cancel(2); !errors.Is(err, scheduler.ErrAlreadyCompleted) {
		t.Fatalf("re-cancel err=%v, want ErrAlreadyCompleted", err)
	}

	// T1 完成时取批：T2 已取消、不在等待队列，不得被放行。
	released, err := s.Complete(1)
	if err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	if !eqInts(released, nil) {
		t.Fatalf("released=%v, want none (T2 cancelled, nothing waiting)", released)
	}
}

// Blockers 仅对等待中的事务合法；返回结果按编号升序。
func TestBlockersQuery(t *testing.T) {
	s, _ := newScheduled(t, 1)

	if _, err := s.Blockers(1); !errors.Is(err, scheduler.ErrIDNotFound) {
		t.Fatalf("blockers missing err=%v, want ErrIDNotFound", err)
	}
	mustArrive(t, s, 1, nil, []string{"a"})
	mustArrive(t, s, 2, nil, []string{"a"}) // 等待，阻塞者 1
	mustArrive(t, s, 3, []string{"a"}, nil) // 等待，阻塞者 1、2

	if b, err := s.Blockers(2); err != nil || !eqInts(b, []int{1}) {
		t.Fatalf("Blockers(2)=%v err=%v, want [1]", b, err)
	}
	if b, err := s.Blockers(3); err != nil || !eqInts(b, []int{1, 2}) {
		t.Fatalf("Blockers(3)=%v err=%v, want [1 2]", b, err)
	}
	if _, err := s.Blockers(1); !errors.Is(err, scheduler.ErrRunning) {
		t.Fatalf("blockers running err=%v, want ErrRunning", err)
	}
	if _, err := s.Complete(1); err != nil {
		t.Fatalf("Complete(1): %v", err)
	}
	// T2 被同批放行，T3 仍等待且阻塞者仅剩 T2。
	if b, err := s.Blockers(3); err != nil || !eqInts(b, []int{2}) {
		t.Fatalf("Blockers(3)=%v err=%v, want [2]", b, err)
	}
}
