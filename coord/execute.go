package coord

import (
	"errors"

	"ontology/ledger"
)

// ErrNoDecision 表示 Execute/Recover 之外在决议写定前直接要求执行。
var ErrNoDecision = errors.New("coord: no decision written")

// BranchError 记录单个分支执行失败；事务继续其余分支。
type BranchError struct {
	BR  string
	Err error
}

// Execute 按已写决议执行 Confirm（Commit）或 Cancel（Abort，含空回滚）。
// 返回 Broken 分支集合与逐分支错误；Commit 下存在 Broken 时顶层错误为 ErrNeedsManual。
// 可重复调用，结果幂等。
func (c *Coordinator) Execute(xid []byte, now int64) ([]string, []BranchError, error) {
	if len(xid) == 0 {
		return nil, nil, ledger.ErrParam
	}
	if err := checkClock(now); err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.tm.Clock() {
		return nil, nil, ledger.ErrClock
	}
	t, ok := c.txns[string(xid)]
	if !ok {
		return nil, nil, ledger.ErrNoTx
	}
	if t.decision == DecisionNone {
		return nil, nil, ErrNoDecision
	}
	return c.applyLocked(t, now)
}

// Recover 只依据协调日志恢复：无决议先写 Abort，然后按决议执行。
func (c *Coordinator) Recover(xid []byte, now int64) (Decision, []string, []BranchError, error) {
	if len(xid) == 0 {
		return DecisionNone, nil, nil, ledger.ErrParam
	}
	if err := checkClock(now); err != nil {
		return DecisionNone, nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.tm.Clock() {
		return DecisionNone, nil, nil, ledger.ErrClock
	}
	t, ok := c.txns[string(xid)]
	if !ok {
		return DecisionNone, nil, nil, ledger.ErrNoTx
	}
	if t.decision == DecisionNone {
		t.decision = DecisionAbort
		t.ran = true
		t.logs = append(t.logs, logEntry{kind: logDecision, decision: DecisionAbort})
		c.crash(t.xid, "decision", -1) // 恢复时补写 Abort 后的崩溃点
	}
	broken, errs, err := c.applyLocked(t, now)
	return t.decision, broken, errs, err
}

func (c *Coordinator) applyLocked(t *txn, now int64) ([]string, []BranchError, error) {
	var branchErrs []BranchError
	if t.decision == DecisionCommit {
		for _, b := range t.branches {
			err := c.tm.Confirm([]byte(t.xid), b.BR, now)
			switch {
			case err == nil:
			case errors.Is(err, ledger.ErrExpired):
				t.broken[string(b.BR)] = true // 不自动重新 Try，等待人工处理
			default:
				branchErrs = append(branchErrs, BranchError{string(b.BR), err})
			}
		}
	} else {
		for _, b := range t.branches {
			if err := c.tm.Cancel([]byte(t.xid), b.BR, now); err != nil {
				// Abort 下继续其余分支（含容量满导致空回滚无法落标记的情形）。
				branchErrs = append(branchErrs, BranchError{string(b.BR), err})
			}
		}
	}
	broken := setToNames(t.broken, t.branches)
	if len(broken) > 0 {
		return broken, branchErrs, ErrNeedsManual
	}
	return nil, branchErrs, nil
}

// Status 查询事务状态。
func (c *Coordinator) Status(xid []byte) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.txns[string(xid)]
	if !ok {
		return Status{}
	}
	return Status{
		Exists:      true,
		Decision:    t.decision,
		Tried:       setToNames(t.tried, t.branches),
		Broken:      setToNames(t.broken, t.branches),
		NeedsManual: len(t.broken) > 0,
	}
}
