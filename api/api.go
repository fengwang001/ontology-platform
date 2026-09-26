// Package api 是 RR 调度器的对外门面：New/Add/Run/SelfCheck。
// 依赖方向：api -> rr -> proc，单向，绝不反向。
package api

import (
	"errors"

	"ontology/proc"
	"ontology/rr"
)

// 三类可判定且互不相同的哨兵错误（直接透传底层，errors.Is 即可判别）：
var (
	// ErrBadConfig 配置非法：quantum <= 0。
	ErrBadConfig = rr.ErrBadQuantum
	// ErrDupPID pid 重复。
	ErrDupPID = proc.ErrDupPID
)

// ErrBadProc 判定“进程非法”这一类：arrival < 0 或 burst <= 0（两个字段哨兵都归此类）。
func ErrBadProc(err error) bool {
	return errors.Is(err, proc.ErrBadArrival) || errors.Is(err, proc.ErrBadBurst)
}

// Sched 是对外调度器。
type Sched struct {
	inner *rr.Scheduler
}

// New 构造调度器；quantum <= 0 返回 ErrBadConfig，整体失败。
func New(quantum int64) (*Sched, error) {
	s, err := rr.New(quantum)
	if err != nil {
		return nil, err
	}
	return &Sched{inner: s}, nil
}

// Add 登记进程。非法字段、重复 pid 均在插入前被拒，已登记进程不变。
// 可被多个 goroutine 并发调用（底层集合在锁内查重并插入）。
func (s *Sched) Add(pid, arrival, burst int64) error {
	return s.inner.Add(pid, arrival, burst)
}

// Run 模拟调度，返回每个 pid 的完成时刻。
func (s *Sched) Run() (map[int64]int64, error) {
	return s.inner.Run(), nil
}

// SelfCheck 对一组内置进程集核验四条不变量；可并发调用（只用局部状态）。
// 1-3 由 rr.SelfTest 核验；这里补第 4 条“失败不留痕”及三类错误互不相同。
func (s *Sched) SelfCheck() error {
	if err := rr.SelfTest(); err != nil {
		return err
	}
	if err := rr.SelfTestCounter(); err != nil {
		return err
	}
	// 不变量 4：被拒操作不改变已登记进程，拒绝后仍可正常使用。
	ch, _ := New(4)
	if err := ch.Add(5, 2, 6); err != nil { // 先登记一个合法进程
		return err
	}
	eDup := ch.Add(5, 0, 1)                           // pid 重复
	eArr, eBurst := ch.Add(6, -1, 1), ch.Add(7, 0, 0) // arrival<0 / burst<=0
	_, eCfg := New(0)                                 // 配置非法
	if !errors.Is(eDup, ErrDupPID) || !ErrBadProc(eArr) || !ErrBadProc(eBurst) ||
		!errors.Is(eCfg, ErrBadConfig) {
		return errors.New("self-check: sentinel errors not judgeable")
	}
	if errors.Is(eDup, ErrBadConfig) || ErrBadProc(eDup) || errors.Is(eCfg, ErrDupPID) {
		return errors.New("self-check: three error classes not distinct")
	}
	got, err := ch.Run() // 被拒后集合仍是“仅 pid 5”，且结果正确（P5 arrival2 burst6 完@8）
	if err != nil || len(got) != 1 || got[5] != 8 {
		return errors.New("self-check: rejected op left a trace")
	}
	return nil
}
