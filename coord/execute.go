package coord

import (
	"fmt"

	"ontology/tcc"
)

// BranchError 汇总单个分支执行时的错误。
type BranchError struct {
	Index int
	BR    string
	Err   error
}

// Result 是 Execute/Recover 的结果。
type Result struct {
	Decision    Decision
	Broken      []int // Commit 下 Confirm 报 ErrExpired 的分支下标
	Errors      []BranchError
	NeedsManual bool
}

// Execute 按决议执行每个分支；遇错继续其余分支，可重复调用。
func (c *Coordinator) Execute(xid string, now int64) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx := c.txns[xid]
	if tx == nil {
		return Result{}, c.failLocked("Execute", xid, ErrNoTxn)
	}
	if err := checkNow(now, c.clock); err != nil {
		return Result{}, c.failLocked("Execute", xid, err)
	}
	if tx.decision == Pending {
		tx.decision = Abort
		tx.state = stDecided
		c.logf("Execute without decision -> implicit Abort xid=%q", xid)
	}
	res, err := c.executeLocked(tx, now)
	if err != nil {
		return Result{}, err
	}
	c.clock = now
	return res, nil
}

// Recover 只依据协调日志：无决议写 Abort，然后等同 Execute。
func (c *Coordinator) Recover(xid string, now int64) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx := c.txns[xid]
	if tx == nil {
		return Result{}, c.failLocked("Recover", xid, ErrNoTxn)
	}
	if err := checkNow(now, c.clock); err != nil {
		return Result{}, c.failLocked("Recover", xid, err)
	}
	if tx.decision == Pending {
		tx.decision = Abort
		tx.state = stDecided
		c.logf("Recover no decision -> Abort xid=%q", xid)
	}
	res, err := c.executeLocked(tx, now)
	if err != nil {
		return Result{}, err
	}
	c.clock = now
	c.logf("Recover xid=%q -> decision=%s broken=%v manual=%v errors=%d",
		xid, res.Decision, res.Broken, res.NeedsManual, len(res.Errors))
	return res, nil
}

func (c *Coordinator) executeLocked(tx *txn, now int64) (res Result, err error) {
	res = Result{Decision: tx.decision}
	// Execute 是成功推进的恢复流程：先统一落实此刻到期（now 的纯函数效果），
	// 再逐分支执行，避免分支间相互回滚到期判定。
	c.rm.MaterializeExpiry(now)
	for i, b := range tx.branches {
		var err error
		if tx.decision == Commit {
			err = c.rm.Confirm(tx.xid, b.BR, now)
		} else {
			err = c.rm.Cancel(tx.xid, b.BR, now)
		}
		if err == nil {
			c.logf("exec step %d/%d %s -> ok", i+1, len(tx.branches), b.BR)
			if c.execHook != nil && c.execHook(tx.xid, i) {
				c.clock = now
				c.logf("CRASH injected after exec step %d of %s", i+1, tx.xid)
				return res, ErrCrashed
			}
			continue
		}
		if tx.decision == Commit && err == tcc.ErrExpired {
			tx.broken[i] = true
			tx.manual = true
			res.Broken = append(res.Broken, i)
			res.NeedsManual = true
			c.logf("exec step %d/%d %s -> ErrExpired => Broken, NeedsManual",
				i+1, len(tx.branches), b.BR)
			if c.execHook != nil && c.execHook(tx.xid, i) {
				c.clock = now
				c.logf("CRASH injected after exec step %d of %s", i+1, tx.xid)
				return res, ErrCrashed
			}
			continue
		}
		res.Errors = append(res.Errors, BranchError{Index: i, BR: b.BR, Err: err})
		c.logf("exec step %d/%d %s -> error %v", i+1, len(tx.branches), b.BR, err)
	}
	tx.state = stDone
	return res, nil
}

// Status 是事务可查状态。
type Status struct {
	Decision    Decision
	Done        bool
	NeedsManual bool
	Broken      []int
	Tried       []int
}

// Status 查询事务状态；不存在返回 ErrNoTxn。
func (c *Coordinator) Status(xid string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx := c.txns[xid]
	if tx == nil {
		return Status{}, ErrNoTxn
	}
	st := Status{Decision: tx.decision, Done: tx.state == stDone, NeedsManual: tx.manual}
	for i := range tx.branches {
		if tx.broken[i] {
			st.Broken = append(st.Broken, i)
		}
		if tx.tried[i] {
			st.Tried = append(st.Tried, i)
		}
	}
	return st, nil
}

// setCrashHook 安装崩溃注入钩子（测试用）。
func (c *Coordinator) setCrashHook(f func(xid string, branchIndex int) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.crashHook = f
}

// setExecHook 安装执行阶段崩溃注入钩子（测试用）。
func (c *Coordinator) setExecHook(f func(xid string, branchIndex int) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.execHook = f
}

func (e BranchError) Error() string {
	return fmt.Sprintf("branch %d (%s): %v", e.Index+1, e.BR, e.Err)
}

// Unwrap 让 errors.Is 能识别底层 tcc 错误。
func (e BranchError) Unwrap() error { return e.Err }
