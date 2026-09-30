// Package scheduler 实现带到期保障与防写饿死的磁盘请求调度器。
package scheduler

import (
	"errors"
	"sync"
)

// Request 是一个磁盘读写请求。
type Request struct {
	ID        string // 请求标识
	Sector    int64  // 目标扇区（不得为负）
	Write     bool   // true 为写请求，false 为读请求
	Submitted int64  // 提交时刻（单调非降）
}

// Config 是调度器配置。
type Config struct {
	ReadExpire  int64 // 读到期时长 Er，必须为正
	WriteExpire int64 // 写到期时长 Ew，必须为正
	WriteStarve int   // 连续优先读次数上限 X，必须 >= 1
}

// Direction 表示本次派发选择的方向。
type Direction string

const (
	Read  Direction = "read"
	Write Direction = "write"
)

// Reason 表示本次派发在方向内的选取依据。
type Reason string

const (
	Expired    Reason = "expired"
	Scan       Reason = "scan"
	WrapAround Reason = "wrap_around"
)

// DispatchResult 描述一次派发的结果与判定依据。
type DispatchResult struct {
	Request      Request   // 被派发的请求
	Direction    Direction // 实际派发方向
	Reason       Reason    // 到期 / 扫描 / 回绕
	ForcedWrite  bool      // 是否由写饿规则强制选写
	HeadPosition int64     // 派发后的磁头位置
}

// 可区分的拒绝原因。
var (
	ErrClockRewind       = errors.New("scheduler: time earlier than a previously seen time")
	ErrNegativeSector    = errors.New("scheduler: negative sector")
	ErrDuplicateID       = errors.New("scheduler: duplicate request id")
	ErrInvalidConfig     = errors.New("scheduler: invalid config")
	ErrCancelNotFound    = errors.New("scheduler: cancel target does not exist")
	ErrAlreadyDispatched = errors.New("scheduler: cancel target already dispatched")
	ErrNoPendingRequests = errors.New("scheduler: no pending requests to dispatch")
)

// entry 给请求附加一个全局提交序号，用于同扇区等场景下的稳定先后判定。
type entry struct {
	req Request
	seq uint64
}

// Scheduler 是并发安全的磁盘请求调度器。
type Scheduler struct {
	mu sync.Mutex

	cfg Config

	reads  []entry // 读队列，按提交先后排列
	writes []entry // 写队列，按提交先后排列

	// seen 记录所有出现过的标识及其终态，保证「派发一次或取消一次」且标识不可复用。
	seen     map[string]reqState
	head     int64  // 磁头位置，初始为 0
	starving int    // 饿计数：写队列持续非空期间连续优先派发的读次数
	lastTime int64  // 此前见过的最大时刻
	nextSeq  uint64 // 下一个提交序号
}

type reqState uint8

const (
	statePending    reqState = iota // 尚未派发、尚未取消
	stateDispatched                 // 已派发
	stateCancelled                  // 已取消
)

// New 创建调度器；配置非法时返回 ErrInvalidConfig。
func New(cfg Config) (*Scheduler, error) {
	if cfg.ReadExpire <= 0 || cfg.WriteExpire <= 0 || cfg.WriteStarve < 1 {
		return nil, ErrInvalidConfig
	}
	return &Scheduler{
		cfg:  cfg,
		seen: make(map[string]reqState),
	}, nil
}

// Submit 提交一个请求。
// 多因同时成立时只报第一个：时钟回拨、扇区为负、标识重复。
func (s *Scheduler) Submit(req Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Submitted < s.lastTime {
		return ErrClockRewind
	}
	if req.Sector < 0 {
		return ErrNegativeSector
	}
	if _, exists := s.seen[req.ID]; exists {
		return ErrDuplicateID
	}

	s.lastTime = req.Submitted
	e := entry{req: req, seq: s.nextSeq}
	s.nextSeq++
	s.seen[req.ID] = statePending
	if req.Write {
		s.writes = append(s.writes, e)
	} else {
		s.reads = append(s.reads, e)
	}
	return nil
}

// Cancel 取消一个尚未派发的请求。
// 先校验时钟回拨；标识不存在（从未提交）与已派发（或已取消）分别报错。
func (s *Scheduler) Cancel(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.lastTime {
		return ErrClockRewind
	}
	state, exists := s.seen[id]
	if !exists {
		return ErrCancelNotFound
	}
	if state != statePending {
		return ErrAlreadyDispatched
	}

	s.lastTime = now
	if e, ok := s.findPending(id); ok {
		if e.req.Write {
			s.writes = removeAt(s.writes, e.idx)
		} else {
			s.reads = removeAt(s.reads, e.idx)
		}
	}
	s.seen[id] = stateCancelled
	return nil
}

type locatedEntry struct {
	entry
	idx int
}

func (s *Scheduler) findPending(id string) (locatedEntry, bool) {
	for i, e := range s.reads {
		if e.req.ID == id {
			return locatedEntry{entry: e, idx: i}, true
		}
	}
	for i, e := range s.writes {
		if e.req.ID == id {
			return locatedEntry{entry: e, idx: i}, true
		}
	}
	return locatedEntry{}, false
}

func removeAt(q []entry, idx int) []entry {
	return append(q[:idx], q[idx+1:]...)
}

// Dispatch 在时刻 now 派发一个请求。
// 先选方向，再在方向内按「到期队首 → 正向扫描 → 回绕」选取。
func (s *Scheduler) Dispatch(now int64) (DispatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.lastTime {
		return DispatchResult{}, ErrClockRewind
	}
	if len(s.reads) == 0 && len(s.writes) == 0 {
		return DispatchResult{}, ErrNoPendingRequests
	}

	// 方向选择：读队列非空且（写队列为空或饿计数小于 X）选读，否则选写。
	dir := Read
	forced := false
	if len(s.reads) == 0 || (len(s.writes) > 0 && s.starving >= s.cfg.WriteStarve) {
		dir = Write
		if len(s.reads) > 0 {
			forced = true
		}
	}

	queue := s.reads
	expire := s.cfg.ReadExpire
	if dir == Write {
		queue = s.writes
		expire = s.cfg.WriteExpire
	}

	picked, reason := selectInDirection(queue, now, expire, s.head)

	// 饿计数更新。
	switch {
	case dir == Write:
		s.starving = 0
	case len(s.writes) > 0:
		s.starving++
	}

	req := queue[picked].req
	if dir == Write {
		s.writes = removeAt(s.writes, picked)
	} else {
		s.reads = removeAt(s.reads, picked)
	}
	s.seen[req.ID] = stateDispatched
	s.head = req.Sector
	s.lastTime = now

	return DispatchResult{
		Request:      req,
		Direction:    dir,
		Reason:       reason,
		ForcedWrite:  forced,
		HeadPosition: req.Sector,
	}, nil
}

// selectInDirection 执行方向内选择，返回被选下标与依据。
// 队列按提交先后排列：队首恰等到期（含等于）则直接派发队首；
// 否则取扇区 >= head 的最小扇区者（同扇区提交在先）；
// 再否则回绕取全队列最小扇区者（同扇区提交在先）。
func selectInDirection(q []entry, now, expire, head int64) (int, Reason) {
	if now-q[0].req.Submitted >= expire {
		return 0, Expired
	}

	best := -1
	for i, e := range q {
		if e.req.Sector < head {
			continue
		}
		if best == -1 || sectorLess(e, q[best]) {
			best = i
		}
	}
	if best != -1 {
		return best, Scan
	}

	best = 0
	for i := 1; i < len(q); i++ {
		if sectorLess(q[i], q[best]) {
			best = i
		}
	}
	return best, WrapAround
}

// sectorLess 按扇区升序、扇区相同按提交先后（提交时刻再相同按提交序号）比较。
func sectorLess(a, b entry) bool {
	if a.req.Sector != b.req.Sector {
		return a.req.Sector < b.req.Sector
	}
	if a.req.Submitted != b.req.Submitted {
		return a.req.Submitted < b.req.Submitted
	}
	return a.seq < b.seq
}

// Snapshot 是调度器的可观测状态快照。
type Snapshot struct {
	Head        int64
	StarveCount int
	LastTime    int64
	ReadQueue   []Request
	WriteQueue  []Request
}

// Query 返回当前状态的深拷贝快照。
func (s *Scheduler) Query() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Head:        s.head,
		StarveCount: s.starving,
		LastTime:    s.lastTime,
		ReadQueue:   cloneEntries(s.reads),
		WriteQueue:  cloneEntries(s.writes),
	}
}

func cloneEntries(q []entry) []Request {
	if len(q) == 0 {
		return []Request{}
	}
	out := make([]Request, len(q))
	for i, e := range q {
		out[i] = e.req
	}
	return out
}
