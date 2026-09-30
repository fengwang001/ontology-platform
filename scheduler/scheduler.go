// Package scheduler 实现一个带到期保障的磁盘请求调度器。
//
// 读、写请求分队列存放，派发时先按"方向选择 + 写饿计数"决定读或写，
// 再在该方向内按"到期优先，否则沿磁头位置扫描（SCAN），到底回绕"选择请求。
package scheduler

import "sync"

// Op 表示请求方向：读或写。
type Op int

const (
	Read Op = iota
	Write
)

func (o Op) String() string {
	switch o {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "unknown"
	}
}

// Request 是一个磁盘 I/O 请求。
type Request struct {
	ID       string // 请求标识，全局唯一
	Sector   int64  // 目标扇区，必须非负
	Op       Op     // 读或写
	SubmitAt int64  // 提交时刻（单调时钟读数，由调用方保证时钟语义）
}

// Config 为调度器配置。
type Config struct {
	ReadDeadline  int64 // Er：读到期时长，必须为正
	WriteDeadline int64 // Ew：写到期时长，必须为正
	WriteStarveX  int   // X：连续被优先的读次数上限，必须 >= 1
}

// Reason 标识单个请求被选中的依据。
type Reason int

const (
	ReasonDeadline Reason = iota // 到期：队首已到期
	ReasonScan                   // 扫描：扇区不小于磁头位置的最小扇区者
	ReasonWrap                   // 回绕：没有不小于磁头位置者，取最小扇区者
)

func (r Reason) String() string {
	switch r {
	case ReasonDeadline:
		return "deadline"
	case ReasonScan:
		return "scan"
	case ReasonWrap:
		return "wrap"
	default:
		return "unknown"
	}
}

// DispatchResult 是一次派发的结果。
type DispatchResult struct {
	Req         Request // 被派发的请求
	Reason      Reason  // 到期 / 扫描 / 回绕
	WriteForced bool    // 是否由写饿强制选写（饿计数已达 X 且写队列非空）
}

// State 是调度器当前状态的只读快照。
type State struct {
	Head            int64     // 磁头位置
	ReadQueue       []Request // 读队列，按提交先后排列
	WriteQueue      []Request // 写队列，按提交先后排列
	StarveCount     int       // 写队列持续非空期间连续优先派发读的次数
	LastSeenTime    int64     // 见过的最大时刻
	DispatchedCount int       // 累计派发次数
	CanceledCount   int       // 累计取消次数
}

// Logger 用于记录每次操作的输入、输出与判定依据（可为 nil）。
type Logger interface {
	Logf(format string, args ...any)
}

// entry 是内部队列条目。同扇区、同提交时刻的先后由 seq 保证。
type entry struct {
	req Request
	seq uint64
}

// Scheduler 是并发安全的磁盘请求调度器。
type Scheduler struct {
	mu     sync.Mutex
	cfg    Config
	log    Logger
	reads  []entry
	writes []entry
	// known 记录所有出现过的标识及其终态，保证"取消不存在"与"取消已派发"可区分。
	known map[string]terminal
	head  int64
	// starve 为写队列持续非空期间连续被优先的读派发次数。
	starve       int
	lastTime     int64
	nextSeq      uint64
	dispatchedNo int
	canceledNo   int
}

type terminal int

const (
	termCanceled terminal = iota
	termDispatched
)

// New 创建调度器并校验配置。
func New(cfg Config, log Logger) (*Scheduler, error) {
	if cfg.ReadDeadline <= 0 {
		return nil, ErrInvalidReadDeadline
	}
	if cfg.WriteDeadline <= 0 {
		return nil, ErrInvalidWriteDeadline
	}
	if cfg.WriteStarveX < 1 {
		return nil, ErrInvalidStarveX
	}
	return &Scheduler{
		cfg:   cfg,
		log:   log,
		known: make(map[string]terminal),
	}, nil
}

// Submit 提交一个请求。
func (s *Scheduler) Submit(r Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 多因顺序：时钟回拨、扇区为负、标识重复。
	if r.SubmitAt < s.lastTime {
		s.logf("submit rejected id=%q sector=%d op=%s at=%d => clock rewind", r.ID, r.Sector, r.Op, r.SubmitAt)
		return ErrClockRewind
	}
	if r.Sector < 0 {
		s.logf("submit rejected id=%q sector=%d at=%d => negative sector", r.ID, r.Sector, r.SubmitAt)
		return ErrNegativeSector
	}
	if _, dup := s.known[r.ID]; dup {
		s.logf("submit rejected id=%q at=%d => duplicate id", r.ID, r.SubmitAt)
		return ErrDuplicateID
	}
	for _, e := range s.reads {
		if e.req.ID == r.ID {
			s.logf("submit rejected id=%q at=%d => duplicate id", r.ID, r.SubmitAt)
			return ErrDuplicateID
		}
	}
	for _, e := range s.writes {
		if e.req.ID == r.ID {
			s.logf("submit rejected id=%q at=%d => duplicate id", r.ID, r.SubmitAt)
			return ErrDuplicateID
		}
	}

	if r.SubmitAt > s.lastTime {
		s.lastTime = r.SubmitAt
	}
	s.nextSeq++
	e := entry{req: r, seq: s.nextSeq}
	if r.Op == Read {
		s.reads = append(s.reads, e)
	} else {
		s.writes = append(s.writes, e)
	}
	s.logf("submit accepted id=%q sector=%d op=%s at=%d (queues read=%d write=%d head=%d starve=%d)",
		r.ID, r.Sector, r.Op, r.SubmitAt, len(s.reads), len(s.writes), s.head, s.starve)
	return nil
}

// Cancel 取消一个尚未派发的请求。
func (s *Scheduler) Cancel(id string, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 多因顺序：时钟回拨、其余。
	if now < s.lastTime {
		s.logf("cancel rejected id=%q at=%d => clock rewind", id, now)
		return ErrClockRewind
	}
	if now > s.lastTime {
		s.lastTime = now
	}

	if term, ok := s.known[id]; ok {
		if term == termDispatched {
			s.logf("cancel rejected id=%q at=%d => already dispatched", id, now)
			return ErrAlreadyDispatched
		}
		s.logf("cancel rejected id=%q at=%d => not found (already canceled)", id, now)
		return ErrNotFound
	}

	if idx := indexOf(s.reads, id); idx >= 0 {
		s.removeRead(idx)
		s.known[id] = termCanceled
		s.canceledNo++
		s.logf("cancel accepted id=%q at=%d op=read (queues read=%d write=%d head=%d starve=%d)",
			id, now, len(s.reads), len(s.writes), s.head, s.starve)
		return nil
	}
	if idx := indexOf(s.writes, id); idx >= 0 {
		s.removeWrite(idx)
		s.known[id] = termCanceled
		s.canceledNo++
		s.logf("cancel accepted id=%q at=%d op=write (queues read=%d write=%d head=%d starve=%d)",
			id, now, len(s.reads), len(s.writes), s.head, s.starve)
		return nil
	}

	s.logf("cancel rejected id=%q at=%d => not found", id, now)
	return ErrNotFound
}

// Dispatch 在 now 时刻派发一个请求。
func (s *Scheduler) Dispatch(now int64) (DispatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 多因顺序：时钟回拨、其余。
	if now < s.lastTime {
		s.logf("dispatch rejected at=%d => clock rewind (head=%d starve=%d)", now, s.head, s.starve)
		return DispatchResult{}, ErrClockRewind
	}
	if now > s.lastTime {
		s.lastTime = now
	}
	if len(s.reads) == 0 && len(s.writes) == 0 {
		s.logf("dispatch rejected at=%d => empty queue", now)
		return DispatchResult{}, ErrEmptyQueue
	}

	// 方向选择：读队列非空且（写队列为空或饿计数 < X）选读，否则选写。
	readNonEmpty := len(s.reads) > 0
	writeNonEmpty := len(s.writes) > 0
	forcedWrite := false
	chooseRead := readNonEmpty && (!writeNonEmpty || s.starve < s.cfg.WriteStarveX)
	if !chooseRead && writeNonEmpty {
		forcedWrite = s.starve >= s.cfg.WriteStarveX
	}

	var chosen entry
	var reason Reason
	var idx int
	if chooseRead {
		chosen, idx, reason = s.pickInDirection(s.reads, now, s.cfg.ReadDeadline, s.head)
		s.removeRead(idx)
		// 饿计数只在"派发读且此刻写队列非空"时加一；写队列为空则不变。
		if writeNonEmpty {
			s.starve++
		}
	} else {
		chosen, idx, reason = s.pickInDirection(s.writes, now, s.cfg.WriteDeadline, s.head)
		s.removeWrite(idx)
		// 派发写时饿计数清零（恰满 X 被强制写也不例外）。
		s.starve = 0
	}

	s.head = chosen.req.Sector
	s.known[chosen.req.ID] = termDispatched
	s.dispatchedNo++

	res := DispatchResult{Req: chosen.req, Reason: reason, WriteForced: forcedWrite}
	s.logf("dispatch id=%q sector=%d op=%s at=%d reason=%s writeForced=%t (head=%d starve=%d queues read=%d write=%d)",
		chosen.req.ID, chosen.req.Sector, chosen.req.Op, now, reason, forcedWrite,
		s.head, s.starve, len(s.reads), len(s.writes))
	return res, nil
}

// Snapshot 返回当前状态的深拷贝。
func (s *Scheduler) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := State{
		Head:            s.head,
		StarveCount:     s.starve,
		LastSeenTime:    s.lastTime,
		DispatchedCount: s.dispatchedNo,
		CanceledCount:   s.canceledNo,
	}
	cp.ReadQueue = make([]Request, len(s.reads))
	for i, e := range s.reads {
		cp.ReadQueue[i] = e.req
	}
	cp.WriteQueue = make([]Request, len(s.writes))
	for i, e := range s.writes {
		cp.WriteQueue[i] = e.req
	}
	return cp
}

// pickInDirection 在一个方向的队列内按规则选择请求。
//  1. 队首（提交最早者）满足 now-submitAt >= deadline（恰等也算到期）则派发队首；
//  2. 否则取扇区不小于 head 的最小扇区者（同扇区取提交在先者）；
//  3. 没有则回绕取最小扇区者（同扇区取提交在先者）。
func (s *Scheduler) pickInDirection(q []entry, now, deadline, head int64) (entry, int, Reason) {
	if now-q[0].req.SubmitAt >= deadline {
		return q[0], 0, ReasonDeadline
	}

	best := -1
	for i := range q {
		if q[i].req.Sector < head {
			continue
		}
		if best < 0 || lessEntry(q[i], q[best]) {
			best = i
		}
	}
	if best >= 0 {
		return q[best], best, ReasonScan
	}

	for i := range q {
		if best < 0 || lessEntry(q[i], q[best]) {
			best = i
		}
	}
	return q[best], best, ReasonWrap
}

// lessEntry 定义扫描/回绕挑选时的顺序：扇区升序，同扇区提交在先（seq 升序）优先。
func lessEntry(a, b entry) bool {
	if a.req.Sector != b.req.Sector {
		return a.req.Sector < b.req.Sector
	}
	return a.seq < b.seq
}

func indexOf(q []entry, id string) int {
	for i := range q {
		if q[i].req.ID == id {
			return i
		}
	}
	return -1
}

func (s *Scheduler) removeRead(i int) {
	s.reads = append(s.reads[:i], s.reads[i+1:]...)
}

func (s *Scheduler) removeWrite(i int) {
	s.writes = append(s.writes[:i], s.writes[i+1:]...)
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Logf(format, args...)
	}
}
