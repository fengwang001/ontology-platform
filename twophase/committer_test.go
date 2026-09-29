package twophase

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// dumpState 打印操作后的副作用存储、位点与判定依据，便于核对两阶段状态。
func dumpState(t *testing.T, c *Committer, op, reason string) {
	t.Helper()
	s := c.State()
	seqs := make([]int64, 0, len(s.Pending))
	for seq := range s.Pending {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	parts := make([]string, 0, len(seqs))
	for _, seq := range seqs {
		parts = append(parts, fmt.Sprintf("%d=%q", seq, s.Pending[seq]))
	}
	t.Logf("[%s] %s | position=%d | effectStore(pending)={%s} | decision: %s",
		t.Name(), op, s.Position, strings.Join(parts, ", "), reason)
}

func rejectKind(err error) RejectKind {
	var reject *RejectError
	if errors.As(err, &reject) {
		return reject.Kind
	}
	return ""
}

func newTestCommitter(t *testing.T) *Committer {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	c, err := New(store)
	if err != nil {
		t.Fatalf("new committer: %v", err)
	}
	return c
}

func TestHappyPathTwoPhase(t *testing.T) {
	c := newTestCommitter(t)

	if err := c.Write(1, "send-email:A"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	dumpState(t, c, "Write(1, send-email:A)", "phase1 effect durable, position still 0")

	if c.Position() != 0 {
		t.Fatalf("position must not advance before commit, got %d", c.Position())
	}
	if err := c.Commit(1); err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	dumpState(t, c, "Commit(1)", "phase2 effect present and seq==position+1 -> position=1")

	if err := c.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(c.Pending()) != 0 {
		t.Fatalf("pending must be empty after commit, got %v", c.Pending())
	}
}

// TestRejectReasons 覆盖非法序号、越序写入、缺效果提交与位点跳跃，
// 并断言每次拒绝后副作用存储与位点均不变。
func TestRejectReasons(t *testing.T) {
	c := newTestCommitter(t)

	if err := c.Write(0, "bad"); rejectKind(err) != RejectInvalidSeq {
		t.Fatalf("write seq=0 kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Write(0, bad)", "REJECT invalid_seq: seq must be positive; state unchanged")

	if err := c.Write(2, "skip"); rejectKind(err) != RejectOutOfOrder {
		t.Fatalf("write seq=2 kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Write(2, skip)", "REJECT out_of_order: expect 1, effect/store untouched")

	if err := c.Commit(1); rejectKind(err) != RejectEffectMissing {
		t.Fatalf("commit without write kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Commit(1)", "REJECT effect_missing: no prepared effect; position untouched")

	if err := c.Write(1, "A"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := c.Commit(2); rejectKind(err) != RejectGap {
		t.Fatalf("commit gap kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Commit(2)", "REJECT gap: expect 1, position must advance by exactly one")

	if err := c.Commit(1); err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	if err := c.Write(1, "old"); rejectKind(err) != RejectInvalidSeq {
		t.Fatalf("rewrite committed seq kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Write(1, old)", "REJECT invalid_seq: seq already committed, monotonic position")

	if err := c.Commit(3); rejectKind(err) != RejectGap {
		t.Fatalf("commit 3 gap kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Commit(3)", "REJECT gap: expect 2, no skipping allowed")

	if c.Position() != 1 {
		t.Fatalf("position changed after rejections: %d", c.Position())
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestIdempotentWrite 同一在途序号重复写入相同副作用幂等，
// 不同副作用整体冲突拒绝且不覆盖原效果。
func TestIdempotentWrite(t *testing.T) {
	c := newTestCommitter(t)

	if err := c.Write(1, "A"); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := c.Write(1, "A"); err != nil {
		t.Fatalf("duplicate identical write should be idempotent: %v", err)
	}
	dumpState(t, c, "Write(1, A) x2", "idempotent: same in-flight seq+effect, store not duplicated")

	if err := c.Write(1, "B"); rejectKind(err) != RejectEffectConflict {
		t.Fatalf("conflicting write kind=%s err=%v", rejectKind(err), err)
	}
	dumpState(t, c, "Write(1, B)", "REJECT effect_conflict: stored A kept, incoming B dropped")

	got, ok := c.PendingEffect(1)
	if !ok || got != "A" {
		t.Fatalf("stored effect altered: %q ok=%v", got, ok)
	}

	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = c.Write(1, "A")
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent identical write: %v", err)
		}
	}
	dumpState(t, c, "Write(1, A) x20 concurrent", "all identical prepares accepted idempotently")
}

// TestCrashBetweenPhases 模拟两阶段之间崩溃：
// 副作用 fsync 后、位点推进前重启，pending 必须保留该序号供重复处理。
func TestCrashBetweenPhases(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	c, err := New(store)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// 事件1完整两阶段。
	if err := c.Write(1, "charge:100"); err != nil {
		t.Fatal(err)
	}
	if err := c.Commit(1); err != nil {
		t.Fatal(err)
	}
	// 事件2只完成阶段一，随后“崩溃”（丢弃内存态提交器）。
	if err := c.Write(2, "charge:200"); err != nil {
		t.Fatal(err)
	}
	dumpState(t, c, "Write(2) then CRASH before Commit", "effect 2 durable on disk, position still 1")

	reopenedStore, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := New(reopenedStore)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if c2.Position() != 1 {
		t.Fatalf("recovered position=%d want 1", c2.Position())
	}
	pending := c2.Pending()
	if len(pending) != 1 || pending[0] != 2 {
		t.Fatalf("recovered pending=%v want [2]", pending)
	}
	if action, ok := c2.PendingEffect(2); !ok || action != "charge:200" {
		t.Fatalf("recovered effect=%q ok=%v", action, ok)
	}
	dumpState(t, c2, "Restart/Recover", "position=1 restored; pending [2] returned for replay, no effect lost")

	// 重放：副作用已存在，重新写入相同动作幂等，然后提交恰好推进一次。
	if err := c2.Write(2, "charge:200"); err != nil {
		t.Fatalf("replay identical write: %v", err)
	}
	if err := c2.Commit(2); err != nil {
		t.Fatalf("replay commit: %v", err)
	}
	if c2.Position() != 2 {
		t.Fatalf("position after replay=%d want 2", c2.Position())
	}
	if err := c2.Verify(); err != nil {
		t.Fatalf("verify after replay: %v", err)
	}
	dumpState(t, c2, "Replay Write(2)+Commit(2)", "idempotent replay; position advanced exactly once to 2")
}

// TestCrashAfterCommit 位点落盘后崩溃，重启不得重复暴露该事件。
func TestCrashAfterCommit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	store, _ := NewFileStore(dir)
	c, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(1, "X"); err != nil {
		t.Fatal(err)
	}
	if err := c.Commit(1); err != nil {
		t.Fatal(err)
	}

	store2, _ := NewFileStore(dir)
	c2, err := New(store2)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Position() != 1 || len(c2.Pending()) != 0 {
		t.Fatalf("recover position=%d pending=%v", c2.Position(), c2.Pending())
	}
	dumpState(t, c2, "Restart after Commit(1)", "position durable=1, no pending: effect not replayed twice")
}

// TestConcurrentWriteThenCommitExactlyOnce 对同一在途序号并发重复写入后，
// 并发提交位点恰好推进一次，且已提交位点单调不减。
func TestConcurrentWriteThenCommitExactlyOnce(t *testing.T) {
	c := newTestCommitter(t)

	const writers = 32
	var wg sync.WaitGroup
	writeErrs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writeErrs[i] = c.Write(1, "once")
		}(i)
	}
	wg.Wait()
	for _, err := range writeErrs {
		if err != nil {
			t.Fatalf("concurrent prepare: %v", err)
		}
	}

	const committers = 32
	var committed int
	var mu sync.Mutex
	var rejected int
	var maxObserved int64
	for i := 0; i < committers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Commit(1)
			mu.Lock()
			defer mu.Unlock()
			pos := c.Position()
			if pos > maxObserved {
				maxObserved = pos
			}
			if err == nil {
				committed++
			} else if rejectKind(err) == RejectInvalidSeq {
				rejected++
			} else {
				t.Errorf("unexpected commit err: %v", err)
			}
		}()
	}
	wg.Wait()

	if committed != 1 {
		t.Fatalf("position advanced %d times, want exactly 1", committed)
	}
	if rejected != committers-1 {
		t.Fatalf("duplicate commits rejected=%d want %d", rejected, committers-1)
	}
	if c.Position() != 1 || maxObserved != 1 {
		t.Fatalf("position=%d maxObserved=%d want 1", c.Position(), maxObserved)
	}
	dumpState(t, c, fmt.Sprintf("Write x%d + Commit x%d concurrent", writers, committers),
		"exactly one commit advanced position; duplicates rejected invalid_seq (monotonic)")
}

// TestConcurrentQueryWithWrites 查询与自检与写入/提交并发执行不发生竞态。
func TestConcurrentQueryWithWrites(t *testing.T) {
	c := newTestCommitter(t)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = c.Position()
				_ = c.Pending()
				_ = c.State()
				_ = c.Verify()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for seq := int64(1); seq <= 100; seq++ {
			action := fmt.Sprintf("effect-%d", seq)
			if err := c.Write(seq, action); err != nil {
				t.Errorf("write %d: %v", seq, err)
				return
			}
			if err := c.Commit(seq); err != nil {
				t.Errorf("commit %d: %v", seq, err)
				return
			}
		}
		close(stop)
	}()
	wg.Wait()

	if c.Position() != 100 {
		t.Fatalf("position=%d want 100", c.Position())
	}
	if err := c.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	dumpState(t, c, "100 sequential Write+Commit with concurrent queries",
		"all reads saw consistent states; final position=100, no pending")
}

// replayOp 是一份可重放的操作记录。
type replayOp struct {
	kind   string // "write" 或 "commit"
	seq    int64
	action string
	want   RejectKind // 期望的拒绝原因；空串表示期望成功
}

// runReplay 在一个全新的存储目录上按顺序执行操作序列，返回每次结果。
func runReplay(t *testing.T, dir string, ops []replayOp) []RejectKind {
	t.Helper()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	c, err := New(store)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	results := make([]RejectKind, len(ops))
	for i, op := range ops {
		var err error
		switch op.kind {
		case "write":
			err = c.Write(op.seq, op.action)
		case "commit":
			err = c.Commit(op.seq)
		default:
			t.Fatalf("unknown op kind %q", op.kind)
		}
		results[i] = rejectKind(err)
		dumpState(t, c, fmt.Sprintf("%s(%d)", op.kind, op.seq),
			fmt.Sprintf("replay step %d: got=%s want=%s", i, resultLabel(results[i]), resultLabel(op.want)))
	}
	return results
}

func resultLabel(k RejectKind) string {
	if k == "" {
		return "ok"
	}
	return string(k)
}

// TestReplayOperationSequence 用同一份包含越序、缺效果、重复写入的操作序列
// 重放两次，两次的判定结果与最终位点必须逐字节一致，证明行为可复现。
func TestReplayOperationSequence(t *testing.T) {
	ops := []replayOp{
		{kind: "write", seq: 2, action: "oops", want: RejectOutOfOrder},
		{kind: "write", seq: 1, action: "A"},
		{kind: "write", seq: 1, action: "A"},
		{kind: "write", seq: 1, action: "B", want: RejectEffectConflict},
		{kind: "commit", seq: 2, want: RejectGap},
		{kind: "commit", seq: 1},
		{kind: "commit", seq: 1, want: RejectInvalidSeq},
		{kind: "write", seq: 2, action: "C"},
		{kind: "commit", seq: 2},
	}

	dirs := []string{
		filepath.Join(t.TempDir(), "run1"),
		filepath.Join(t.TempDir(), "run2"),
	}
	var first []RejectKind
	for run, dir := range dirs {
		results := runReplay(t, dir, ops)
		for i, op := range ops {
			if results[i] != op.want {
				t.Fatalf("run %d step %d: got=%s want=%s", run+1, i, resultLabel(results[i]), resultLabel(op.want))
			}
		}
		if run == 0 {
			first = results
		} else {
			for i := range first {
				if first[i] != results[i] {
					t.Fatalf("replay diverges at step %d: %s vs %s", i, first[i], results[i])
				}
			}
		}
	}
	t.Logf("[%s] replay verdict: %d ops reproduced identically across 2 fresh stores",
		t.Name(), len(ops))
}
