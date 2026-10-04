package lockstep

import (
	"errors"
	"sync"

	"ontology/input"
	"ontology/turn"
)

var (
	ErrInvalidParam = errors.New("lockstep: invalid parameter")
	ErrClockRewind  = errors.New("lockstep: clock rewind")
	ErrLate         = errors.New("lockstep: turn already settled")
	ErrAhead        = errors.New("lockstep: turn too far ahead")
	ErrDuplicate    = errors.New("lockstep: duplicate submission")
)

// Engine 是锁步同步对局的输入回合收集器。全部入口方法由单把互斥锁串行，
// 因此并发调用的结果等价于某个串行顺序。
type Engine struct {
	mu sync.Mutex

	n      int
	t      int64
	a      int
	cur    int
	dl     int64
	maxNow int64

	win *input.Window
	te  *turn.Engine

	log []turn.Record
}

// New 创建收集器。
// n 为玩家数（1..4096），t 为回合超时毫秒（1..1e6），a 为可提前回合数（0..64），
// r 为重复填充上限（0..100），kd 为掉线阈值（1..100），now0 为初始时钟（>=0）。
func New(n int, t int64, a, r, kd int, now0 int64) *Engine {
	if n < 1 || n > 4096 ||
		t < 1 || t > 1_000_000 ||
		a < 0 || a > 64 ||
		r < 0 || r > 100 ||
		kd < 1 || kd > 100 ||
		now0 < 0 || now0 > 1_000_000_000_000 {
		return nil
	}
	return &Engine{
		n:      n,
		t:      t,
		a:      a,
		cur:    1,
		dl:     now0 + t,
		maxNow: now0,
		win:    input.New(n, 1, a),
		te:     turn.New(n, r, kd),
		log:    make([]turn.Record, 0),
	}
}

// catchup 执行入口处理：反复在逻辑截止时刻 dl 结算超时回合，并连锁结算
// 同一时刻已到齐的回合，直到时钟 now 位于当前截止时刻之前。
func (e *Engine) catchup(now int64) []turn.Record {
	var settled []turn.Record
	for now >= e.dl {
		ts := e.dl
		rec := e.te.Settle(e.cur, ts, e.win)
		e.log = append(e.log, *rec)
		settled = append(settled, *rec)
		e.cur++
		e.dl = ts + e.t

		// 同一 ts 连锁结算已提前到齐的回合。
		for e.te.ActiveCount() > 0 && e.win.Ready(e.cur) == e.te.ActiveCount() {
			rec = e.te.Settle(e.cur, ts, e.win)
			e.log = append(e.log, *rec)
			settled = append(settled, *rec)
			e.cur++
			e.dl = ts + e.t
		}
	}
	return settled
}

// settleReady 以提交时刻 now 连锁结算已到齐的回合。
func (e *Engine) settleReady(now int64) []turn.Record {
	var settled []turn.Record
	for e.te.ActiveCount() > 0 && e.win.Ready(e.cur) == e.te.ActiveCount() {
		rec := e.te.Settle(e.cur, now, e.win)
		e.log = append(e.log, *rec)
		settled = append(settled, *rec)
		e.cur++
		e.dl = now + e.t
	}
	return settled
}

// Submit 提交玩家 p 在回合 k 的输入 cmd（1..64 字节）。
// 拒绝次序：参数非法 > 时钟回退 > 迟到 > 超前 > 重复（以首次为准）。
func (e *Engine) Submit(now int64, p, k int, cmd []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if p < 0 || p >= e.n || k < 1 || len(cmd) < 1 || len(cmd) > 64 ||
		now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	if now < e.maxNow {
		return ErrClockRewind
	}
	// 通过参数与回退检查即构成一次合法的时钟观测：入口处理可被该 now 触发，
	// 故时钟下限推进到 now；若操作随后因迟到/超前/重复被拒，只有其输入不被接受。
	e.maxNow = now

	// 入口处理：可能先超时结算多个回合，因此迟到判定必须在此之后。
	e.catchup(now)

	if k < e.cur {
		// 被拒绝的操作不推进时钟。
		return ErrLate
	}
	if k > e.cur+e.a {
		return ErrAhead
	}
	if e.win.Has(k, p) {
		return ErrDuplicate
	}

	wasActive := e.te.Active(p)
	e.win.Put(k, p, cmd)
	if !wasActive {
		// 立即恢复活跃，但 m 不变；先标记活跃再把已存槽位计入就绪数。
		e.te.Reactivate(p)
		e.win.MarkActive(p)
	}
	// 活跃玩家的新提交计入就绪计数（恢复路径下 Put 时尚不活跃，
	// MarkActive 已包含本槽位，避免重复计数）。
	if wasActive {
		e.win.CountSubmitted(k)
	}

	e.settleReady(now)
	return nil
}

// Advance 只做入口处理，返回本次新结算的回合记录。
func (e *Engine) Advance(now int64) ([]turn.Record, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	if now < e.maxNow {
		return nil, ErrClockRewind
	}
	e.maxNow = now
	return e.catchup(now), nil
}

// Log 返回自回合 from 起的全部结算记录（含回合号、结算时刻与每人的输入、来源）。
func (e *Engine) Log(from int) []turn.Record {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]turn.Record, 0)
	for _, rec := range e.log {
		if rec.Turn >= from {
			cpSlots := make([]turn.Slot, len(rec.Slots))
			for i, s := range rec.Slots {
				cpSlots[i] = turn.Slot{Cmd: append([]byte(nil), s.Cmd...), Source: s.Source}
			}
			out = append(out, turn.Record{Turn: rec.Turn, TS: rec.TS, Slots: cpSlots})
		}
	}
	return out
}

// Cur 返回最小的未结算回合号（观测口）。
func (e *Engine) Cur() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cur
}

// Deadline 返回当前截止时刻（观测口）。
func (e *Engine) Deadline() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dl
}

// Active 返回玩家当前是否活跃（观测口）。
func (e *Engine) Active(p int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p < 0 || p >= e.n {
		return false
	}
	return e.te.Active(p)
}

// Miss 返回玩家当前连续缺失数（观测口）。
func (e *Engine) Miss(p int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.te.Miss(p)
}

// ResetTouched / Touched 暴露非导出计数器，用于证明单次 Submit 的槽位触碰上界。
func (e *Engine) ResetTouched() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.win.ResetTouched()
}

func (e *Engine) Touched() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.win.Touched()
}
