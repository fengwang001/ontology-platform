// Package backfill 管理回填作业：区间占用、暂存与原子提交。
//
// 活跃作业区间两两不相交。作业在 now >= deadline 时过期（恰等即过期）：
// 校验时过期作业视同不存在，但其取消只在某个携带 now >= deadline 的操作
// 被接受时才通过 Sweep 落地；被拒操作不落地任何取消。
// Manager 不加锁，并发串行化由上层（mark 门面）负责。
package backfill

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrNoJob       = errors.New("backfill: job not found (or expired)")
	ErrJobExists   = errors.New("backfill: job already exists")
	ErrTooManyJobs = errors.New("backfill: too many active jobs")
	ErrOverlap     = errors.New("backfill: interval overlaps an active job")
	ErrOutOfRange  = errors.New("backfill: partition out of job range")
	ErrIncomplete  = errors.New("backfill: job interval not fully staged")
	ErrHeld        = errors.New("backfill: partition held by a backfill job")
)

// Error 携带冲突作业名或分区号等细节，Unwrap 返回上面的哨兵错误。
type Error struct {
	Kind error
	Job  string
	Part int
}

func (e *Error) Error() string {
	if e.Job != "" {
		return fmt.Sprintf("%v (job %q)", e.Kind, e.Job)
	}
	return fmt.Sprintf("%v (partition %d)", e.Kind, e.Part)
}

func (e *Error) Unwrap() error { return e.Kind }

// Job 是一个回填作业。staged 为暂存集（不可见，重复暂存幂等）。
type Job struct {
	Name     string
	A, B     int // 占用闭区间 [A,B]
	TTL      int
	Deadline int // now >= Deadline 即过期
	staged   map[int]struct{}
}

// Stage 暂存分区 p（幂等）。调用方保证 p 在 [A,B] 内。
func (j *Job) Stage(p int) { j.staged[p] = struct{}{} }

// FirstUnstaged 返回区间内最小的未暂存分区号；全部暂存时 ok=false。
func (j *Job) FirstUnstaged() (p int, ok bool) {
	for p := j.A; p <= j.B; p++ {
		if _, done := j.staged[p]; !done {
			return p, true
		}
	}
	return 0, false
}

// Manager 管理活跃作业。sorted 按区间起点升序，可能含已过期未落地的作业。
type Manager struct {
	cap    int
	jobs   map[string]*Job
	sorted []*Job
	cmps   int64 // Begin 相交判定时比较的活跃作业数（累计）
}

// NewManager 返回容量为 capacity 的管理器。
func NewManager(capacity int) *Manager {
	return &Manager{cap: capacity, jobs: make(map[string]*Job)}
}

// Cmps 返回 Begin 相交判定累计比较的活跃作业数。
func (m *Manager) Cmps() int64 { return m.cmps }

func live(j *Job, now int) bool { return j.Deadline > now }

// Sweep 落地所有在 now 已到期的取消（物理删除，暂存随之丢弃）。
// 只能在被接受的操作中调用。
func (m *Manager) Sweep(now int) {
	expired := false
	for _, j := range m.sorted {
		if j.Deadline <= now {
			delete(m.jobs, j.Name)
			expired = true
		}
	}
	if !expired {
		return
	}
	kept := m.sorted[:0]
	for _, j := range m.sorted {
		if live(j, now) {
			kept = append(kept, j)
		}
	}
	m.sorted = kept
}

// Get 返回名为 name 的活跃作业；不存在或已过期（视同不存在）返回 nil。
func (m *Manager) Get(name string, now int) *Job {
	j := m.jobs[name]
	if j == nil || !live(j, now) {
		return nil
	}
	return j
}

// Remove 移除一个作业（Finish/Abort 成功时调用）。
func (m *Manager) Remove(j *Job) {
	delete(m.jobs, j.Name)
	idx := sort.Search(len(m.sorted), func(i int) bool { return m.sorted[i].A >= j.A })
	for i := idx; i < len(m.sorted); i++ {
		if m.sorted[i] == j {
			m.sorted = append(m.sorted[:i], m.sorted[i+1:]...)
			return
		}
	}
}

// Holder 返回占用分区 p 的活跃作业名（区间互不相交，至多一个）。
func (m *Manager) Holder(p, now int) (string, bool) {
	idx := sort.Search(len(m.sorted), func(i int) bool { return m.sorted[i].A > p }) - 1
	if idx >= 0 {
		if j := m.sorted[idx]; j.A <= p && p <= j.B && live(j, now) {
			return j.Name, true
		}
	}
	return "", false
}

// MinStartLanded 返回已落地（含已过期未取消）作业的最小区间起点。
func (m *Manager) MinStartLanded() (int, bool) {
	if len(m.sorted) == 0 {
		return 0, false
	}
	return m.sorted[0].A, true
}

// MinStartLive 返回活跃作业的最小区间起点。
func (m *Manager) MinStartLive(now int) (int, bool) {
	for _, j := range m.sorted {
		if live(j, now) {
			return j.A, true
		}
	}
	return 0, false
}

// Begin 占用闭区间 [a,b]。调用方已校验参数与时钟。
// 拒绝次序：ErrJobExists > ErrTooManyJobs > ErrOverlap。
func (m *Manager) Begin(name string, a, b, ttl, now int) error {
	if j := m.jobs[name]; j != nil && live(j, now) {
		return &Error{Kind: ErrJobExists, Job: name}
	}
	n := 0
	for _, j := range m.sorted {
		if live(j, now) {
			n++
		}
	}
	if n >= m.cap {
		return &Error{Kind: ErrTooManyJobs, Job: name}
	}
	if conflict, ok := m.firstConflict(a, b, now); ok {
		return &Error{Kind: ErrOverlap, Job: conflict}
	}
	m.Sweep(now)
	j := &Job{Name: name, A: a, B: b, TTL: ttl, Deadline: now + ttl, staged: make(map[int]struct{})}
	m.jobs[name] = j
	idx := sort.Search(len(m.sorted), func(i int) bool { return m.sorted[i].A >= a })
	m.sorted = append(m.sorted, nil)
	copy(m.sorted[idx+1:], m.sorted[idx:])
	m.sorted[idx] = j
	return nil
}

// firstConflict 返回与 [a,b] 相交的活跃作业中区间起点最小者。
// 活跃区间互不相交，只需看按起点排序的前驱与后继，cmps 增量不超过 2。
func (m *Manager) firstConflict(a, b, now int) (string, bool) {
	idx := sort.Search(len(m.sorted), func(i int) bool { return m.sorted[i].A >= a })
	for i := idx - 1; i >= 0; i-- { // 前驱：起点 < a 的最近活跃作业
		j := m.sorted[i]
		if !live(j, now) {
			continue
		}
		m.cmps++
		if j.B >= a {
			return j.Name, true
		}
		break
	}
	for i := idx; i < len(m.sorted); i++ { // 后继：起点 >= a 的最近活跃作业
		j := m.sorted[i]
		if !live(j, now) {
			continue
		}
		m.cmps++
		if j.A <= b {
			return j.Name, true
		}
		break
	}
	return "", false
}
