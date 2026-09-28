package txreassemble_test

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/txreassemble"
)

func row(key string) txreassemble.Row {
	return txreassemble.Row{Key: key, Payload: []byte("payload-" + key)}
}

func keys(tx txreassemble.CommittedTx) []string {
	ks := make([]string, len(tx.Rows))
	for i, r := range tx.Rows {
		ks[i] = r.Key
	}
	return ks
}

// rejectKind 从 error 中提取可区分的拒绝原因。
func rejectKind(t *testing.T, err error) txreassemble.RejectKind {
	t.Helper()
	var re *txreassemble.RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %T: %v", err, err)
	}
	return re.Kind
}

// TestInterleavedCommits 验证交错到达的事务被分别缓冲，
// 提交时整体输出且行顺序为到达顺序，输出顺序即提交到达顺序。
func TestInterleavedCommits(t *testing.T) {
	r := txreassemble.New(100)

	must(t, r.Begin("A"))
	must(t, r.Begin("B"))
	must(t, r.Write("A", row("a1")))
	must(t, r.Write("B", row("b1")))
	must(t, r.Write("A", row("a2")))

	outA, err := r.Commit("A")
	must(t, err)
	if got := keys(outA); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Fatalf("A commit rows = %v, want [a1 a2]", got)
	}

	must(t, r.Write("B", row("b2")))
	outB, err := r.Commit("B")
	must(t, err)
	if got := keys(outB); !reflect.DeepEqual(got, []string{"b1", "b2"}) {
		t.Fatalf("B commit rows = %v, want [b1 b2]", got)
	}

	all := r.Output()
	if len(all) != 2 {
		t.Fatalf("output len = %d, want 2", len(all))
	}
	if all[0].TxID != "A" || all[1].TxID != "B" {
		t.Fatalf("output order = [%s %s], want [A B]", all[0].TxID, all[1].TxID)
	}
	if all[0].CommitSeq != 1 || all[1].CommitSeq != 2 {
		t.Fatalf("commit seq = [%d %d], want [1 2]", all[0].CommitSeq, all[1].CommitSeq)
	}
	if r.BufferedRows() != 0 || r.ActiveCount() != 0 {
		t.Fatalf("after drain: buffered=%d active=%d, want 0/0", r.BufferedRows(), r.ActiveCount())
	}
}

// TestRollbackDiscardsRows 验证回滚事务的行全部丢弃且不产生输出，缓冲释放。
func TestRollbackDiscardsRows(t *testing.T) {
	r := txreassemble.New(100)

	must(t, r.Begin("rb"))
	must(t, r.Write("rb", row("x1")))
	must(t, r.Write("rb", row("x2")))
	if r.BufferedRows() != 2 {
		t.Fatalf("buffered = %d, want 2", r.BufferedRows())
	}
	must(t, r.Rollback("rb"))

	if len(r.Output()) != 0 {
		t.Fatalf("rollback produced output: %+v", r.Output())
	}
	if r.BufferedRows() != 0 || r.ActiveCount() != 0 {
		t.Fatalf("after rollback: buffered=%d active=%d, want 0/0", r.BufferedRows(), r.ActiveCount())
	}

	// 回滚后该事务不再进行中。
	if err := r.Rollback("rb"); rejectKind(t, err) != txreassemble.RejectTxNotActive {
		t.Fatalf("second rollback kind = %v, want tx_not_active", rejectKind(t, err))
	}

	// 另一个正常提交的事务不受影响。
	must(t, r.Begin("ok"))
	must(t, r.Write("ok", row("o1")))
	out, err := r.Commit("ok")
	must(t, err)
	if got := keys(out); !reflect.DeepEqual(got, []string{"o1"}) {
		t.Fatalf("ok rows = %v, want [o1]", got)
	}
}

// TestEmptyTransaction 验证空事务提交也会输出一个空行集事务。
func TestEmptyTransaction(t *testing.T) {
	r := txreassemble.New(0) // 容量 0：空事务仍可提交

	must(t, r.Begin("empty"))
	out, err := r.Commit("empty")
	must(t, err)
	if out.TxID != "empty" || len(out.Rows) != 0 || out.Rows == nil {
		t.Fatalf("empty commit = %+v, want non-nil zero-length rows", out)
	}
	all := r.Output()
	if len(all) != 1 || all[0].TxID != "empty" || len(all[0].Rows) != 0 || all[0].Rows == nil {
		t.Fatalf("output = %+v, want single empty txn", all)
	}
	if out.CommitSeq != 1 {
		t.Fatalf("commit seq = %d, want 1", out.CommitSeq)
	}
}

// TestBufferFull 验证缓冲满时写入被拒且可区分原因，拒绝不改变状态；
// 提交/回滚释放容量后可继续写入。
func TestBufferFull(t *testing.T) {
	r := txreassemble.New(2)
	must(t, r.Begin("t"))
	must(t, r.Write("t", row("1")))
	must(t, r.Write("t", row("2")))

	err := r.Write("t", row("3"))
	if rejectKind(t, err) != txreassemble.RejectBufferFull {
		t.Fatalf("overflow kind = %v, want buffer_full", rejectKind(t, err))
	}
	if r.BufferedRows() != 2 {
		t.Fatalf("buffered after rejected write = %d, want 2", r.BufferedRows())
	}

	out, err := r.Commit("t")
	must(t, err)
	if got := keys(out); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Fatalf("rows = %v, want [1 2]", got)
	}

	// 容量释放：新事务可再次写满。
	must(t, r.Begin("u"))
	must(t, r.Write("u", row("u1")))
	must(t, r.Write("u", row("u2")))
	if k := rejectKind(t, r.Write("u", row("u3"))); k != txreassemble.RejectBufferFull {
		t.Fatalf("second overflow kind = %v, want buffer_full", k)
	}
	must(t, r.Rollback("u"))
	if r.BufferedRows() != 0 {
		t.Fatalf("buffered after rollback = %d, want 0", r.BufferedRows())
	}
}

// TestBufferFullSharedAcrossTransactions 验证上限按所有进行中事务的总行数计。
func TestBufferFullSharedAcrossTransactions(t *testing.T) {
	r := txreassemble.New(3)
	must(t, r.Begin("A"))
	must(t, r.Begin("B"))
	must(t, r.Write("A", row("a1")))
	must(t, r.Write("A", row("a2")))
	must(t, r.Write("B", row("b1")))
	if k := rejectKind(t, r.Write("B", row("b2"))); k != txreassemble.RejectBufferFull {
		t.Fatalf("shared overflow kind = %v, want buffer_full", k)
	}
	_, errA := r.Commit("A")
	must(t, errA)
	// A 释放 2 个位置后缓冲只剩 b1（1/3）：b2、b3 可继续写，b4 超限。
	must(t, r.Write("B", row("b2")))
	must(t, r.Write("B", row("b3")))
	if k := rejectKind(t, r.Write("B", row("b4"))); k != txreassemble.RejectBufferFull {
		t.Fatalf("post-commit overflow kind = %v, want buffer_full", k)
	}
	if r.BufferedRows() != 3 {
		t.Fatalf("buffered = %d, want 3", r.BufferedRows())
	}
}

// TestInvalidInputs 覆盖各类非法输入，且原因可区分。
func TestInvalidInputs(t *testing.T) {
	r := txreassemble.New(100)

	// 空事务 ID：四类操作均为 invalid_event。
	if k := rejectKind(t, r.Begin("")); k != txreassemble.RejectInvalidEvent {
		t.Fatalf("begin empty kind = %v", k)
	}
	if k := rejectKind(t, r.Write("", row("x"))); k != txreassemble.RejectInvalidEvent {
		t.Fatalf("write empty tx kind = %v", k)
	}
	_, err := r.Commit("")
	if k := rejectKind(t, err); k != txreassemble.RejectInvalidEvent {
		t.Fatalf("commit empty kind = %v", k)
	}
	if k := rejectKind(t, r.Rollback("")); k != txreassemble.RejectInvalidEvent {
		t.Fatalf("rollback empty kind = %v", k)
	}

	// 写空行为非法事件。
	must(t, r.Begin("t"))
	if k := rejectKind(t, r.Write("t", txreassemble.Row{})); k != txreassemble.RejectInvalidEvent {
		t.Fatalf("empty row kind = %v", k)
	}

	// 重复开始。
	if k := rejectKind(t, r.Begin("t")); k != txreassemble.RejectDuplicateBegin {
		t.Fatalf("duplicate begin kind = %v", k)
	}

	// 对不在进行中的事务操作。
	if k := rejectKind(t, r.Write("ghost", row("g"))); k != txreassemble.RejectTxNotActive {
		t.Fatalf("write ghost kind = %v", k)
	}
	_, err = r.Commit("ghost")
	if k := rejectKind(t, err); k != txreassemble.RejectTxNotActive {
		t.Fatalf("commit ghost kind = %v", k)
	}
	if k := rejectKind(t, r.Rollback("ghost")); k != txreassemble.RejectTxNotActive {
		t.Fatalf("rollback ghost kind = %v", k)
	}

	// Apply 未知事件类型。
	if _, err := r.Apply(txreassemble.Event{Type: txreassemble.EventUnknown, TxID: "t"}); rejectKind(t, err) != txreassemble.RejectInvalidEvent {
		t.Fatalf("unknown event kind = %v", rejectKind(t, err))
	}
	if _, err := r.Apply(txreassemble.Event{Type: txreassemble.EventType(99), TxID: "t"}); rejectKind(t, err) != txreassemble.RejectInvalidEvent {
		t.Fatalf("event type 99 kind = %v", rejectKind(t, err))
	}

	// 全部拒绝后事务状态完好：t 仍可正常写入并提交空缓冲之外的内容。
	if r.ActiveCount() != 1 {
		t.Fatalf("active count = %d, want 1 (rejections must not mutate state)", r.ActiveCount())
	}
	must(t, r.Write("t", row("real")))
	out, err := r.Commit("t")
	must(t, err)
	if got := keys(out); !reflect.DeepEqual(got, []string{"real"}) {
		t.Fatalf("t rows = %v, want [real] (rejected writes must not be buffered)", got)
	}
}

// TestRejectionDoesNotMutateOutput 验证对已结束事务的非法操作不改变已输出序列。
func TestRejectionDoesNotMutateOutput(t *testing.T) {
	r := txreassemble.New(100)
	must(t, r.Begin("a"))
	must(t, r.Write("a", row("a1")))
	_, err := r.Commit("a")
	must(t, err)
	before := r.Output()

	_ = r.Begin("a") // 同名事务在提交后可重新开始，但下面立刻拒绝掉以保持序列不变
	if k := rejectKind(t, r.Begin("a")); k != txreassemble.RejectDuplicateBegin {
		t.Fatalf("kind = %v", k)
	}
	_ = r.Rollback("a") // 清掉刚重新开始的事务
	_, err = r.Commit("a")
	if k := rejectKind(t, err); k != txreassemble.RejectTxNotActive {
		t.Fatalf("kind = %v", k)
	}
	if k := rejectKind(t, r.Write("a", row("z"))); k != txreassemble.RejectTxNotActive {
		t.Fatalf("kind = %v", k)
	}
	after := r.Output()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("output mutated by rejected ops:\nbefore=%+v\nafter =%+v", before, after)
	}
}

// TestOutputIsSnapshot 验证 Output 返回深拷贝，调用方修改不影响重组器。
func TestOutputIsSnapshot(t *testing.T) {
	r := txreassemble.New(100)
	must(t, r.Begin("a"))
	must(t, r.Write("a", row("a1")))
	_, _ = r.Commit("a")

	snap := r.Output()
	snap[0].Rows[0].Key = "tampered"
	snap[0].Rows[0].Payload[0] = 'X'
	again := r.Output()
	if again[0].Rows[0].Key != "a1" || string(again[0].Rows[0].Payload) != "payload-a1" {
		t.Fatalf("internal output mutated through snapshot: %+v", again[0])
	}
}

// TestDeterministicReplay 验证同一输入事件序列反复计算得到完全相同的输出。
func TestDeterministicReplay(t *testing.T) {
	events := []txreassemble.Event{
		{Type: txreassemble.EventBegin, TxID: "A"},
		{Type: txreassemble.EventBegin, TxID: "B"},
		{Type: txreassemble.EventWrite, TxID: "A", Row: row("a1")},
		{Type: txreassemble.EventWrite, TxID: "B", Row: row("b1")},
		{Type: txreassemble.EventBegin, TxID: "C"},
		{Type: txreassemble.EventWrite, TxID: "C", Row: row("c1")},
		{Type: txreassemble.EventRollback, TxID: "B"},
		{Type: txreassemble.EventWrite, TxID: "A", Row: row("a2")},
		{Type: txreassemble.EventCommit, TxID: "C"},
		{Type: txreassemble.EventWrite, TxID: "A", Row: row("a3")},
		{Type: txreassemble.EventCommit, TxID: "A"},
	}

	var first []txreassemble.CommittedTx
	for iter := 0; iter < 3; iter++ {
		r := txreassemble.New(100)
		for _, ev := range events {
			if _, err := r.Apply(ev); err != nil {
				t.Fatalf("iter %d: apply %+v: %v", iter, ev, err)
			}
		}
		got := r.Output()
		if iter == 0 {
			first = got
			if len(got) != 2 || got[0].TxID != "C" || got[1].TxID != "A" {
				t.Fatalf("output = %+v, want [C A]", got)
			}
			if ks := keys(got[0]); !reflect.DeepEqual(ks, []string{"c1"}) {
				t.Fatalf("C rows = %v", ks)
			}
			if ks := keys(got[1]); !reflect.DeepEqual(ks, []string{"a1", "a2", "a3"}) {
				t.Fatalf("A rows = %v", ks)
			}
		} else if !reflect.DeepEqual(first, got) {
			t.Fatalf("iter %d output differs:\nfirst=%+v\ngot  =%+v", iter, first, got)
		}
	}
}

// TestConcurrentCommitRollback 在并发提交/回滚下验证：
// 每个事务缓冲行与发出的行逐条一致、回滚事务零输出、缓冲最终归零。
func TestConcurrentCommitRollback(t *testing.T) {
	const txnCount = 200
	const rowsPerTx = 5
	r := txreassemble.New(txnCount*rowsPerTx + 1)

	var wg sync.WaitGroup
	for i := 0; i < txnCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("t%03d", i)
			if err := r.Begin(id); err != nil {
				t.Errorf("begin %s: %v", id, err)
				return
			}
			for j := 0; j < rowsPerTx; j++ {
				if err := r.Write(id, row(fmt.Sprintf("%s-r%d", id, j))); err != nil {
					t.Errorf("write %s-%d: %v", id, j, err)
					return
				}
			}
			// 偶数提交、奇数回滚；提交与回滚在此被大量并发调用。
			if i%2 == 0 {
				out, err := r.Commit(id)
				if err != nil {
					t.Errorf("commit %s: %v", id, err)
					return
				}
				if len(out.Rows) != rowsPerTx {
					t.Errorf("commit %s emitted %d rows, want %d", id, len(out.Rows), rowsPerTx)
				}
				for j, rw := range out.Rows {
					if want := fmt.Sprintf("%s-r%d", id, j); rw.Key != want {
						t.Errorf("commit %s row %d = %s, want %s", id, j, rw.Key, want)
					}
				}
			} else {
				if err := r.Rollback(id); err != nil {
					t.Errorf("rollback %s: %v", id, err)
				}
			}
		}(i)
	}
	wg.Wait()

	if r.BufferedRows() != 0 {
		t.Fatalf("buffered rows after all done = %d, want 0", r.BufferedRows())
	}
	if r.ActiveCount() != 0 {
		t.Fatalf("active txns after all done = %d, want 0", r.ActiveCount())
	}

	all := r.Output()
	if want := txnCount / 2; len(all) != want {
		t.Fatalf("committed output count = %d, want %d", len(all), want)
	}
	seen := map[string]bool{}
	var prevSeq int64
	for _, tx := range all {
		if seen[tx.TxID] {
			t.Fatalf("txn %s emitted twice", tx.TxID)
		}
		seen[tx.TxID] = true
		if len(tx.Rows) != rowsPerTx {
			t.Fatalf("txn %s rows = %d, want %d", tx.TxID, len(tx.Rows), rowsPerTx)
		}
		for j, rw := range tx.Rows {
			if want := fmt.Sprintf("%s-r%d", tx.TxID, j); rw.Key != want {
				t.Fatalf("txn %s row %d = %s, want %s", tx.TxID, j, rw.Key, want)
			}
		}
		if tx.CommitSeq != prevSeq+1 {
			t.Fatalf("txn %s seq = %d, want %d", tx.TxID, tx.CommitSeq, prevSeq+1)
		}
		prevSeq = tx.CommitSeq
	}
}

// TestConcurrentRejectedWritesAreStable 并发制造缓冲满拒绝，
// 验证高并发下计数守恒：输出的行与最终缓冲之和等于成功写入数。
func TestConcurrentRejectedWritesAreStable(t *testing.T) {
	const writers = 32
	const attempts = 200
	r := txreassemble.New(50)
	must(t, r.Begin("shared"))

	var wg sync.WaitGroup
	var accepted int64
	var mu sync.Mutex
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < attempts; j++ {
				err := r.Write("shared", row(fmt.Sprintf("w%d-r%d", w, j)))
				if err == nil {
					mu.Lock()
					accepted++
					mu.Unlock()
				} else if rejectKind(t, err) != txreassemble.RejectBufferFull {
					t.Errorf("unexpected reject: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()

	out, err := r.Commit("shared")
	must(t, err)
	if int64(len(out.Rows)) != accepted {
		t.Fatalf("emitted rows = %d, accepted writes = %d", len(out.Rows), accepted)
	}
	if r.BufferedRows() != 0 {
		t.Fatalf("buffered = %d, want 0", r.BufferedRows())
	}
}

// TestLogging 验证日志中打印输入、输出与判定依据。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	r := txreassemble.New(1).WithLogger(logger)

	must(t, r.Begin("L"))
	must(t, r.Write("L", row("l1")))
	_ = r.Write("L", row("overflow")) // 缓冲满，被拒绝
	_, _ = r.Commit("L")
	must(t, r.Begin("E"))
	_, _ = r.Commit("E") // 空事务
	must(t, r.Begin("R"))
	must(t, r.Write("R", row("r1")))
	must(t, r.Rollback("R"))

	logText := buf.String()
	t.Logf("captured log:\n%s", logText)
	for _, want := range []string{
		"input=begin",
		"input=write",
		"input=commit",
		"input=rollback",
		"decision=accept",
		"reject:buffer_full",
		"output_tx=\"L\"",
		"output_rows=1",
		"discarded_rows=1",
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Errorf("log missing %q", want)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRejectKindStringAndErrorMessage 验证拒绝原因可读名称与错误消息。
func TestRejectKindStringAndErrorMessage(t *testing.T) {
	cases := []struct {
		kind txreassemble.RejectKind
		want string
	}{
		{txreassemble.RejectInvalidEvent, "invalid_event"},
		{txreassemble.RejectDuplicateBegin, "duplicate_begin"},
		{txreassemble.RejectTxNotActive, "tx_not_active"},
		{txreassemble.RejectBufferFull, "buffer_full"},
		{txreassemble.RejectUnknown, "unknown"},
		{txreassemble.RejectKind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("kind %d String() = %q, want %q", c.kind, got, c.want)
		}
	}

	// 触发一次拒绝并断言错误消息同时包含原因与事务 ID。
	r := txreassemble.New(1)
	must(t, r.Begin("dup"))
	err := r.Begin("dup")
	if err == nil {
		t.Fatal("want duplicate begin error")
	}
	var re *txreassemble.RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %T", err)
	}
	if re.Kind != txreassemble.RejectDuplicateBegin || re.TxID != "dup" {
		t.Fatalf("reject = %+v", re)
	}
	msg := err.Error()
	if !strings.Contains(msg, "duplicate_begin") || !strings.Contains(msg, "dup") {
		t.Fatalf("error message = %q, want reason and tx id", msg)
	}
}

// TestNewNegativePanics 验证负数缓冲上限属于编程错误，直接 panic。
func TestNewNegativePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New(-1) did not panic")
		}
	}()
	_ = txreassemble.New(-1)
}

// TestApplyDispatchFailures 验证 Apply 对各事件类型的错误透传，
// 尤其 Commit 失败时不返回输出。
func TestApplyDispatchFailures(t *testing.T) {
	r := txreassemble.New(100)
	// 不 Begin 直接 Apply Commit：tx_not_active 且输出为 nil。
	out, err := r.Apply(txreassemble.Event{Type: txreassemble.EventCommit, TxID: "ghost"})
	if out != nil {
		t.Fatalf("failed commit returned output: %+v", out)
	}
	if rejectKind(t, err) != txreassemble.RejectTxNotActive {
		t.Fatalf("kind = %v", rejectKind(t, err))
	}
	// Apply Rollback 对不存在事务同样拒绝。
	if _, err := r.Apply(txreassemble.Event{Type: txreassemble.EventRollback, TxID: "ghost"}); rejectKind(t, err) != txreassemble.RejectTxNotActive {
		t.Fatalf("rollback kind = %v", rejectKind(t, err))
	}
	// Apply 成功路径：Begin/Write/Commit 端到端。
	if _, err := r.Apply(txreassemble.Event{Type: txreassemble.EventBegin, TxID: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(txreassemble.Event{Type: txreassemble.EventWrite, TxID: "x", Row: row("x1")}); err != nil {
		t.Fatal(err)
	}
	out, err = r.Apply(txreassemble.Event{Type: txreassemble.EventCommit, TxID: "x"})
	if err != nil || out == nil || out.TxID != "x" || len(out.Rows) != 1 {
		t.Fatalf("apply commit = %+v, %v", out, err)
	}
}
