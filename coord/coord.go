// Package coord 管理全局事务的决议执行与基于日志的崩溃恢复。
package coord

import (
	"errors"
	"fmt"
	"sync"

	"ontology/ledger"
	"ontology/tcc"
)

// ErrNeedsManual 表示 Commit 决议下存在到期 Broken 分支，需要人工处理。
var ErrNeedsManual = errors.New("coord: transaction needs manual intervention")

// Decision 是全局决议。
type Decision int

const (
	DecisionNone Decision = iota
	DecisionCommit
	DecisionAbort
)

func (d Decision) String() string {
	switch d {
	case DecisionCommit:
		return "Commit"
	case DecisionAbort:
		return "Abort"
	default:
		return "None"
	}
}

// BranchSpec 描述一个分支：br、账户、金额。
type BranchSpec struct {
	BR     []byte
	Acct   []byte
	Amount int64
}

// CrashPoint 标记一次日志追加（含其效果）完成之后的崩溃点。
type CrashPoint struct {
	XID  string
	Kind string // begin | try | decision
	Step int
}

// CrashHook 在每个日志追加点被调用；返回 true 模拟该点之后立即崩溃（panic）。
type CrashHook func(CrashPoint) bool

type logEntryKind int

const (
	logBegin logEntryKind = iota
	logTry
	logDecision
)

type logEntry struct {
	kind     logEntryKind
	br       string // Begin 与 Try 使用
	decision Decision
}

type txn struct {
	xid      string
	branches []BranchSpec
	logs     []logEntry
	decision Decision
	tried    map[string]bool // 由日志重建：已成功 Try 的分支
	broken   map[string]bool // Commit 下 Confirm 前已到期的分支
	ran      bool
}

// Status 为事务可查询状态。
type Status struct {
	Exists      bool
	Decision    Decision
	Tried       []string
	Broken      []string
	NeedsManual bool
}

// Coordinator 协调器；tm 为其操作的 TCC 管理器。
type Coordinator struct {
	mu      sync.Mutex
	tm      *tcc.Manager
	txns    map[string]*txn
	onCrash CrashHook
}

// New 创建协调器。
func New(tm *tcc.Manager) *Coordinator {
	return &Coordinator{tm: tm, txns: map[string]*txn{}}
}

// SetCrashHook 注入崩溃模拟钩子（测试用）。
func (c *Coordinator) SetCrashHook(h CrashHook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onCrash = h
}

func (c *Coordinator) crash(xid, kind string, step int) {
	if c.onCrash != nil && c.onCrash(CrashPoint{XID: xid, Kind: kind, Step: step}) {
		panic(crashError{xid, kind, step})
	}
}

type crashError struct {
	xid  string
	kind string
	step int
}

func (e crashError) Error() string {
	return fmt.Sprintf("coord: simulated crash after %s step %d of %s", e.kind, e.step, e.xid)
}

func validBranches(bs []BranchSpec) bool {
	if len(bs) < 1 || len(bs) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, b := range bs {
		if len(b.BR) == 0 || len(b.Acct) == 0 || b.Amount < 1 || b.Amount > tcc.MaxAmount {
			return false
		}
		if seen[string(b.BR)] {
			return false
		}
		seen[string(b.BR)] = true
	}
	return true
}

func checkClock(now int64) error {
	if now < 0 || now > tcc.MaxNow {
		return ledger.ErrParam
	}
	return nil
}

func setToNames(set map[string]bool, all []BranchSpec) []string {
	var out []string
	for _, b := range all {
		if set[string(b.BR)] {
			out = append(out, string(b.BR))
		}
	}
	return out
}
