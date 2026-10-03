// Package coord 在 tcc 资源管理器之上实现全局事务的
// 决议（Commit/Abort）、执行、崩溃恢复与人工介入标记。
package coord

import (
	"errors"
	"io"
	"log"
	"sync"

	"ontology/tcc"
)

var (
	// ErrInvalid 参数非法。
	ErrInvalid = tcc.ErrInvalid
	// ErrClock now 非法或早于协调者时钟。
	ErrClock = tcc.ErrClock
	// ErrExists xid 已 Begin。
	ErrExists = errors.New("coord: transaction already exists")
	// ErrNoTxn xid 从未 Begin。
	ErrNoTxn = errors.New("coord: transaction not found")
)

// ErrCrashed 表示测试钩子注入的模拟崩溃。
var ErrCrashed = errors.New("coord: simulated crash")

// Decision 是不可变的全局决议。
type Decision string

const (
	// Pending 尚未写入决议。
	Pending Decision = ""
	// Commit 全分支 Try 成功。
	Commit Decision = "Commit"
	// Abort 有分支 Try 失败或恢复时无决议。
	Abort Decision = "Abort"
)

// BranchSpec 是 Begin 中声明的一个分支。
type BranchSpec struct {
	BR     string
	Acct   string
	Amount int64
}

type txnState int

const (
	stBegin txnState = iota
	stDecided
	stDone
)

type txn struct {
	xid      string
	branches []BranchSpec
	tried    []bool
	decision Decision
	state    txnState
	broken   map[int]bool
	manual   bool
	runNow   int64
}

// Coordinator 管全局事务。
type Coordinator struct {
	mu    sync.Mutex
	rm    *tcc.TCC
	log   *log.Logger
	clock int64
	txns  map[string]*txn
	// crashHook 在 Run 的每次成功 Try 之后、写决议之前被调用，
	// 返回 true 则模拟崩溃（丢弃尚未写盘的协调日志）。测试用。
	crashHook func(xid string, branchIndex int) bool
	// execHook 在 Execute/Recover 每个分支步骤之后被调用，
	// 返回 true 则模拟当场崩溃（决议与已完成步骤仍在日志中）。测试用。
	execHook func(xid string, branchIndex int) bool
}

// Option 配置 Coordinator。
type Option func(*Coordinator)

// WithLogger 设置协调日志输出。
func WithLogger(w io.Writer) Option {
	return func(c *Coordinator) {
		if w != nil {
			c.log = log.New(w, "coord ", log.LstdFlags|log.Lmicroseconds)
		}
	}
}

// New 创建协调者。
func New(rm *tcc.TCC, opts ...Option) *Coordinator {
	c := &Coordinator{rm: rm, txns: make(map[string]*txn)}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Begin 登记全局事务：branches 为 1..16 个非空分支。
func (c *Coordinator) Begin(xid string, branches []BranchSpec, now int64) error {
	if xid == "" || len(branches) < 1 || len(branches) > 16 {
		return c.failLocked("Begin", xid, tcc.ErrInvalid)
	}
	for _, b := range branches {
		if b.BR == "" || b.Acct == "" || b.Amount < 1 || b.Amount > 1_000_000_000_000 {
			return c.failLocked("Begin", xid, tcc.ErrInvalid)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := checkNow(now, c.clock); err != nil {
		return c.failLocked("Begin", xid, err)
	}
	if _, ok := c.txns[xid]; ok {
		return c.failLocked("Begin", xid, ErrExists)
	}
	cp := make([]BranchSpec, len(branches))
	copy(cp, branches)
	c.txns[xid] = &txn{xid: xid, branches: cp, tried: make([]bool, len(cp)), broken: map[int]bool{}}
	c.clock = now
	c.ok("Begin", xid)
	return nil
}

// Run 依次 Try 各分支并写入不可变决议；重复 Run 返回已有决议。
func (c *Coordinator) Run(xid string, now int64) (Decision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx := c.txns[xid]
	if tx == nil {
		return Pending, c.failLocked("Run", xid, ErrNoTxn)
	}
	if err := checkNow(now, c.clock); err != nil {
		return Pending, c.failLocked("Run", xid, err)
	}
	if tx.decision != Pending {
		c.out("Run(repeated)", xid, tx.decision)
		return tx.decision, nil
	}
	decision := Commit
	for i, b := range tx.branches {
		err := c.rm.Try(xid, b.BR, b.Acct, b.Amount, now)
		if err != nil {
			c.logf("Run branch %d Try failed: %v -> Abort", i+1, err)
			decision = Abort
			break
		}
		tx.tried[i] = true
		c.logf("Run branch %d Try durable", i+1)
		if c.crashHook != nil && c.crashHook(xid, i) {
			// Begin 与已成功 Try 已持久化；决议未写，故恢复为 Begin/Pending。
			tx.decision = Pending
			tx.state = stBegin
			c.clock = now
			c.logf("CRASH injected after branch %d of %s", i+1, xid)
			return Pending, ErrCrashed
		}
	}
	tx.decision = decision
	tx.state = stDecided
	tx.runNow = now
	c.clock = now
	c.out("Run(decide)", xid, decision)
	return decision, nil
}

func checkNow(now, clock int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return tcc.ErrInvalid
	}
	if now < clock {
		return tcc.ErrClock
	}
	return nil
}

func (c *Coordinator) logf(format string, v ...any) {
	if c.log != nil {
		c.log.Printf(format, v...)
	}
}

func (c *Coordinator) ok(op, xid string) {
	c.logf("%s xid=%q -> ok", op, xid)
}

func (c *Coordinator) out(op, xid string, d Decision) {
	c.logf("%s xid=%q -> %s", op, xid, d)
}

func (c *Coordinator) failLocked(op, xid string, err error) error {
	c.logf("%s xid=%q -> %v", op, xid, err)
	return err
}
