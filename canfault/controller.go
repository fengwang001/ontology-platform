package canfault

import (
	"errors"
	"sync"
)

// Event 是驱动故障界定状态机的总线事件。
type Event int

const (
	TxOK          Event = iota // 发送一帧成功
	TxErr                      // 发送时位错误（通用）
	TxAckErr                   // 发送时应答错误
	RxOK                       // 接收一帧成功
	RxErr                      // 接收错误（常规）
	RxErrDominant              // 接收错误，错误标志期间检测到显地位
)

// State 是节点的故障界定状态。
type State int

const (
	ErrorActive  State = iota // 主动错误
	ErrorPassive              // 被动错误
	BusOff                    // 总线关闭
)

// Transition 记录一次状态迁移。
type Transition struct {
	OpSeq int   // 该迁移发生在第几个成功操作（序号从 1 起）
	Old   State // 事件处理前的状态
	New   State // 事件处理后的状态
}

var (
	// ErrInvalidEvent 表示 Apply 收到未定义的事件。
	ErrInvalidEvent = errors.New("canfault: invalid event")
	// ErrApplyWhileBusOff 表示总线关闭期间调用 Apply。
	ErrApplyWhileBusOff = errors.New("canfault: apply rejected while bus-off")
	// ErrRestartNotBusOff 表示节点不在总线关闭状态时调用 Restart。
	ErrRestartNotBusOff = errors.New("canfault: restart rejected: not bus-off")
	// ErrRestartAlreadyRecovering 表示恢复流程已经开始时再次调用 Restart。
	ErrRestartAlreadyRecovering = errors.New("canfault: restart rejected: already recovering")
	// ErrIdle11NotRecovering 表示不在恢复流程中时调用 Idle11。
	ErrIdle11NotRecovering = errors.New("canfault: idle11 rejected: not recovering")
)

// Controller 是可并发使用的 CAN 故障界定状态机。
type Controller struct {
	mu         sync.Mutex
	tec        int
	rec        int
	recovering bool
	idleCount  int
	opSeq      int
	log        []Transition
}

// stateOf 由计数导出故障界定状态：
// TEC>255 为总线关闭；否则 TEC>127 或 REC>127 为被动错误；否则主动错误。
func stateOf(tec, rec int) State {
	switch {
	case tec > 255:
		return BusOff
	case tec > 127 || rec > 127:
		return ErrorPassive
	default:
		return ErrorActive
	}
}

// New 创建初始状态机：TEC=REC=0，主动错误。
func New() *Controller {
	return &Controller{log: make([]Transition, 0)}
}

// Apply 按事件发生前的状态处理一个事件。
// 拒绝顺序：先判事件非法，再判总线关闭。被拒绝时不改变任何状态。
func (c *Controller) Apply(ev Event) error {
	if ev < TxOK || ev > RxErrDominant {
		return ErrInvalidEvent
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	old := stateOf(c.tec, c.rec)
	if old == BusOff {
		return ErrApplyWhileBusOff
	}

	switch ev {
	case TxOK:
		if c.tec > 0 {
			c.tec--
		}
	case TxErr:
		c.tec += 8
	case TxAckErr:
		// 事件前为主动错误态加 8；被动错误态两个计数都不变。
		if old == ErrorActive {
			c.tec += 8
		}
	case RxOK:
		switch {
		case c.rec > 127:
			c.rec = 127
		case c.rec > 0:
			c.rec--
		}
	case RxErr:
		c.rec++
	case RxErrDominant:
		c.rec += 8
	}

	c.opSeq++
	c.record(old, stateOf(c.tec, c.rec))
	return nil
}

// Restart 仅在总线关闭且尚未开始恢复时有效：开始恢复并把恢复计数置 0。
// 拒绝顺序：先判不在总线关闭，再判已在恢复中。
func (c *Controller) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if stateOf(c.tec, c.rec) != BusOff {
		return ErrRestartNotBusOff
	}
	if c.recovering {
		return ErrRestartAlreadyRecovering
	}

	c.opSeq++
	c.recovering = true
	c.idleCount = 0
	return nil
}

// Idle11 记录恢复期间观察到一个连续 11 个隐性位的序列：恢复计数加 1。
// 第 128 次时 TEC 与 REC 清零、回到主动错误、恢复结束。
func (c *Controller) Idle11() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.recovering {
		return ErrIdle11NotRecovering
	}

	old := stateOf(c.tec, c.rec)
	c.idleCount++
	if c.idleCount == 128 {
		c.tec = 0
		c.rec = 0
		c.recovering = false
		c.idleCount = 0
	}

	c.opSeq++
	c.record(old, stateOf(c.tec, c.rec))
	return nil
}

// record 仅在新旧状态不同时追加迁移记录；调用方须持有 c.mu。
func (c *Controller) record(old, new State) {
	if old == new {
		return
	}
	c.log = append(c.log, Transition{OpSeq: c.opSeq, Old: old, New: new})
}

// Snapshot 是状态机某一时刻的只读视图。
type Snapshot struct {
	TEC         int
	REC         int
	State       State
	Recovering  bool
	IdleCount   int
	OpSeq       int
	Transitions []Transition
}

// Snapshot 返回当前计数、状态、恢复计数与迁移记录的拷贝，
// 调用方可自由修改返回值而不影响状态机内部状态。
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	transitions := make([]Transition, len(c.log))
	copy(transitions, c.log)
	return Snapshot{
		TEC:         c.tec,
		REC:         c.rec,
		State:       stateOf(c.tec, c.rec),
		Recovering:  c.recovering,
		IdleCount:   c.idleCount,
		OpSeq:       c.opSeq,
		Transitions: transitions,
	}
}
