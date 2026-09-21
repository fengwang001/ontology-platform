// Package ledger 在 wal 与 shard 之上协调原子转账与崩溃恢复。
//
// 并发取舍：Transfer、Recover、Checkpoint 全部在同一把互斥锁下串行执行。
// 也就是说 Recover 选择“独占”而非与转账并存——恢复期间新转账会阻塞，
// 换来的是不变量（总额守恒、Seq 连续、Txn 两条记录相邻）极易推理，
// 且崩溃注入点不会与并发转账交错产生半状态。
package ledger

import (
	"errors"
	"fmt"
	"sync"

	"ontology/shard"
	"ontology/wal"
)

// CrashPoint 指定下一次 Transfer 注入崩溃的位置。
type CrashPoint int

const (
	// CrashNone 不注入崩溃。
	CrashNone CrashPoint = iota
	// CrashAfterAppend 两条记录已写入 WAL，尚未 Apply 任何分片。
	CrashAfterAppend
	// CrashAfterDebit 扣款片已 Apply，入账片未 Apply。
	CrashAfterDebit
	// CrashAfterApply 两片都已 Apply，但元数据（appliedSeq）未更新。
	CrashAfterApply
)

// ErrCrashed 是被注入崩溃打断的 Transfer 返回的错误。
var ErrCrashed = errors.New("ledger: simulated crash")

// Ledger 是转账协调器。所有方法并发安全。
type Ledger struct {
	mu         sync.Mutex
	log        *wal.Log
	set        *shard.Set
	txn        uint64 // 下一个事务号
	crash      CrashPoint
	appliedSeq uint64 // 已确认全部应用的最大 Seq，Checkpoint 只能截到这里
}

// New 创建一个基于 WAL l 与分片集合 s 的协调器。
func New(l *wal.Log, s *shard.Set) *Ledger {
	return &Ledger{log: l, set: s}
}

// Transfer 把 amount 从分片 from 转到分片 to。
// 先整组写 WAL（一条 -amount、一条 +amount，共用同一 Txn），再逐片 Apply。
// WAL 落盘失败或参数非法时，分片状态不做任何修改。
func (g *Ledger) Transfer(from, to int, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("ledger: amount must be positive, got %d", amount)
	}
	if from == to {
		return fmt.Errorf("ledger: from and to must differ (%d)", from)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	n := g.set.Len()
	if from < 0 || from >= n || to < 0 || to >= n {
		return fmt.Errorf("ledger: shard index out of range [0,%d): %d,%d", n, from, to)
	}
	if g.set.Balance(from) < amount {
		return fmt.Errorf("ledger: insufficient balance on shard %d", from)
	}

	g.txn++
	txn := g.txn
	debit := wal.Record{Txn: txn, Shard: from, Delta: -amount}
	credit := wal.Record{Txn: txn, Shard: to, Delta: amount}
	if err := g.log.Append(debit, credit); err != nil {
		// 落盘失败：分片状态一个字节都不改，Seq 也未消耗。
		return err
	}
	if g.takeCrash(CrashAfterAppend) {
		return ErrCrashed
	}
	g.set.Apply(debit)
	if g.takeCrash(CrashAfterDebit) {
		return ErrCrashed
	}
	g.set.Apply(credit)
	if g.takeCrash(CrashAfterApply) {
		return ErrCrashed
	}
	// 串行执行下，此刻 WAL 中所有记录都已应用。
	g.appliedSeq = g.log.LastSeq()
	return nil
}

// takeCrash 判断并消费一次性崩溃点。
func (g *Ledger) takeCrash(p CrashPoint) bool {
	if g.crash == p {
		g.crash = CrashNone
		return true
	}
	return false
}
