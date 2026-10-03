// Package activity 实现带四类超时与重试预算的工作流活动执行器。
package activity

import (
	"log"

	"ontology/deadline"
	"ontology/retry"
)

// Config 是活动执行配置。
type Config struct {
	S2S int64 // 排队到开始时限，0 不设
	S2C int64 // 单次尝试执行时限，0 不设
	HB  int64 // 心跳间隔，0 表示无心跳到期（Heartbeat 仍合法）
	SC  int64 // 自首次排队起的总时限，0 不设
	M   int   // 最大尝试数，1..20
	D0  int64 // 退避初值，1..1e6
	Cap int64 // 退避上限，d0..1e9
}

// State 为活动的生命周期状态。
type State uint8

const (
	Waiting       State = iota // 退避中，尝试号已是下一次
	Scheduled                  // 等待领取
	Running                    // 执行中
	StateTerminal              // 终局
)

// Reason 标识尝试失败或终局的原因。
type Reason uint8

const (
	None     Reason = iota
	App             // 应用报告的可重试失败
	HB              // 心跳超时
	S2C             // 单次执行超时
	S2S             // 排队超时（不重试，直接终局）
	SCReason        // 总时限
)

// Terminal 标识终局类别。
type Terminal uint8

const (
	TermTimedOutSC Terminal = iota + 1
	TermTimedOutS2S
	TermFailed
	TermCompleted
)

// Status 是 Status 操作的只读快照（在给定 now 推演后的视图）。
type Status struct {
	State   State
	Attempt int
	Terminal
	Reason
	At int64
}

func (c Config) deadlineConfig() deadline.Config {
	return deadline.Config{S2S: c.S2S, S2C: c.S2C, HB: c.HB, SC: c.SC}
}

func (c Config) policy() retry.Policy {
	return retry.Policy{MaxAttempts: c.M, Initial: c.D0, Cap: c.Cap}
}

// activity 是单个活动的可变状态。
type activity struct {
	id  string
	cfg Config

	state    State
	k        int   // 当前尝试号；Waiting 期间已为下一次
	t0       int64 // 首次排队时刻
	g        int64 // 当前尝试排队时刻
	r        int64 // 本次开始时刻
	h        int64 // 最近心跳时刻
	waitEnd  int64 // Waiting 结束（下次排队）时刻
	progress int64

	term   Terminal
	reason Reason
	termAt int64

	heap *deadline.Heap

	// probe 为本次 advance 中被考察过的堆项数（Peek+Pop 各算一次接触）。
	probe int
	log   *log.Logger
}

func newActivity(id string, cfg Config, now int64, lg *log.Logger) *activity {
	a := &activity{
		id: id, cfg: cfg, state: Scheduled, k: 1, t0: now, g: now,
		heap: deadline.NewHeap(), log: lg,
	}
	a.buildScheduled()
	return a
}

func (a *activity) scSet() bool { return a.cfg.SC != 0 }

func (a *activity) setSC(h *deadline.Heap) {
	if a.cfg.SC != 0 {
		h.Set(deadline.SC, a.t0+a.cfg.SC)
	}
}

func (a *activity) buildScheduled() {
	h := deadline.NewHeap()
	if a.cfg.S2S != 0 {
		h.Set(deadline.S2S, a.g+a.cfg.S2S)
	}
	a.setSC(h)
	a.heap = h
}

func (a *activity) start(now int64) {
	a.state = Running
	a.r, a.h = now, now
	h := deadline.NewHeap()
	if a.cfg.S2C != 0 {
		h.Set(deadline.S2C, now+a.cfg.S2C)
	}
	if a.cfg.HB != 0 {
		h.Set(deadline.HB, now+a.cfg.HB)
	}
	a.setSC(h)
	a.heap = h
}

func (a *activity) heartbeat(now, progress int64) {
	a.h, a.progress = now, progress
	if a.cfg.HB != 0 {
		a.heap.Set(deadline.HB, now+a.cfg.HB)
	}
}

func (a *activity) finish(term Terminal, reason Reason, at int64) {
	a.state = StateTerminal
	a.term, a.reason, a.termAt = term, reason, at
	a.heap = deadline.NewHeap()
}

func (a *activity) clone() *activity {
	cp := *a
	cp.heap = a.heap.Clone()
	cp.log = nil // 虚拟推演不重复记录
	return &cp
}
