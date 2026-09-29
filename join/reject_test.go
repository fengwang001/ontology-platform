package join

import (
	"errors"
	"testing"
)

func assertReject(t *testing.T, err error, reason Reason, sentinel error) {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("期望 *RejectError，got %v", err)
	}
	if re.Reason != reason {
		t.Fatalf("拒绝原因 = %q, want %q", re.Reason, reason)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is 未匹配哨兵 %q，err=%v", reason, err)
	}
}

// TestInvalidInputs 覆盖各类非法输入，且原因可区分。
func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name     string
		seed     []Op
		bad      Op
		reason   Reason
		sentinel error
	}{
		{"左表空键", nil, lOp(Insert, "l", "", "v"), ReasonEmptyKey, ErrEmptyKey},
		{"右表空键", nil, rOp(Insert, "r", "", "v"), ReasonEmptyKey, ErrEmptyKey},
		{"左表空标识", nil, lOp(Insert, "", "k", "v"), ReasonEmptyID, ErrEmptyID},
		{"右表空标识", nil, rOp(Insert, "", "k", "v"), ReasonEmptyID, ErrEmptyID},
		{"重复插入左标识", []Op{lOp(Insert, "l1", "k", "v")}, lOp(Insert, "l1", "k2", "v"), ReasonDuplicateID, ErrDuplicateID},
		{"重复插入右标识", []Op{rOp(Insert, "r1", "k", "v")}, rOp(Insert, "r1", "k2", "v"), ReasonDuplicateID, ErrDuplicateID},
		{"删除不存在的左标识", nil, lOp(Delete, "nope", "k", "v"), ReasonIDNotFound, ErrIDNotFound},
		{"删除不存在的右标识", nil, rOp(Delete, "nope", "k", "v"), ReasonIDNotFound, ErrIDNotFound},
		{"未知侧", nil, Op{Side: Side(9), Kind: Insert, Row: Row{ID: "x", Key: "k"}}, ReasonUnknownSide, ErrUnknownSide},
		{"未知操作种类", nil, Op{Side: Left, Kind: Kind(9), Row: Row{ID: "x", Key: "k"}}, ReasonUnknownKind, ErrUnknownKind},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := New(0)
			if c.seed != nil {
				if _, err := j.Apply(c.seed); err != nil {
					t.Fatal(err)
				}
			}
			_, err := j.Apply([]Op{c.bad})
			assertReject(t, err, c.reason, c.sentinel)
		})
	}
}

// TestDuplicateWithinBatch 同一批内先插同名再插同名，必须被拒绝。
func TestDuplicateWithinBatch(t *testing.T) {
	j := New(0)
	_, err := j.Apply([]Op{
		lOp(Insert, "l1", "k", "a"),
		lOp(Insert, "l1", "k", "b"),
	})
	assertReject(t, err, ReasonDuplicateID, ErrDuplicateID)
	if j.LeftCount() != 0 {
		t.Fatalf("同批重复插入被拒绝后左表应为空，got %d", j.LeftCount())
	}
}

// TestEmptyBatch 空批应被接受且不产生任何变更。
func TestEmptyBatch(t *testing.T) {
	j := New(0)
	res, err := j.Apply(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 || len(j.DecisionLog()) != 0 {
		t.Fatalf("空批不应产生条目或日志，entries=%v log=%d", res.Entries, len(j.DecisionLog()))
	}
}

// TestRowLimit 左右合计总行数超限必须拒绝，且原因可区分。
func TestRowLimit(t *testing.T) {
	j := New(3)
	if _, err := j.Apply([]Op{
		lOp(Insert, "l1", "k", "v"),
		rOp(Insert, "r1", "k", "v"),
		lOp(Insert, "l2", "k", "v"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := j.Apply([]Op{rOp(Insert, "r2", "k", "v")})
	assertReject(t, err, ReasonRowLimitExceeded, ErrRowLimitExceeded)
	if j.LeftCount() != 2 || j.RightCount() != 1 {
		t.Fatalf("超限后两表行数不应改变: L=%d R=%d", j.LeftCount(), j.RightCount())
	}
	// 删除腾出名额后再插入应成功。
	if _, err := j.Apply([]Op{lOp(Delete, "l2", "k", "v")}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Apply([]Op{rOp(Insert, "r2", "k", "v")}); err != nil {
		t.Fatalf("名额释放后应插入成功，got %v", err)
	}
}

// TestRejectedBatchIsAtomic 被拒绝的批不得改变两表或已产生的日志。
func TestRejectedBatchIsAtomic(t *testing.T) {
	j := New(0)
	first, err := j.Apply([]Op{lOp(Insert, "l1", "k", "v")})
	if err != nil {
		t.Fatal(err)
	}
	beforeSnap := j.Snapshot()
	beforeLogs := len(j.DecisionLog())
	beforeL, beforeR := j.LeftCount(), j.RightCount()

	// 批内前两条合法、第三条非法：整批回滚，前两条也不生效。
	_, err = j.Apply([]Op{
		rOp(Insert, "r1", "k", "v"),
		rOp(Insert, "r2", "k", "v"),
		lOp(Insert, "l1", "dup", "v"), // 重复左标识
	})
	if err == nil {
		t.Fatal("期望批被拒绝")
	}
	if j.LeftCount() != beforeL || j.RightCount() != beforeR {
		t.Fatalf("拒绝后两表被改变: L %d->%d R %d->%d", beforeL, j.LeftCount(), beforeR, j.RightCount())
	}
	if len(j.DecisionLog()) != beforeLogs {
		t.Fatalf("拒绝后判定日志数量改变: %d -> %d", beforeLogs, len(j.DecisionLog()))
	}
	assertEntries(t, j.Snapshot(), beforeSnap)
	if len(first.Entries) != 1 || first.Entries[0] != beforeSnap[0] {
		t.Fatalf("历史成功批的输出被破坏: %+v", first.Entries)
	}
}
