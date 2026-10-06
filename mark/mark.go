// Package mark 组合 part 与 backfill，提供实时提交、区间回填与双水位推进。
//
// 完整水位 W 只增不减；稳定水位 S = min(W, amin-1)（amin 为活跃作业区间
// 起点的最小值，无活跃作业时 S = W），S 可回退。所有方法可并发调用，
// 结果等价于某个串行顺序。所有变更操作带 now 且不得小于已接受的最大 now；
// 被拒绝的操作不落地任何过期取消、不推进时钟。
//
// 拒绝次序（只报第一个）：参数非法 > 时钟回退 > 作业不存在/已存在 >
// 各操作自身的状态类错误。
package mark

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/backfill"
	"ontology/part"
)

// MaxNow 是 now 的最大合法值。
const MaxNow = 1_000_000_000_000

var (
	ErrInvalid      = errors.New("mark: invalid argument")
	ErrClock        = errors.New("mark: clock regression")
	ErrHeld         = errors.New("mark: partition held by backfill job")
	ErrAckRegress   = errors.New("mark: ack regresses")
	ErrBeyondStable = errors.New("mark: ack beyond stable watermark")
)

// HeldError 携带占用该分区的作业名。
type HeldError struct {
	Job string
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("%v (job: %s)", ErrHeld, e.Job)
}

func (e *HeldError) Unwrap() error { return ErrHeld }

// Revision 是发给一个消费者的修订清单（分区号升序）。
type Revision struct {
	Consumer string
	Parts    []int
}

// Revisions 按消费者名字节序排列；清单为空的消费者不列出。
type Revisions []Revision

// System 是双水位推进器，协调分区表、回填作业与消费者确认。
type System struct {
	mu     sync.Mutex
	parts  *part.Table
	jobs   *backfill.Manager
	acks   map[string]int // 消费者 -> 已确认值；缺席视为 -1
	maxNow int            // 已接受的最大 now
}

// New 返回活跃作业数上限为 maxJobs 的系统。
func New(maxJobs int) *System {
	return &System{
		parts:  part.NewTable(),
		jobs:   backfill.NewManager(maxJobs),
		acks:   make(map[string]int),
		maxNow: -1,
	}
}

func validNow(now int) bool { return now >= 0 && now <= MaxNow }

func validPart(p int) bool { return p >= 0 && p <= part.MaxPart }

// W 返回完整水位。
func (s *System) W() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parts.W()
}

// S 返回稳定水位，只反映已落地的状态。
func (s *System) S() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.jobs.MinStartLanded()
	return stable(s.parts.W(), a, ok)
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
	if v, ok := s.acks[consumer]; ok {
		return v
	}
	return -1
}

func stable(w int, a int, ok bool) int {
	if ok && a-1 < w {
		return a - 1
	}
	return w
}

// Commit 实时提交：Missing 变为 Committed(ver=1)。
// 状态类拒绝次序：ErrHeld > ErrAlready。
func (s *System) Commit(p, now int) error {
	if !validPart(p) || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	if name, ok := s.jobs.Holder(p, now); ok {
		return &HeldError{Job: name}
	}
	if err := s.parts.Commit(p); err != nil {
		return err
	}
	s.jobs.Sweep(now)
	s.maxNow = now
	return nil
}

// Begin 开始回填作业，占用闭区间 [a,b]，deadline = now+ttl。
func (s *System) Begin(job string, a, b, ttl, now int) error {
	if job == "" || !validNow(now) ||
		a < 0 || a > b || b > part.MaxPart || b-a+1 > backfill.MaxRange ||
		ttl < 1 || ttl > backfill.MaxTTL {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	if err := s.jobs.Begin(job, a, b, ttl, now); err != nil {
		return err
	}
	s.maxNow = now
	return nil
}

// Stage 暂存一个分区的新数据（不可见，重复暂存幂等，不续期）。
func (s *System) Stage(job string, p, now int) error {
	if job == "" || !validPart(p) || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	if err := s.jobs.Stage(job, p, now); err != nil {
		return err
	}
	s.maxNow = now
	return nil
}

// Heartbeat 把 deadline 改为 now+ttl。
func (s *System) Heartbeat(job string, now int) error {
	if job == "" || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	if err := s.jobs.Heartbeat(job, now); err != nil {
		return err
	}
	s.maxNow = now
	return nil
}

// Abort 放弃作业并丢弃暂存。
func (s *System) Abort(job string, now int) error {
	if job == "" || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	if err := s.jobs.Abort(job, now); err != nil {
		return err
	}
	s.maxNow = now
	return nil
}

// Finish 原子提交：区间内每个分区 ver 加 1（Missing 视为 0），
// 作业结束、区间释放。返回发给各消费者的修订清单。
func (s *System) Finish(job string, now int) (Revisions, error) {
	if job == "" || !validNow(now) {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return nil, ErrClock
	}
	j, err := s.jobs.Finish(job, now)
	if err != nil {
		return nil, err
	}
	// 修订按提交前版本快照计算：提交前 ver>=1 且 p <= 消费者已确认值。
	revs := s.revisions(j.A, j.B)
	s.parts.BumpRange(j.A, j.B)
	s.maxNow = now
	return revs, nil
}

func (s *System) revisions(a, b int) Revisions {
	names := make([]string, 0, len(s.acks))
	for name := range s.acks {
		names = append(names, name)
	}
	sort.Strings(names)
	var out Revisions
	for _, name := range names {
		upto := s.acks[name]
		var ps []int
		for p := a; p <= b && p <= upto; p++ {
			if s.parts.Ver(p) >= 1 {
				ps = append(ps, p)
			}
		}
		if len(ps) > 0 {
			out = append(out, Revision{Consumer: name, Parts: ps})
		}
	}
	return out
}

// Ack 确认消费者已消费到 upto。状态类拒绝次序：
// ErrAckRegress > ErrBeyondStable。已确认值不因 S 回退而改变。
func (s *System) Ack(consumer string, upto, now int) error {
	if consumer == "" || !validPart(upto) || !validNow(now) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	last := -1
	if v, ok := s.acks[consumer]; ok {
		last = v
	}
	if upto < last {
		return ErrAckRegress
	}
	a, ok := s.jobs.MinStartAt(now)
	if upto > stable(s.parts.W(), a, ok) {
		return ErrBeyondStable
	}
	s.jobs.Sweep(now)
	s.acks[consumer] = upto
	s.maxNow = now
	return nil
}
