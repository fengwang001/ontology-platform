package transaction

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// mkRows 生成 n 行，Key 为 prefix+i，便于在交错场景下逐行核对归属与顺序。
func mkRows(prefix string, n int) []Row {
	rows := make([]Row, n)
	for i := 0; i < n; i++ {
		rows[i] = Row{Key: fmt.Sprintf("%s-k%d", prefix, i), Value: fmt.Sprintf("%s-v%d", prefix, i)}
	}
	return rows
}

// mustWrite 要求写入成功。
func mustWrite(t *testing.T, r *Reassembler, txID string, row Row) {
	t.Helper()
	if err := r.Write(txID, row); err != nil {
		t.Fatalf("Write(%q, %v) unexpected error: %v", txID, row, err)
	}
}

// expectReject 断言 err 是指向 wantReason 的 *RejectError，且可被 errors.Is 命中。
func expectReject(t *testing.T, err error, want RejectReason) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError(%s), got %T: %v", want, err, err)
	}
	if re.Reason != want {
		t.Fatalf("want reason %s, got %s", want, re.Reason)
	}
	sentinel := map[RejectReason]error{
		ReasonInvalidEvent:        ErrInvalidEvent,
		ReasonEmptyTxID:           ErrEmptyTxID,
		ReasonDuplicateBegin:      ErrDuplicateBegin,
		ReasonTxNotFound:          ErrTxNotFound,
		ReasonBufferLimitExceeded: ErrBufferLimitExceeded,
	}[want]
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(err, sentinel %s) = false", want)
	}
}

// TestInterleavedTransactions 验证两个事务交错写入后，
// 每个事务按自身行的到达顺序整体输出，且输出顺序等于提交的到达顺序。
func TestInterleavedTransactions(t *testing.T) {
	r := NewReassembler(10)
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Begin("b"); err != nil {
		t.Fatal(err)
	}
	aRows := mkRows("a", 3)
	bRows := mkRows("b", 2)
	mustWrite(t, r, "a", aRows[0]) // a0
	mustWrite(t, r, "b", bRows[0]) // b0
	mustWrite(t, r, "a", aRows[1]) // a1
	mustWrite(t, r, "b", bRows[1]) // b1
	mustWrite(t, r, "a", aRows[2]) // a2
	if r.BufferedRows() != 5 || r.InFlight() != 2 {
		t.Fatalf("before commit: buffered=%d inflight=%d, want 5/2", r.BufferedRows(), r.InFlight())
	}

	// b 先提交：第一个输出必须是 b 的完整行集。
	outB, err := r.Commit("b")
	if err != nil {
		t.Fatal(err)
	}
	if outB.Seq != 1 || !rowsEqual(outB.Rows, bRows) {
		t.Fatalf("output[0] = seq=%d rows=%v, want seq=1 rows=%v", outB.Seq, outB.Rows, bRows)
	}
	if r.BufferedRows() != 3 {
		t.Fatalf("after commit b: buffered=%d, want 3", r.BufferedRows())
	}

	// a 后提交：第二个输出是 a 的完整行集，顺序为 a0,a1,a2。
	outA, err := r.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	if outA.Seq != 2 || !rowsEqual(outA.Rows, aRows) {
		t.Fatalf("output[1] = seq=%d rows=%v, want seq=2 rows=%v", outA.Seq, outA.Rows, aRows)
	}
	if r.BufferedRows() != 0 || r.InFlight() != 0 {
		t.Fatalf("after all: buffered=%d inflight=%d, want 0/0", r.BufferedRows(), r.InFlight())
	}
}

// TestRollbackDropsRows 验证回滚丢弃全部行，且这些行永远不会出现在任何输出中。
func TestRollbackDropsRows(t *testing.T) {
	r := NewReassembler(10)
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	for _, row := range mkRows("a", 4) {
		mustWrite(t, r, "a", row)
	}
	if err := r.Rollback("a"); err != nil {
		t.Fatal(err)
	}
	if r.BufferedRows() != 0 || r.InFlight() != 0 {
		t.Fatalf("after rollback: buffered=%d inflight=%d, want 0/0", r.BufferedRows(), r.InFlight())
	}
	// 回滚后该事务不再进行中：写/提交/回滚都应被拒绝。
	expectReject(t, r.Write("a", Row{Key: "x"}), ReasonTxNotFound)
	_, err := r.Commit("a")
	expectReject(t, err, ReasonTxNotFound)
	expectReject(t, r.Rollback("a"), ReasonTxNotFound)

	// 同名事务可重新开始；新事务的输出不得包含回滚前的任何行。
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, r, "a", Row{Key: "new", Value: "v"})
	out, err := r.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].Key != "new" {
		t.Fatalf("rows after restart = %v, want only [new=v]", out.Rows)
	}
}

// TestEmptyTransactionCommits 验证空事务提交也产生输出（Rows 为长度 0 的非 nil 切片）。
func TestEmptyTransactionCommits(t *testing.T) {
	r := NewReassembler(10)
	if err := r.Begin("empty"); err != nil {
		t.Fatal(err)
	}
	out, err := r.Commit("empty")
	if err != nil {
		t.Fatal(err)
	}
	if out.Rows == nil || len(out.Rows) != 0 {
		t.Fatalf("empty tx output Rows = %v, want non-nil len 0", out.Rows)
	}
	if out.Seq != 1 || out.TxID != "empty" {
		t.Fatalf("empty tx output = %+v, want seq=1 tx=empty", out)
	}
}

// TestBufferLimitIsGlobalAndRejectedWriteHasNoEffect 验证缓冲上限是所有进行中事务
// 的总行数；超限写入被拒绝，且不追加行、不改变任何状态。
func TestBufferLimitIsGlobalAndRejectedWriteHasNoEffect(t *testing.T) {
	r := NewReassembler(2)
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Begin("b"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, r, "a", Row{Key: "a0"})
	mustWrite(t, r, "b", Row{Key: "b0"})

	// 缓冲已满：无论写给哪个事务都拒绝。
	expectReject(t, r.Write("a", Row{Key: "a1"}), ReasonBufferLimitExceeded)
	expectReject(t, r.Write("b", Row{Key: "b1"}), ReasonBufferLimitExceeded)
	if r.BufferedRows() != 2 {
		t.Fatalf("buffered after rejected writes = %d, want 2", r.BufferedRows())
	}

	// 提交 b 释放 1 个名额后，新的写入应被接受。
	out, err := r.Commit("b")
	if err != nil {
		t.Fatal(err)
	}
	if !rowsEqual(out.Rows, []Row{{Key: "b0"}}) {
		t.Fatalf("commit b rows = %v, want [b0]", out.Rows)
	}
	mustWrite(t, r, "a", Row{Key: "a1"})
	if r.BufferedRows() != 2 {
		t.Fatalf("buffered after refill = %d, want 2", r.BufferedRows())
	}
}

// TestInvalidEvents 覆盖各类非法输入及其可区分原因。
func TestInvalidEvents(t *testing.T) {
	r := NewReassembler(10)

	// 未知事件类型（含零值与越界值）。
	for _, bad := range []EventType{0, 99, -1} {
		_, err := r.Apply(Event{Type: bad, TxID: "x"})
		expectReject(t, err, ReasonInvalidEvent)
	}

	// 空事务标识：begin/write/commit/rollback 全部拒绝。
	expectReject(t, r.Begin(""), ReasonEmptyTxID)
	expectReject(t, r.Write("", Row{Key: "x"}), ReasonEmptyTxID)
	_, err := r.Commit("")
	expectReject(t, err, ReasonEmptyTxID)
	expectReject(t, r.Rollback(""), ReasonEmptyTxID)

	// 重复开始。
	if err := r.Begin("dup"); err != nil {
		t.Fatal(err)
	}
	expectReject(t, r.Begin("dup"), ReasonDuplicateBegin)

	// 对不在进行中的事务写/提交/回滚。
	expectReject(t, r.Write("ghost", Row{Key: "x"}), ReasonTxNotFound)
	_, err = r.Commit("ghost")
	expectReject(t, err, ReasonTxNotFound)
	expectReject(t, r.Rollback("ghost"), ReasonTxNotFound)

	// 提交与回滚后事务即结束，再次操作同样“不在进行中”。
	mustWrite(t, r, "dup", Row{Key: "d0"})
	if _, err := r.Commit("dup"); err != nil {
		t.Fatal(err)
	}
	_, errCommit := r.Commit("dup")
	expectReject(t, errCommit, ReasonTxNotFound)
	if err := r.Begin("rb"); err != nil {
		t.Fatal(err)
	}
	if err := r.Rollback("rb"); err != nil {
		t.Fatal(err)
	}
	expectReject(t, r.Rollback("rb"), ReasonTxNotFound)

	// 所有拒绝都不得留下任何残留状态。
	if r.InFlight() != 0 || r.BufferedRows() != 0 {
		t.Fatalf("state after rejected ops: inflight=%d buffered=%d, want 0/0",
			r.InFlight(), r.BufferedRows())
	}
}

// TestRejectedCommitDoesNotConsumeSeq 验证被拒绝的提交不产生输出、不占用提交序号。
func TestRejectedCommitDoesNotConsumeSeq(t *testing.T) {
	r := NewReassembler(10)
	_, err := r.Commit("nobody")
	expectReject(t, err, ReasonTxNotFound)
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	out, err := r.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	if out.Seq != 1 {
		t.Fatalf("seq after rejected commit = %d, want 1", out.Seq)
	}
}

// TestCommitReturnsIndependentCopy 验证调用方修改返回切片不会污染重组器内部状态。
func TestCommitReturnsIndependentCopy(t *testing.T) {
	r := NewReassembler(10)
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, r, "a", Row{Key: "k", Value: "v"})
	out, err := r.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	out.Rows[0] = Row{Key: "TAMPERED"}

	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	out2, err := r.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.Rows) != 0 {
		t.Fatalf("internal state leaked through returned slice: %v", out2.Rows)
	}
}

// TestApplyDispatch 验证事件入口 Apply 的分派语义：仅成功提交返回非 nil 输出。
func TestApplyDispatch(t *testing.T) {
	r := NewReassembler(10)
	if out, err := r.Apply(Event{Type: EventBegin, TxID: "a"}); err != nil || out != nil {
		t.Fatalf("begin apply = %v, %v", out, err)
	}
	if out, err := r.Apply(Event{Type: EventWrite, TxID: "a", Row: Row{Key: "k"}}); err != nil || out != nil {
		t.Fatalf("write apply = %v, %v", out, err)
	}
	out, err := r.Apply(Event{Type: EventCommit, TxID: "a"})
	if err != nil || out == nil || len(out.Rows) != 1 {
		t.Fatalf("commit apply = %v, %v", out, err)
	}
	if out, err := r.Apply(Event{Type: EventRollback, TxID: "a"}); err == nil || out != nil {
		t.Fatalf("rollback of committed tx should reject, got %v, %v", out, err)
	}
}

// TestNewReassemblerRejectsNonPositiveLimit 是构造期防御：上限必须为正。
func TestNewReassemblerRejectsNonPositiveLimit(t *testing.T) {
	for _, n := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewReassembler(%d) should panic", n)
				}
			}()
			NewReassembler(n)
		}()
	}
}

// TestDeterministicReplay 验证同一输入序列反复计算得到完全相同的输出（含序号与行顺序）。
func TestDeterministicReplay(t *testing.T) {
	// 固定脚本：交错 begin/write，按固定顺序 commit/rollback。
	script := []Event{
		{Type: EventBegin, TxID: "a"},
		{Type: EventBegin, TxID: "b"},
		{Type: EventBegin, TxID: "c"},
		{Type: EventWrite, TxID: "a", Row: Row{Key: "a0"}},
		{Type: EventWrite, TxID: "b", Row: Row{Key: "b0"}},
		{Type: EventWrite, TxID: "a", Row: Row{Key: "a1"}},
		{Type: EventWrite, TxID: "c", Row: Row{Key: "c0"}},
		{Type: EventRollback, TxID: "c"},
		{Type: EventCommit, TxID: "b"},
		{Type: EventCommit, TxID: "a"},
	}

	run := func() []CommittedTx {
		r := NewReassembler(100)
		var got []CommittedTx
		for _, ev := range script {
			out, err := r.Apply(ev)
			if err != nil {
				t.Fatalf("event %v rejected: %v", ev, err)
			}
			if out != nil {
				got = append(got, *out)
			}
		}
		return got
	}

	first := run()
	for iter := 0; iter < 5; iter++ {
		got := run()
		if !committedEqual(got, first) {
			t.Fatalf("iteration %d output diverged:\n got=%v\nwant=%v", iter, got, first)
		}
	}
	want := []CommittedTx{
		{TxID: "b", Seq: 1, Rows: []Row{{Key: "b0"}}},
		{TxID: "a", Seq: 2, Rows: []Row{{Key: "a0"}, {Key: "a1"}}},
	}
	if !committedEqual(first, want) {
		t.Fatalf("deterministic output = %v, want %v", first, want)
	}
}

// TestConcurrentCommitRollback 在竞争检测器下并发提交与回滚：
// 每个提交事务发出的行必须与其缓冲行逐条一致，序号连续唯一，最终缓冲归零。
func TestConcurrentCommitRollback(t *testing.T) {
	const txnCount = 24
	const rowsPerTxn = 50

	r := NewReassembler(txnCount * rowsPerTxn)

	// 每个事务由独立 goroutine 写入自己的行：并发交错共享内部状态，
	// 但同一事务内的行到达顺序由该 goroutine 保证。
	var wg sync.WaitGroup
	expected := make(map[string][]Row, txnCount)
	for i := 0; i < txnCount; i++ {
		id := fmt.Sprintf("tx%02d", i)
		rows := mkRows(id, rowsPerTxn)
		expected[id] = rows
		if err := r.Begin(id); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(id string, rows []Row) {
			defer wg.Done()
			for _, row := range rows {
				if err := r.Write(id, row); err != nil {
					t.Errorf("write %s: %v", id, err)
					return
				}
			}
		}(id, rows)
	}
	wg.Wait()
	if r.BufferedRows() != txnCount*rowsPerTxn {
		t.Fatalf("buffered after writes = %d, want %d",
			r.BufferedRows(), txnCount*rowsPerTxn)
	}

	// 偶数号事务提交、奇数号回滚，全部并发发起。
	var (
		mu      sync.Mutex
		outputs []CommittedTx
	)
	for i := 0; i < txnCount; i++ {
		id := fmt.Sprintf("tx%02d", i)
		wg.Add(1)
		if i%2 == 0 {
			go func(id string) {
				defer wg.Done()
				out, err := r.Commit(id)
				if err != nil {
					t.Errorf("commit %s: %v", id, err)
					return
				}
				mu.Lock()
				outputs = append(outputs, out)
				mu.Unlock()
			}(id)
		} else {
			go func(id string) {
				defer wg.Done()
				if err := r.Rollback(id); err != nil {
					t.Errorf("rollback %s: %v", id, err)
				}
			}(id)
		}
	}
	wg.Wait()

	// 最终缓冲必须归零、无进行中事务。
	if r.BufferedRows() != 0 || r.InFlight() != 0 {
		t.Fatalf("after concurrent finish: buffered=%d inflight=%d, want 0/0",
			r.BufferedRows(), r.InFlight())
	}

	// 恰好一半事务被提交。
	if len(outputs) != txnCount/2 {
		t.Fatalf("committed count = %d, want %d", len(outputs), txnCount/2)
	}

	// 序号必须是 1..N 的一个排列（提交到达顺序），每行与该事务缓冲逐条一致。
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Seq < outputs[j].Seq })
	seenSeq := make(map[int64]bool, len(outputs))
	committedIDs := make(map[string]bool, len(outputs))
	for i, out := range outputs {
		if out.Seq != int64(i+1) {
			t.Fatalf("seq at position %d = %d, want %d (seqs must be a permutation of 1..N)",
				i, out.Seq, i+1)
		}
		if seenSeq[out.Seq] {
			t.Fatalf("duplicate seq %d", out.Seq)
		}
		seenSeq[out.Seq] = true
		committedIDs[out.TxID] = true
		if !rowsEqual(out.Rows, expected[out.TxID]) {
			t.Fatalf("tx %s emitted rows diverge from buffered rows:\n got=%v\nwant=%v",
				out.TxID, out.Rows, expected[out.TxID])
		}
	}

	// 回滚事务的行绝不能出现在输出中。
	for i := 1; i < txnCount; i += 2 {
		id := fmt.Sprintf("tx%02d", i)
		if committedIDs[id] {
			t.Fatalf("rolled-back tx %s appeared in output", id)
		}
	}
}

// TestLoggerPrintsInputOutputAndDecision 验证日志包含输入、输出与判定依据。
func TestLoggerPrintsInputOutputAndDecision(t *testing.T) {
	var buf bytes.Buffer
	r := NewReassembler(2, WithLogger(&buf))

	_ = r.Begin("a")
	_ = r.Write("a", Row{Key: "k1", Value: "v1"})
	_ = r.Begin("a")                              // 重复开始 → 拒绝
	_ = r.Write("a", Row{Key: "k2", Value: "v2"}) // 缓冲将满？上限 2，当前 1，接受
	_ = r.Write("a", Row{Key: "k3", Value: "v3"}) // 超限 → 拒绝
	_, _ = r.Commit("a")

	log := buf.String()
	for _, want := range []string{
		"in  BEGIN",                      // 输入
		`in  WRITE tx="a" row="k1"="v1"`, // 输入含行
		"rej BEGIN",                      // 判定依据：拒绝
		"reason=" + string(ReasonDuplicateBegin),
		"reason=" + string(ReasonBufferLimitExceeded),
		"out COMMIT",                         // 输出
		`seq=1 rows=2 ["k1"="v1" "k2"="v2"]`, // 输出内容，被拒的 k3 不在其中
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
	// 被拒绝的 k3 允许出现在输入日志中，但绝不能出现在提交输出行里。
	var outLine string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "out COMMIT") {
			outLine = line
		}
	}
	if outLine == "" {
		t.Fatalf("log missing output line:\n%s", log)
	}
	if strings.Contains(outLine, `"k3"`) {
		t.Fatalf("rejected row k3 must not appear in output line: %s", outLine)
	}
	t.Logf("captured decision log:\n%s", log)
}

// TestEmptyRowIsStillARow 验证 Key/Value 均为空也是合法数据行：
// 计入缓冲、随提交输出，且输入日志始终带 row= 字段。
func TestEmptyRowIsStillARow(t *testing.T) {
	var buf bytes.Buffer
	r := NewReassembler(4, WithLogger(&buf))
	if err := r.Begin("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.Write("a", Row{}); err != nil {
		t.Fatalf("write empty row: %v", err)
	}
	if r.BufferedRows() != 1 {
		t.Fatalf("buffered after empty row = %d, want 1", r.BufferedRows())
	}
	out, err := r.Commit("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0] != (Row{}) {
		t.Fatalf("empty row not preserved, got %v", out.Rows)
	}
	if !strings.Contains(buf.String(), `in  WRITE tx="a" row=""=""`) {
		t.Fatalf("log must record empty row explicitly:\n%s", buf.String())
	}
}

// rowsEqual 逐行比较两个行切片（nil 与空切片视为相等）。
func rowsEqual(a, b []Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// committedEqual 比较两个提交输出序列（含序号、事务标识与行）。
func committedEqual(a, b []CommittedTx) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TxID != b[i].TxID || a[i].Seq != b[i].Seq || !rowsEqual(a[i].Rows, b[i].Rows) {
			return false
		}
	}
	return true
}
