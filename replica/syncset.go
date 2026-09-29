package replica

import (
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// ReplicaID 是副本的唯一标识。空字符串不是合法标识。
type ReplicaID = string

// EndOffset 是日志结束位点：表示该副本已拥有日志 [0, EndOffset)。
type EndOffset = int64

// progress 记录单个副本的运行时状态。
type progress struct {
	id         ReplicaID
	end        EndOffset
	caughtUpAt time.Time
}

// Status 是一次查询得到的同步副本集快照。
type Status struct {
	// Leader 是当前领导者副本标识。
	Leader ReplicaID
	// HighWatermark 是已提交位点，只进不退。
	HighWatermark EndOffset
	// Synced 是当前同步副本集（ISR），含领导者。
	Synced []ReplicaID
	// Ends 是全部已知副本（含已被移出者）的日志结束位点。
	Ends map[ReplicaID]EndOffset
	// LastClock 是系统已观察到的最新时间。
	LastClock time.Time
}

// SyncSet 维护一个分区的同步副本集与高水位。
// 所有方法均可被多个执行体并发调用，内部以互斥锁保护。
type SyncSet struct {
	mu        sync.RWMutex
	leader    ReplicaID
	tolerance time.Duration
	lastClock time.Time

	// order 保留注册顺序，使快照与日志输出可复现。
	order  []ReplicaID
	progs  map[ReplicaID]*progress
	synced map[ReplicaID]bool

	hwm EndOffset

	logger *log.Logger
}

// NewSyncSet 创建同步副本集。
//
// leader 为领导者副本标识；tolerance 为跟随者允许落后的时长，
// 周期检查时“追上时间距当前时间严格大于 tolerance”才会被移出；
// start 为逻辑时钟的初始时间。
func NewSyncSet(leader ReplicaID, tolerance time.Duration, start time.Time, opts ...Option) (*SyncSet, error) {
	if leader == "" {
		return nil, ErrInvalidArgument
	}
	if tolerance < 0 {
		return nil, ErrInvalidArgument
	}
	if start.IsZero() {
		return nil, ErrInvalidArgument
	}
	cfg := config{logWriter: os.Stderr}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.logWriter == nil {
		cfg.logWriter = io.Discard
	}
	s := &SyncSet{
		leader:    leader,
		tolerance: tolerance,
		lastClock: start,
		order:     []ReplicaID{leader},
		progs: map[ReplicaID]*progress{
			// 领导者初始即追平（位点 0 == 高水位 0）。
			leader: {id: leader, end: 0, caughtUpAt: start},
		},
		synced: map[ReplicaID]bool{leader: true},
		hwm:    0,
		logger: log.New(cfg.logWriter, "[replica] ", log.LstdFlags|log.Lmicroseconds),
	}
	s.logger.Printf("new leader=%q tolerance=%s start=%s synced=%v hwm=%d",
		leader, tolerance, start.Format(time.RFC3339Nano), s.syncedList(), s.hwm)
	return s, nil
}

// AddReplica 注册一个新跟随者副本，初始位点为 0。
// 新副本只有在追平当前高水位后才会进入同步副本集。
func (s *SyncSet) AddReplica(id ReplicaID) error {
	if id == "" {
		s.reject("add", "id=%q: empty replica id", id)
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.leader || s.progs[id] != nil {
		s.rejectLocked("add", "id=%q: duplicate replica", id)
		return ErrInvalidArgument
	}
	s.order = append(s.order, id)
	s.progs[id] = &progress{id: id, end: 0}
	s.logger.Printf("add id=%q end=0 synced=%v hwm=%d decision=registered_outside_isr (end=0 < hwm unless hwm==0; joins on first fetch)",
		id, s.syncedList(), s.hwm)
	return nil
}

// Append 由领导者写入 n 条日志，推进领导者日志结束位点，
// 随后按同步副本集重算并推进高水位。now 必须不早于此前观察到的时间。
func (s *SyncSet) Append(now time.Time, n int64) (leaderEnd EndOffset, hwm EndOffset, err error) {
	if n < 0 {
		s.reject("append", "n=%d: negative append length", n)
		return 0, 0, ErrInvalidArgument
	}
	if now.IsZero() {
		s.reject("append", "zero timestamp", nil)
		return 0, 0, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock("append", now); err != nil {
		return 0, 0, err
	}
	lp := s.progs[s.leader]
	newEnd := lp.end + n
	if newEnd < lp.end {
		s.rejectLocked("append", "n=%d: end offset overflow", n)
		return 0, 0, ErrInvalidArgument
	}
	lp.end = newEnd
	// 领导者始终与自己追平。
	lp.caughtUpAt = now
	s.lastClock = now
	hwm = s.advanceHWM("after append")
	s.logger.Printf("append now=%s n=%d leader_end=%d hwm=%d synced=%v decision=leader_advanced; hwm=min(synced ends)",
		now.Format(time.RFC3339Nano), n, newEnd, hwm, s.syncedList())
	return newEnd, hwm, nil
}

// Fetch 由跟随者拉取：以其已拥有位点 end 更新自身进度。
// end 不得为负、不得超过领导者位点、不得小于该副本已报告的位点。
// 当 end 追平领导者位点时刷新“追上时间”；
// 当 end 追平当前高水位时，被移出的副本重新进入同步副本集。
func (s *SyncSet) Fetch(now time.Time, id ReplicaID, end EndOffset) error {
	if id == "" {
		s.reject("fetch", "id=%q: empty replica id", id)
		return ErrInvalidArgument
	}
	if now.IsZero() {
		s.reject("fetch", "id=%q: zero timestamp", id)
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.progs[id]
	if p == nil {
		s.rejectLocked("fetch", "id=%q: unknown replica", id)
		return ErrUnknownReplica
	}
	if id == s.leader {
		s.rejectLocked("fetch", "id=%q: leader does not fetch", id)
		return ErrInvalidArgument
	}
	if err := s.checkClock("fetch", now); err != nil {
		return err
	}
	leaderEnd := s.progs[s.leader].end
	if end < 0 {
		s.rejectLocked("fetch", "id=%q end=%d: negative offset", id, end)
		return ErrInvalidOffset
	}
	if end > leaderEnd {
		s.rejectLocked("fetch", "id=%q end=%d: ahead of leader_end=%d", id, end, leaderEnd)
		return ErrInvalidOffset
	}
	if end < p.end {
		s.rejectLocked("fetch", "id=%q end=%d: regressed from %d", id, end, p.end)
		return ErrInvalidOffset
	}

	p.end = end
	wasSynced := s.synced[id]
	caughtUp := false
	if end == leaderEnd {
		// 追平领导者：刷新追上时间（此时必然不低于高水位）。
		p.caughtUpAt = now
		caughtUp = true
	}
	if !wasSynced && caughtUp && end >= s.hwm {
		// 被移出/新注册副本重新加入的门槛：追上当前高水位。
		s.synced[id] = true
	}
	s.lastClock = now
	// 无论本次是追平推进还是重新加入，都按 ISR 重算高水位；
	// 位点单调保证结果只进不退。
	s.advanceHWM("after fetch")
	s.logger.Printf("fetch now=%s id=%q end=%d leader_end=%d hwm=%d synced=%v decision=caught_up=%v was_synced=%v",
		now.Format(time.RFC3339Nano), id, end, leaderEnd, s.hwm, s.syncedList(), caughtUp, wasSynced)
	return nil
}

// Sweep 执行周期性检查：
// 追上时间距 now 严格大于 tolerance 的跟随者被移出同步副本集，
// 随后高水位可能因新的最小者而被“顶住”（数值不会下降）。
// 返回本次被移出的副本标识。
func (s *SyncSet) Sweep(now time.Time) (removed []ReplicaID, err error) {
	if now.IsZero() {
		s.reject("sweep", "zero timestamp", nil)
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock("sweep", now); err != nil {
		return nil, err
	}
	s.lastClock = now
	limit := now.Add(-s.tolerance)
	// 按注册顺序判定，结果可复现。
	for _, id := range s.order {
		if id == s.leader || !s.synced[id] {
			continue
		}
		p := s.progs[id]
		// 严格大于容忍阈值才移出；恰好相等保留。能进入同步副本集
		// 的副本必然已追平过，caughtUpAt 不会是零值。
		if p.caughtUpAt.Before(limit) {
			delete(s.synced, id)
			removed = append(removed, id)
		}
	}
	hwm := s.advanceHWM("after sweep")
	s.logger.Printf("sweep now=%s tolerance=%s removed=%v hwm=%d synced=%v decision=remove iff lag strictly greater than tolerance",
		now.Format(time.RFC3339Nano), s.tolerance, removed, hwm, s.syncedList())
	return removed, nil
}

// Status 返回当前状态快照（副本顺序按注册顺序，保证可复现）。
func (s *SyncSet) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	synced := s.syncedList()
	ends := make(map[ReplicaID]EndOffset, len(s.progs))
	for _, id := range s.order {
		ends[id] = s.progs[id].end
	}
	return Status{
		Leader:        s.leader,
		HighWatermark: s.hwm,
		Synced:        synced,
		Ends:          ends,
		LastClock:     s.lastClock,
	}
}
