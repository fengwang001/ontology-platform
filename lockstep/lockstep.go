// Package lockstep 提供锁步同步对局的输入回合收集器：
// 时钟与截止管理、活跃标记、入口处理与回合编排。
package lockstep

import (
	"errors"
	"sync"

	"ontology/input"
	"ontology/turn"
)

// 拒绝原因，按 参数 > 时钟 > 迟到 > 超前 > 重复 的次序判定。
var (
	ErrParam     = errors.New("lockstep: 参数非法")
	ErrClock     = errors.New("lockstep: 时钟回退")
	ErrLate      = errors.New("lockstep: 回合已结算（迟到）")
	ErrAhead     = errors.New("lockstep: 超出提前提交窗口")
	ErrDuplicate = errors.New("lockstep: 重复提交")
)

const (
	maxN     = 4096
	maxT     = 1_000_000
	maxA     = 64
	maxR     = 100
	maxKd    = 100
	maxClock = 1_000_000_000_000
)

// Engine 是锁步收集器。所有方法可并发调用，效果等价于某个串行顺序。
type Engine struct {
	mu      sync.Mutex
	n       int
	timeout int64 // T：回合超时（毫秒）
	ahead   int   // A：可提前提交的回合数
	repeat  int   // R：重复填充上限
	drop    int   // Kd：掉线阈值
	now0    int64

	cur     int // 最小未结算回合，从 1 编号
	dl      int64
	maxNow  int64 // 已接受操作的最大 now
	players []turn.Player
	active  int
	win     *input.Window
	logs    []turn.Log // logs[i] 是回合 i+1 的记录

	touched int // 非导出计数器：单次 Submit 触碰的玩家槽位数（测试用）
}

// New 创建收集器：玩家 0..n-1，回合从 1 编号，初始截止 dl=now0+T，全员活跃。
func New(n int, timeout int64, ahead, repeat, drop int, now0 int64) (*Engine, error) {
	if n < 1 || n > maxN || timeout < 1 || timeout > maxT ||
		ahead < 0 || ahead > maxA || repeat < 0 || repeat > maxR ||
		drop < 1 || drop > maxKd || now0 < 0 || now0 > maxClock {
		return nil, ErrParam
	}
	players := make([]turn.Player, n)
	for i := range players {
		players[i].Active = true
	}
	return &Engine{
		n: n, timeout: timeout, ahead: ahead, repeat: repeat, drop: drop,
		now0: now0, cur: 1, dl: now0 + timeout, active: n,
		players: players, win: input.New(),
	}, nil
}

// Submit 提交玩家 p 对回合 k 的输入 cmd（1..64 字节）。
// 拒绝次序：参数非法 > 时钟回退 > 迟到 > 超前 > 重复；被拒绝等价于无操作。
func (e *Engine) Submit(now int64, p, k int, cmd []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p < 0 || p >= e.n || k < 1 || len(cmd) < 1 || len(cmd) > 64 {
		return ErrParam
	}
	if now < e.now0 || now < e.maxNow {
		return ErrClock
	}
	e.entry(now)
	if k < e.cur {
		return ErrLate
	}
	if k > e.cur+e.ahead {
		return ErrAhead
	}
	if e.win.Has(p, k) {
		return ErrDuplicate
	}
	e.maxNow = now
	if !e.players[p].Active {
		// 提交即恢复活跃，但 m 不变，只有实交回合结算时才清零。
		e.players[p].Active = true
		e.active++
		for _, j := range e.win.Turns(p) {
			e.win.Bump(j, 1)
			e.touched++
		}
	}
	e.win.Add(p, k, append([]byte(nil), cmd...))
	e.win.Bump(k, 1)
	e.touched++
	if e.arrived() {
		e.settle(now)
	}
	return nil
}

// Advance 只做入口处理，返回本次新结算的回合记录。
func (e *Engine) Advance(now int64) ([]turn.Log, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now0 || now < e.maxNow {
		return nil, ErrClock
	}
	base := len(e.logs)
	e.entry(now)
	e.maxNow = now
	return append([]turn.Log(nil), e.logs[base:]...), nil
}

// Log 返回自回合 from 起的全部已结算记录。
func (e *Engine) Log(from int) []turn.Log {
	e.mu.Lock()
	defer e.mu.Unlock()
	idx := from - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(e.logs) {
		return nil
	}
	return append([]turn.Log(nil), e.logs[idx:]...)
}

// entry 入口处理：now >= dl（取等超时）时以 ts=dl 超时结算并连锁，
// 直到 now 小于 dl。只由 now 与此前被接受的操作决定。
func (e *Engine) entry(now int64) {
	for now >= e.dl {
		e.settle(e.dl)
	}
}

// settle 以 ts 结算 cur，若新 cur 立即到齐则以同一 ts 连锁结算。
func (e *Engine) settle(ts int64) {
	for {
		lg := turn.Settle(e.cur, ts, e.players, e.get, e.repeat, e.drop, e.deactivate)
		e.logs = append(e.logs, lg)
		e.win.Clear(e.cur)
		e.cur++
		e.dl = ts + e.timeout
		if !e.arrived() {
			return
		}
	}
}

// arrived 到齐判定：活跃集合非空且每个活跃玩家都已提交 cur。
// 靠每回合活跃提交者计数实现，O(1)，不遍历玩家。
func (e *Engine) arrived() bool {
	return e.active > 0 && e.win.Count(e.cur) == e.active
}

func (e *Engine) get(p int) ([]byte, bool) { return e.win.Get(p, e.cur) }

// deactivate 在玩家被置为非活跃后修正各回合的活跃提交者计数。
// 非活跃者的存量输入保留，届时按实交处理，但不因此复活。
func (e *Engine) deactivate(p int) {
	e.active--
	for _, k := range e.win.Turns(p) {
		e.win.Bump(k, -1)
	}
}
