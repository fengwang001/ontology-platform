// Package mark 是系统门面：实时提交、回填作业编排、完整水位 W、稳定水位 S、
// 消费者确认与修订通知。
//
// 所有操作持单互斥锁，并发调用等价于某个串行顺序。变更操作携带 now，
// 不得小于已接受的最大 now；被拒操作不落地任何过期取消、不推进时钟。
// S = min(W, amin-1)，amin 为活跃作业区间起点最小值（无活跃作业时 S=W）；
// S 可回退，但消费者已确认值不随之改变。
package mark

import (
	"errors"
	"sort"
	"sync"

	"ontology/backfill"
	"ontology/part"
)

// 参数合法域。
const (
	MaxPart = 1_000_000         // 分区号上界（含）
	MaxNow  = 1_000_000_000_000 // now 上界（含）
	MaxSpan = 1000              // 回填区间长度上界（含）
	MaxTTL  = 1_000_000         // ttl 上界（含）
)

var (
	ErrInvalid      = errors.New("mark: invalid argument")
	ErrClockRegress = errors.New("mark: now regresses below accepted maximum")
	ErrAckRegress   = errors.New("mark: ack regresses below last acknowledged")
	ErrBeyondStable = errors.New("mark: ack beyond stable watermark")
)

// 再导出下层哨兵错误，消费方可单点引入并用 errors.Is 区分。
var (
	ErrAlready     = part.ErrAlready
	ErrNoJob       = backfill.ErrNoJob
	ErrJobExists   = backfill.ErrJobExists
	ErrTooManyJobs = backfill.ErrTooManyJobs
	ErrOverlap     = backfill.ErrOverlap
	ErrOutOfRange  = backfill.ErrOutOfRange
	ErrIncomplete  = backfill.ErrIncomplete
	ErrHeld        = backfill.ErrHeld
)

// Revision 是 Finish 成功时发给一个消费者的修订清单（Parts 升序）。
type Revision struct {
	Consumer string
	Parts    []int
}

// System 是双水位推进器。零值不可用，请用 New 构造。
type System struct {
	mu     sync.Mutex
	parts  *part.Store
	jobs   *backfill.Manager
	acked  map[string]int // 消费者确认值，首次确认前视为 -1
	maxNow int            // 已接受的最大 now
}

// New 返回活跃作业数上限为 maxJobs 的系统。
func New(maxJobs int) *System {
	return &System{
		parts: part.NewStore(),
		jobs:  backfill.NewManager(maxJobs),
		acked: make(map[string]int),
	}
}

func validNow(now int) bool { return 0 <= now && now <= MaxNow }

func validPart(p int) bool { return 0 <= p && p <= MaxPart }

// Commit 实时提交分区 p。拒绝次序：ErrInvalid > ErrClockRegress > ErrHeld > ErrAlready。
func (s *System) Commit(p, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPart(p) || !validNow(now) {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockRegress
	}
	if name, held := s.jobs.Holder(p, now); held {
		return &backfill.Error{Kind: backfill.ErrHeld, Job: name, Part: p}
	}
	if err := s.parts.Commit(p); err != nil {
		return err
	}
	s.jobs.Sweep(now)
	s.maxNow = now
	return nil
}

// Begin 开始回填作业。拒绝次序：ErrInvalid > ErrClockRegress > ErrJobExists >
// ErrTooManyJobs > ErrOverlap。
func (s *System) Begin(job string, a, b, ttl, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) || a < 0 || a > b || b > MaxPart ||
		b-a+1 > MaxSpan || ttl < 1 || ttl > MaxTTL {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockRegress
	}
	if err := s.jobs.Begin(job, a, b, ttl, now); err != nil {
		return err
	}
	s.maxNow = now
	return nil
}

// Stage 暂存作业区间内一个分区的新数据（不可见，幂等，不续期）。
func (s *System) Stage(job string, p, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validPart(p) || !validNow(now) {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockRegress
	}
	j := s.jobs.Get(job, now)
	if j == nil {
		return &backfill.Error{Kind: backfill.ErrNoJob, Job: job}
	}
	if p < j.A || p > j.B {
		return &backfill.Error{Kind: backfill.ErrOutOfRange, Job: job, Part: p}
	}
	s.jobs.Sweep(now)
	j.Stage(p)
	s.maxNow = now
	return nil
}

// Heartbeat 把作业的 deadline 改为 now+ttl。
func (s *System) Heartbeat(job string, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockRegress
	}
	j := s.jobs.Get(job, now)
	if j == nil {
		return &backfill.Error{Kind: backfill.ErrNoJob, Job: job}
	}
	s.jobs.Sweep(now)
	j.Deadline = now + j.TTL
	s.maxNow = now
	return nil
}

// Finish 原子提交作业：区间内每个分区都已暂存才可提交，否则报
// ErrIncomplete（带最小未暂存分区号）。成功时区间内每个分区 ver 加 1，
// 作业结束、区间释放，并返回发给各消费者的修订清单（按消费者名字节序，
// 清单为空者不列出）。
func (s *System) Finish(job string, now int) ([]Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) {
		return nil, ErrInvalid
	}
	if now < s.maxNow {
		return nil, ErrClockRegress
	}
	j := s.jobs.Get(job, now)
	if j == nil {
		return nil, &backfill.Error{Kind: backfill.ErrNoJob, Job: job}
	}
	if p, ok := j.FirstUnstaged(); ok {
		return nil, &backfill.Error{Kind: backfill.ErrIncomplete, Job: job, Part: p}
	}
	s.jobs.Sweep(now)
	pre := s.parts.Bump(j.A, j.B)
	s.jobs.Remove(j)
	s.maxNow = now
	return s.revisions(j.A, pre), nil
}

// revisions 对每个消费者列出 [a,b] 内提交前 ver>=1 且 p <= 已确认值的分区。
func (s *System) revisions(a int, pre []int) []Revision {
	consumers := make([]string, 0, len(s.acked))
	for c := range s.acked {
		consumers = append(consumers, c)
	}
	sort.Strings(consumers)
	var revs []Revision
	for _, c := range consumers {
		upto := s.acked[c]
		var ps []int
		for i, p := 0, a; p <= a+len(pre)-1 && p <= upto; i, p = i+1, p+1 {
			if pre[i] >= 1 {
				ps = append(ps, p)
			}
		}
		if len(ps) > 0 {
			revs = append(revs, Revision{Consumer: c, Parts: ps})
		}
	}
	return revs
}

// Abort 放弃作业并丢弃暂存，区间释放。
func (s *System) Abort(job string, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNow(now) {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockRegress
	}
	j := s.jobs.Get(job, now)
	if j == nil {
		return &backfill.Error{Kind: backfill.ErrNoJob, Job: job}
	}
	s.jobs.Sweep(now)
	s.jobs.Remove(j)
	s.maxNow = now
	return nil
}

// Ack 消费者确认已消费到 upto。拒绝次序：ErrInvalid > ErrClockRegress >
// ErrAckRegress > ErrBeyondStable。已确认值不因 S 回退而改变。
func (s *System) Ack(consumer string, upto, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if upto < -1 || !validNow(now) {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClockRegress
	}
	if upto < s.ackedLocked(consumer) {
		return ErrAckRegress
	}
	if upto > s.stableLive(now) {
		return ErrBeyondStable
	}
	s.jobs.Sweep(now)
	s.acked[consumer] = upto
	s.maxNow = now
	return nil
}

// stableLive 按 now 处的活跃作业计算稳定水位（用于 Ack 判定）。
func (s *System) stableLive(now int) int {
	w := s.parts.W()
	if amin, ok := s.jobs.MinStartLive(now); ok && amin-1 < w {
		return amin - 1
	}
	return w
}

// W 返回完整水位（只增不减）。
func (s *System) W() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parts.W()
}

// S 返回稳定水位，只反映已落地的状态（含已过期但未落地取消的作业）。
func (s *System) S() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.parts.W()
	if amin, ok := s.jobs.MinStartLanded(); ok && amin-1 < w {
		return amin - 1
	}
	return w
}

// Ver 返回分区版本，Missing 为 0。
func (s *System) Ver(p int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parts.Ver(p)
}

// Acked 返回消费者已确认值，首次确认前为 -1。
func (s *System) Acked(consumer string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ackedLocked(consumer)
}

func (s *System) ackedLocked(consumer string) int {
	if v, ok := s.acked[consumer]; ok {
		return v
	}
	return -1
}

// Probes 返回推进 W 累计探测的分区数。
func (s *System) Probes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parts.Probes()
}

// Cmps 返回 Begin 相交判定累计比较的活跃作业数。
func (s *System) Cmps() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs.Cmps()
}
