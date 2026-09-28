package replication

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// helper：构造行镜像。
func row(cols ...string) Row {
	r := Row{}
	for i := 0; i+1 < len(cols); i += 2 {
		r[cols[i]] = cols[i+1]
	}
	return r
}

// 1. INSERT/UPDATE/DELETE 的正常应用路径。
func TestApplyHappyPath(t *testing.T) {
	s := NewStore(0, nil)

	res, err := s.Apply([]Event{
		{Seq: 1, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "1", "b", "2")},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if len(res.Applied) != 1 || res.LastSeq != 1 || res.RowCount != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}

	res, err = s.Apply([]Event{
		{Seq: 2, Op: OpUpdate, Key: "k1", Before: row("a", "1", "b", "2"), After: row("a", "1", "b", "9")},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := mustGet(t, s, "k1"); got["b"] != "9" {
		t.Fatalf("update not applied: %v", got)
	}

	res, err = s.Apply([]Event{
		{Seq: 3, Op: OpDelete, Key: "k1", Before: row("a", "1", "b", "9"), After: nil},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if s.RowCount() != 0 || res.LastSeq != 3 {
		t.Fatalf("delete not applied: count=%d lastSeq=%d", s.RowCount(), res.LastSeq)
	}
	if _, ok := s.Get("k1"); ok {
		t.Fatalf("row should be gone")
	}
}

func mustGet(t *testing.T, s *Store, key string) Row {
	t.Helper()
	r, ok := s.Get(key)
	if !ok {
		t.Fatalf("key %q missing", key)
	}
	return r
}

// 2. 前像整行不等 => BEFORE_MISMATCH 冲突，跳过且不改变该行。
func TestConflictBeforeMismatch(t *testing.T) {
	s := NewStore(0, nil)
	_, _ = s.Apply([]Event{{Seq: 1, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "1")}})

	res, err := s.Apply([]Event{
		{Seq: 2, Op: OpUpdate, Key: "k1", Before: row("a", "2"), After: row("a", "3")},
	})
	if err != nil {
		t.Fatalf("conflict must not be an error, got %v", err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d", len(res.Conflicts))
	}
	cf := res.Conflicts[0]
	if cf.Kind != ConflictBeforeMismatch || cf.Seq != 2 {
		t.Fatalf("unexpected conflict: %+v", cf)
	}
	// 副本未被冲突改变。
	if got := mustGet(t, s, "k1"); got["a"] != "1" {
		t.Fatalf("conflict changed the row: %v", got)
	}
	// 冲突日志持久保留。
	if len(s.Conflicts()) != 1 {
		t.Fatalf("conflict not logged")
	}
	// 冲突后序号仍推进，下一批从 3 开始。
	if s.LastSeq() != 2 {
		t.Fatalf("lastSeq=%d, want 2", s.LastSeq())
	}
}

// 3. UPDATE/DELETE 行不存在 => ROW_MISSING；INSERT 行已存在 => ROW_EXISTS。
func TestConflictRowPresence(t *testing.T) {
	s := NewStore(0, nil)
	_, _ = s.Apply([]Event{{Seq: 1, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "1")}})

	res, err := s.Apply([]Event{
		{Seq: 2, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "9")},
		{Seq: 3, Op: OpUpdate, Key: "ghost", Before: row("a", "1"), After: row("a", "2")},
		{Seq: 4, Op: OpDelete, Key: "ghost", Before: row("a", "1"), After: nil},
	})
	if err != nil {
		t.Fatalf("conflicts must not be errors, got %v", err)
	}
	wantKinds := []ConflictKind{ConflictRowExists, ConflictRowMissing, ConflictRowMissing}
	if len(res.Conflicts) != 3 {
		t.Fatalf("want 3 conflicts, got %d", len(res.Conflicts))
	}
	for i, want := range wantKinds {
		if res.Conflicts[i].Kind != want {
			t.Fatalf("conflict %d kind=%s want %s", i, res.Conflicts[i].Kind, want)
		}
	}
	if got := mustGet(t, s, "k1"); got["a"] != "1" {
		t.Fatalf("row changed by conflict: %v", got)
	}
}

// 4. 缺列与空串不相等：多一列、少一列、值为 "" 都必须判冲突。
func TestMissingColumnIsNotEmptyString(t *testing.T) {
	insert := func(initial Row) *Store {
		s := NewStore(0, nil)
		_, _ = s.Apply([]Event{{Seq: 1, Op: OpInsert, Key: "k", Before: nil, After: initial}})
		return s
	}

	cases := []struct {
		name    string
		current Row
		before  Row
	}{
		{"current has extra column", row("a", "1", "b", ""), row("a", "1")},
		{"before has extra column", row("a", "1"), row("a", "1", "b", "")},
		{"empty string vs other value", row("a", "1"), row("a", "")},
		// 当前行列 a 的值是空串，前像干脆不含列 a（但前像本身非空）：缺列 ≠ 空串。
		{"before omits a column whose current value is empty string", row("a", "", "keep", "1"), row("keep", "1")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := insert(tc.current)
			res, err := s.Apply([]Event{
				{Seq: 2, Op: OpUpdate, Key: "k", Before: tc.before, After: row("a", "z")},
			})
			if err != nil {
				t.Fatalf("want conflict not error, got %v", err)
			}
			if len(res.Conflicts) != 1 || res.Conflicts[0].Kind != ConflictBeforeMismatch {
				t.Fatalf("want BEFORE_MISMATCH, got %+v", res.Conflicts)
			}
			// 行保持初始内容。
			if got := mustGet(t, s, "k"); !rowsEqual(got, tc.current) {
				t.Fatalf("row changed: got %v want %v", got, tc.current)
			}
		})
	}

	// 反向对照：列集合相同、值相同（含空串）必须成功。
	s := insert(row("a", "", "b", "x"))
	res, err := s.Apply([]Event{
		{Seq: 2, Op: OpUpdate, Key: "k", Before: row("a", "", "b", "x"), After: row("a", "2")},
	})
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("empty-string columns should compare equal, err=%v conflicts=%+v", err, res.Conflicts)
	}
}

// 5. 同批内冲突与应用混合：冲突跳过，其余照常，顺序互不影响。
func TestMixedAppliedAndConflictInOneBatch(t *testing.T) {
	s := NewStore(0, nil)
	res, err := s.Apply([]Event{
		{Seq: 1, Op: OpInsert, Key: "a", Before: nil, After: row("v", "1")},           // applied
		{Seq: 2, Op: OpInsert, Key: "a", Before: nil, After: row("v", "2")},           // conflict ROW_EXISTS
		{Seq: 3, Op: OpInsert, Key: "b", Before: nil, After: row("v", "3")},           // applied
		{Seq: 4, Op: OpUpdate, Key: "a", Before: row("v", "9"), After: row("v", "4")}, // conflict MISMATCH
		{Seq: 5, Op: OpUpdate, Key: "a", Before: row("v", "1"), After: row("v", "5")}, // applied
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(res.Applied) != 3 || len(res.Conflicts) != 2 {
		t.Fatalf("applied=%v conflicts=%+v", res.Applied, res.Conflicts)
	}
	if got := mustGet(t, s, "a"); got["v"] != "5" {
		t.Fatalf("a=%v want v=5", got)
	}
	if got := mustGet(t, s, "b"); got["v"] != "3" {
		t.Fatalf("b=%v want v=3", got)
	}
	if s.LastSeq() != 5 {
		t.Fatalf("lastSeq=%d want 5", s.LastSeq())
	}
}

// 6. 各类非法输入 => ILLEGAL_EVENT。
func TestIllegalEvents(t *testing.T) {
	base := func(mut func(*Event)) []Event {
		e := Event{Seq: 1, Op: OpInsert, Key: "k", Before: nil, After: row("a", "1")}
		mut(&e)
		return []Event{e}
	}
	cases := []struct {
		name   string
		events []Event
	}{
		{"unknown op", base(func(e *Event) { e.Op = "WAT" })},
		{"empty key", base(func(e *Event) { e.Key = "" })},
		{"non-positive seq", base(func(e *Event) { e.Seq = 0 })},
		{"insert with before", base(func(e *Event) { e.Before = row("a", "0") })},
		{"insert with nil after", base(func(e *Event) { e.After = nil })},
		{"insert with empty after", base(func(e *Event) { e.After = Row{} })},
		{"update without before", base(func(e *Event) {
			e.Op = OpUpdate
			e.Before = nil
			e.After = row("a", "2")
		})},
		{"update without after", base(func(e *Event) {
			e.Op = OpUpdate
			e.Before = row("a", "1")
			e.After = nil
		})},
		{"delete without before", base(func(e *Event) {
			e.Op = OpDelete
			e.Before = nil
			e.After = nil
		})},
		{"delete with after", base(func(e *Event) {
			e.Op = OpDelete
			e.Before = row("a", "1")
			e.After = row("a", "2")
		})},
		{"empty column name", base(func(e *Event) { e.After = row("", "1") })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore(0, nil)
			_, err := s.Apply(tc.events)
			var re *RejectError
			if !errors.As(err, &re) || re.Reason != RejectIllegalEvent {
				t.Fatalf("want ILLEGAL_EVENT, got %v", err)
			}
			if !errors.Is(err, ErrIllegalEvent) {
				t.Fatalf("errors.Is(ErrIllegalEvent) failed")
			}
			if s.LastSeq() != 0 || s.RowCount() != 0 || len(s.Conflicts()) != 0 {
				t.Fatalf("rejected batch changed state: %+v", s.Snapshot())
			}
		})
	}
}

// 7. 序号不连续 => SEQUENCE_GAP；批中段内跳号同样拒绝。
func TestSequenceGap(t *testing.T) {
	s := NewStore(0, nil)
	// 未处理任何事件时第一条必须是 seq=1。
	if _, err := s.Apply([]Event{{Seq: 5, Op: OpInsert, Key: "k", After: row("a", "1")}}); !isReason(err, RejectSequenceGap) {
		t.Fatalf("want SEQUENCE_GAP, got %v", err)
	}
	_, _ = s.Apply([]Event{{Seq: 1, Op: OpInsert, Key: "k", Before: nil, After: row("a", "1")}})

	// 期望 2 却给 4。
	_, err := s.Apply([]Event{{Seq: 4, Op: OpDelete, Key: "k", Before: row("a", "1"), After: nil}})
	if !isReason(err, RejectSequenceGap) {
		t.Fatalf("want SEQUENCE_GAP, got %v", err)
	}
	// 批内第二条跳号：整批拒绝。
	_, err = s.Apply([]Event{
		{Seq: 2, Op: OpInsert, Key: "m", Before: nil, After: row("a", "1")},
		{Seq: 4, Op: OpInsert, Key: "n", Before: nil, After: row("a", "1")},
	})
	var re *RejectError
	if !errors.As(err, &re) || re.ExpectedSeq != 3 || re.EventIndex != 1 {
		t.Fatalf("want gap at index1 expected_seq=3, got %v", err)
	}
	// 拒绝后状态仍是 seq1 之后：只有 k 一行。
	if s.LastSeq() != 1 || s.RowCount() != 1 {
		t.Fatalf("rejected gap batch changed state: lastSeq=%d count=%d", s.LastSeq(), s.RowCount())
	}
	if _, ok := s.Get("m"); ok {
		t.Fatalf("rejected batch leaked row m")
	}
	// 正确序号仍可继续。
	if _, err := s.Apply([]Event{{Seq: 2, Op: OpInsert, Key: "m", Before: nil, After: row("a", "1")}}); err != nil {
		t.Fatalf("retry with correct seq failed: %v", err)
	}
}

// 8. 副本行数超限 => REPLICA_FULL。
func TestReplicaFull(t *testing.T) {
	s := NewStore(2, nil)
	_, err := s.Apply([]Event{
		{Seq: 1, Op: OpInsert, Key: "a", Before: nil, After: row("v", "1")},
		{Seq: 2, Op: OpInsert, Key: "b", Before: nil, After: row("v", "1")},
		{Seq: 3, Op: OpInsert, Key: "c", Before: nil, After: row("v", "1")},
	})
	if !isReason(err, RejectReplicaFull) {
		t.Fatalf("want REPLICA_FULL, got %v", err)
	}
	if s.RowCount() != 0 || s.LastSeq() != 0 {
		t.Fatalf("rejected over-full batch changed state: count=%d lastSeq=%d", s.RowCount(), s.LastSeq())
	}

	// 删除腾出空间后不超限；恰好达到上限允许。
	_, err = s.Apply([]Event{
		{Seq: 1, Op: OpInsert, Key: "a", Before: nil, After: row("v", "1")},
		{Seq: 2, Op: OpInsert, Key: "b", Before: nil, After: row("v", "1")},
		{Seq: 3, Op: OpDelete, Key: "a", Before: row("v", "1"), After: nil},
		{Seq: 4, Op: OpInsert, Key: "c", Before: nil, After: row("v", "1")},
	})
	if err != nil {
		t.Fatalf("delete-then-insert within limit should pass, got %v", err)
	}
	if s.RowCount() != 2 {
		t.Fatalf("count=%d want 2", s.RowCount())
	}
}

// 9. 拒绝的批（非法事件）即使前面事件本可应用，也必须完全无痕。
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	s := NewStore(0, nil)
	_, _ = s.Apply([]Event{{Seq: 1, Op: OpInsert, Key: "keep", Before: nil, After: row("v", "0")}})

	_, err := s.Apply([]Event{
		{Seq: 2, Op: OpUpdate, Key: "keep", Before: row("v", "0"), After: row("v", "1")}, // would apply
		{Seq: 3, Op: OpInsert, Key: "new", Before: nil, After: row("v", "2")},            // would apply
		{Seq: 4, Op: OpInsert, Key: "bad", Before: row("v", "x"), After: row("v", "3")},  // illegal
	})
	if !isReason(err, RejectIllegalEvent) {
		t.Fatalf("want ILLEGAL_EVENT, got %v", err)
	}
	if got := mustGet(t, s, "keep"); got["v"] != "0" {
		t.Fatalf("rejected batch changed existing row: %v", got)
	}
	if _, ok := s.Get("new"); ok {
		t.Fatalf("rejected batch leaked new row")
	}
	if s.LastSeq() != 1 || len(s.Conflicts()) != 0 {
		t.Fatalf("rejected batch advanced seq or logged conflict")
	}
}

// 10. 判定日志包含输入、判定结果与依据；拒绝也有原因行。
func TestDecisionLog(t *testing.T) {
	var buf bytes.Buffer
	s := NewStore(0, NewTextLogger(&buf))
	_, _ = s.Apply([]Event{
		{Seq: 1, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "1", "b", "")},
		{Seq: 2, Op: OpUpdate, Key: "k1", Before: row("a", "2"), After: row("a", "3")},
	})
	log := buf.String()
	for _, want := range []string{
		"[decision] seq=1", `"k1"`, "-> APPLIED", "after-image inserted",
		`"b":""`, // 空串值与缺列可区分
		"seq=2", "-> CONFLICT", "BEFORE_MISMATCH", "mismatch",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}

	buf.Reset()
	_, err := s.Apply([]Event{{Seq: 3, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "9")}})
	_ = err
	// 该条是冲突不是拒绝，不应出现 rejection 行；再来一个真非法批验证拒绝日志。
	if strings.Contains(buf.String(), "[rejection]") {
		t.Fatalf("conflict produced a rejection line: %s", buf.String())
	}
	buf.Reset()
	_, _ = s.Apply([]Event{{Seq: 3, Op: "BOGUS", Key: "k1", After: row("a", "9")}})
	if !strings.Contains(buf.String(), "[rejection] reason=ILLEGAL_EVENT") {
		t.Fatalf("rejection line missing: %s", buf.String())
	}
}

// 11. 同一输入序列反复计算得到完全相同的输出（日志逐字节一致，结果一致）。
func TestDeterministicReplay(t *testing.T) {
	batch1 := []Event{{Seq: 1, Op: OpInsert, Key: "k1", Before: nil, After: row("a", "1", "b", "2")}}
	batch2 := []Event{
		{Seq: 2, Op: OpUpdate, Key: "k1", Before: row("a", "9"), After: row("a", "3")},
		{Seq: 3, Op: OpInsert, Key: "k2", Before: nil, After: row("a", "1")},
	}
	run := func() (string, BatchResult, map[string]Row) {
		var buf bytes.Buffer
		s := NewStore(0, NewTextLogger(&buf))
		r1, err := s.Apply(batch1)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := s.Apply(batch2)
		if err != nil {
			t.Fatal(err)
		}
		_ = r1
		return buf.String(), r2, s.Snapshot()
	}
	log1, res1, snap1 := run()
	for i := 0; i < 5; i++ {
		log2, res2, snap2 := run()
		if log1 != log2 {
			t.Fatalf("log not deterministic on iteration %d", i)
		}
		if !rowsEqual(res1.Conflicts[0].Current, res2.Conflicts[0].Current) {
			t.Fatalf("conflict result differs")
		}
		if len(snap1) != len(snap2) || !rowsEqual(snap1["k1"], snap2["k1"]) || !rowsEqual(snap1["k2"], snap2["k2"]) {
			t.Fatalf("snapshot differs: %v vs %v", snap1, snap2)
		}
	}
}

// 12. 并发读取：读者只能看到整批边界状态（行数始终是批前或批后值）。
func TestConcurrentReadersSeeBatchBoundaries(t *testing.T) {
	s := NewStore(0, nil)
	const batchSize = 25

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读者：持续快照并校验每行内部一致（这里以"要么看不到新行、要么整行在"为边界不变量，
	// 同时行数必须可由已提交批次数解释）。
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				for k, r := range snap {
					if r == nil {
						t.Errorf("nil row for %s", k)
						return
					}
					if r["batch"] == "" {
						t.Errorf("half-applied row observed: %s=%v", k, r)
						return
					}
				}
			}
		}()
	}

	// 写者：每批 batchSize 条插入，行内容带批次标记，整批原子可见。
	for b := int64(1); b <= 20; b++ {
		evs := make([]Event, 0, batchSize)
		for i := int64(0); i < batchSize; i++ {
			seq := (b-1)*batchSize + i + 1
			evs = append(evs, Event{
				Seq: seq, Op: OpInsert, Key: "k" + strconv.FormatInt(seq, 10), Before: nil,
				After: row("batch", strconv.FormatInt(b, 10), "idx", strconv.FormatInt(i, 10)),
			})
		}
		if _, err := s.Apply(evs); err != nil {
			t.Fatalf("batch %d: %v", b, err)
		}
	}
	close(stop)
	wg.Wait()

	if got := s.RowCount(); got != 20*batchSize {
		t.Fatalf("final count=%d want %d", got, 20*batchSize)
	}
	if s.LastSeq() != int64(20*batchSize) {
		t.Fatalf("final lastSeq=%d", s.LastSeq())
	}
}

// 13. 对外返回的镜像都是拷贝，调用方修改不影响内部状态。
func TestReturnedImagesAreCopies(t *testing.T) {
	s := NewStore(0, nil)
	_, _ = s.Apply([]Event{{Seq: 1, Op: OpInsert, Key: "k", Before: nil, After: row("a", "1")}})
	r, _ := s.Get("k")
	r["a"] = "tampered"
	r["evil"] = "x"
	if got := mustGet(t, s, "k"); got["a"] != "1" || len(got) != 1 {
		t.Fatalf("internal state mutated through Get: %v", got)
	}
	snap := s.Snapshot()
	snap["k"]["a"] = "tampered2"
	if got := mustGet(t, s, "k"); got["a"] != "1" {
		t.Fatalf("internal state mutated through Snapshot: %v", got)
	}
}

func isReason(err error, reason RejectReason) bool {
	var re *RejectError
	return errors.As(err, &re) && re.Reason == reason
}
