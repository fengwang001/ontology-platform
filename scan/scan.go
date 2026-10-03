// Package scan 提供无变更日志源的轮询同步删除推断器：
// 分区扫描会话（Begin/Report/End）、连续缺席删除判定（absent）
// 与批量删除熔断/人工放行（guard）。
package scan

import (
	"errors"
	"sync"

	"ontology/absent"
	"ontology/guard"
)

var (
	// ErrInvalidParam 参数非法：越界、分区或键不匹配、rows 内键重复等。
	ErrInvalidParam = errors.New("scan: invalid parameter")
	// ErrUnauthorized Approve 的 role 不等于 2。
	ErrUnauthorized = errors.New("scan: approve role must be 2")
	// ErrClockRollback now 小于已接受操作的最大 now。
	ErrClockRollback = errors.New("scan: clock rollback")
	// ErrBusy 该分区已有打开的扫描会话。
	ErrBusy = errors.New("scan: scan session already open")
	// ErrNoSession epoch 与该分区当前打开会话不匹配（含无打开会话）。
	ErrNoSession = errors.New("scan: no matching scan session")
	// ErrNotSuspect 对非 Suspect 分区请求放行。
	ErrNotSuspect = errors.New("scan: partition is not suspect")
)

// Row 是 Report 的一行。
type Row struct {
	K int64
	V int64
}

// WriteOutcome 描述一次 Report 单项写入的结果。
type WriteOutcome int

const (
	Same   WriteOutcome = iota // 键已存在且值相同
	Insert                     // 新键
	Update                     // 值不同
)

// Engine 是删除推断器；零值不可用，须用 New 构造。
type Engine struct {
	partCount int
	xPercent  int

	mu     sync.Mutex // 保护 maxNow（全局单调时钟）
	maxNow int64

	shards []*shard
}

type session struct {
	seen map[int64]struct{} // 本会话已见键
}

type shard struct {
	mu      sync.Mutex // 保护本分区全部状态
	epoch   int
	tab     *absent.Partition
	cb      *guard.Guard
	current *session // 非 nil 当且仅当有打开会话
}

// New 构造引擎。p 为分区数（1..16），k 为连续缺席判定轮数（1..10），
// x 为熔断比例百分数（1..100）。参数越界返回 ErrInvalidParam。
func New(p, k, x int) (*Engine, error) {
	if p < 1 || p > 16 || k < 1 || k > 10 || x < 1 || x > 100 {
		return nil, ErrInvalidParam
	}
	e := &Engine{partCount: p, xPercent: x, shards: make([]*shard, p)}
	for i := 0; i < p; i++ {
		e.shards[i] = &shard{tab: absent.New(k), cb: guard.New()}
	}
	return e, nil
}

// commitClock 尝试以 now 推进全局时钟。调用方必须持有目标分片锁。
// 时钟回退时返回 ErrClockRollback 且不改变任何状态；接受则推进 maxNow，
// 并返回推进前的上一接受值，供随后状态校验失败时原样恢复。
func (e *Engine) commitClock(now int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.maxNow {
		return 0, ErrClockRollback
	}
	prev := e.maxNow
	e.maxNow = now
	return prev, nil
}

// restoreClock 在状态类拒绝时把 maxNow 恢复为推进前的值；now 不变。
func (e *Engine) restoreClock(prev int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.maxNow = prev
}

// Begin 打开分区 part 的扫描会话；已有打开会话返回 ErrBusy。
// 接受时该分区 epoch 加 1，并返回新 epoch。
func (e *Engine) Begin(part int, now int64) (epoch int, err error) {
	if part < 0 || part >= e.partCount || now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidParam
	}
	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, err := e.commitClock(now)
	if err != nil {
		return 0, err
	}
	if s.current != nil {
		// 状态错误回退时钟：被拒不改最大 now。
		e.restoreClock(prev)
		return 0, ErrBusy
	}
	s.epoch++
	s.current = &session{seen: make(map[int64]struct{})}
	return s.epoch, nil
}

// Report 在 part 的 epoch 会话内逐项写入 rows：
// 新键 Insert、值不同 Update、相同 Same；所见键 a 置 0 并标记本会话已见。
// 返回与 rows 等长、逐项对应的写入结果。
func (e *Engine) Report(part, epoch int, rows []Row, now int64) ([]WriteOutcome, error) {
	if part < 0 || part >= e.partCount || now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	dup := make(map[int64]struct{}, len(rows))
	for _, r := range rows {
		if r.K < 1 || r.K > 1_000_000_000 || r.V < -1_000_000_000 || r.V > 1_000_000_000 {
			return nil, ErrInvalidParam
		}
		if int(r.K%int64(e.partCount)) != part {
			return nil, ErrInvalidParam
		}
		if _, ok := dup[r.K]; ok {
			return nil, ErrInvalidParam
		}
		dup[r.K] = struct{}{}
	}

	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, err := e.commitClock(now)
	if err != nil {
		return nil, err
	}
	if s.current == nil || s.epoch != epoch {
		e.restoreClock(prev)
		return nil, ErrNoSession
	}

	outcomes := make([]WriteOutcome, len(rows))
	for i, r := range rows {
		outcomes[i] = WriteOutcome(s.tab.Observe(r.K, r.V, s.current.seen))
	}
	return outcomes, nil
}

// EndResult 是 End 的返回：删除数与本轮是否触发熔断。
type EndResult struct {
	Deleted int
	Tripped bool
}

// End 关闭 part 的 epoch 会话。complete=false 不做缺席统计；
// complete=true 累计未见键 a、按比例判定熔断或删除候选。
func (e *Engine) End(part, epoch int, complete bool, now int64) (EndResult, error) {
	if part < 0 || part >= e.partCount || now < 0 || now > 1_000_000_000_000 {
		return EndResult{}, ErrInvalidParam
	}
	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, err := e.commitClock(now)
	if err != nil {
		return EndResult{}, err
	}
	if s.current == nil || s.epoch != epoch {
		e.restoreClock(prev)
		return EndResult{}, ErrNoSession
	}
	seen := s.current.seen
	s.current = nil // 关闭会话

	if !complete {
		return EndResult{}, nil // 异常中止：不做任何缺席统计
	}

	s.tab.Accumulate(seen)
	n0, candidates := s.tab.Snapshot()

	if s.cb.Suspected() {
		// Suspect 期间：只累计 a，不删除、不再判熔断。
		return EndResult{}, nil
	}
	if s.cb.ShouldTrip(len(candidates), n0, e.xPercent) {
		// 熔断：C 一个不删，a 保持。
		s.cb.Trip()
		return EndResult{Deleted: 0, Tripped: true}, nil
	}
	deleted := s.tab.DeleteCandidates(candidates)
	return EndResult{Deleted: deleted}, nil
}

// Approve 人工放行：role 必须为 2，分区必须为 Suspect；
// 清除 Suspect 并立即删除当前 a>=K 的键（不再判比例），返回删除数。
func (e *Engine) Approve(role, part int, now int64) (int, error) {
	if part < 0 || part >= e.partCount || now < 0 || now > 1_000_000_000_000 {
		return 0, ErrInvalidParam
	}
	if role != 2 {
		return 0, ErrUnauthorized
	}
	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, err := e.commitClock(now)
	if err != nil {
		return 0, err
	}
	if !s.cb.Suspected() {
		e.restoreClock(prev)
		return 0, ErrNotSuspect
	}
	if err := s.cb.Release(role); err != nil {
		e.restoreClock(prev)
		return 0, err
	}
	// 放行不是赦免：删除当前仍 a>=K 的键；其间被重新见到而归 0 的键保留。
	return s.tab.DeleteAbsent(), nil
}
