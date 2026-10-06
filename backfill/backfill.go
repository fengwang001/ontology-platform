// Package backfill 管理回填作业的区间占用、暂存与原子提交。
//
// 活跃作业的区间两两不相交，按起点升序存放。作业在 now >= deadline
// 时过期（恰等即过期）：过期作业对任何判定视同不存在，但其取消只在
// 某个被接受的操作落地（惰性取消，见 Sweep）；被拒绝的操作不落地任何取消。
package backfill

import (
	"errors"
	"fmt"
	"sort"
)

// 作业参数限制。
const (
	MaxRange = 1000      // 区间长度 b-a+1 上限
	MaxTTL   = 1_000_000 // ttl 上限
)

var (
	ErrNoJob       = errors.New("backfill: job does not exist")
	ErrJobExists   = errors.New("backfill: job already exists")
	ErrTooManyJobs = errors.New("backfill: too many active jobs")
	ErrOutOfRange  = errors.New("backfill: partition out of job range")
	ErrIncomplete  = errors.New("backfill: job has unstaged partitions")
	ErrOverlap     = errors.New("backfill: interval overlaps an active job")
)

// OverlapError 携带冲突者中区间起点最小的作业名。
type OverlapError struct {
	Conflict string
}

func (e *OverlapError) Error() string {
	return fmt.Sprintf("%v (conflict: %s)", ErrOverlap, e.Conflict)
}

func (e *OverlapError) Unwrap() error { return ErrOverlap }

// IncompleteError 携带最小的未暂存分区号。
type IncompleteError struct {
	Part int
}

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("%v (first unstaged: %d)", ErrIncomplete, e.Part)
}

func (e *IncompleteError) Unwrap() error { return ErrIncomplete }

// Job 是一个回填作业，占用闭区间 [A, B]。
type Job struct {
	Name     string
	A, B     int
	TTL      int
	Deadline int
	staged   map[int]struct{}
}

// Staged 报告 p 是否已暂存。
func (j *Job) Staged(p int) bool {
	_, ok := j.staged[p]
	return ok
}

// Manager 管理活跃作业。自身不加锁，同步由调用方负责。
type Manager struct {
	maxJobs int
	byName  map[string]*Job
	sorted  []*Job // 按 A 升序；区间两两不相交
	cmps    int    // 最近一次 Begin 相交判定比较的活跃作业数
}

// NewManager 返回活跃作业数上限为 maxJobs 的管理器。
func NewManager(maxJobs int) *Manager {
	return &Manager{maxJobs: maxJobs, byName: make(map[string]*Job)}
}

// Cmps 返回最近一次 Begin 相交判定比较的活跃作业数（不超过 2）。
func (m *Manager) Cmps() int { return m.cmps }

func expired(j *Job, now int) bool { return now >= j.Deadline }

// Sweep 落地所有在 now 已过期的作业的取消：移除作业、释放区间、丢弃暂存。
// 只在某个操作被接受时调用。
func (m *Manager) Sweep(now int) {
	kept := m.sorted[:0]
	for _, j := range m.sorted {
		if expired(j, now) {
			delete(m.byName, j.Name)
		} else {
			kept = append(kept, j)
		}
	}
	m.sorted = kept
}

// Lookup 返回在 now 视同存在的作业；不存在或已过期返回 nil。
func (m *Manager) Lookup(name string, now int) *Job {
	j := m.byName[name]
	if j == nil || expired(j, now) {
		return nil
	}
	return j
}

// active 返回在 now 视同存在的作业（按起点升序）。
func (m *Manager) active(now int) []*Job {
	act := make([]*Job, 0, len(m.sorted))
	for _, j := range m.sorted {
		if !expired(j, now) {
			act = append(act, j)
		}
	}
	return act
}

// Holder 返回在 now 占用分区 p 的作业名。
func (m *Manager) Holder(p, now int) (string, bool) {
	for _, j := range m.sorted {
		if j.A > p {
			break
		}
		if expired(j, now) {
			continue
		}
		if p <= j.B {
			return j.Name, true
		}
	}
	return "", false
}

// MinStartLanded 返回已落地（含已过期但取消尚未落地）作业的最小区间起点。
func (m *Manager) MinStartLanded() (int, bool) {
	if len(m.sorted) == 0 {
		return 0, false
	}
	return m.sorted[0].A, true
}

// MinStartAt 返回在 now 视同存在的作业的最小区间起点。
func (m *Manager) MinStartAt(now int) (int, bool) {
	for _, j := range m.sorted {
		if !expired(j, now) {
			return j.A, true
		}
	}
	return 0, false
}

// Begin 占用闭区间 [a,b]，deadline = now+ttl。参数合法性由调用方校验。
// 拒绝次序：ErrJobExists > ErrTooManyJobs > ErrOverlap。
func (m *Manager) Begin(name string, a, b, ttl, now int) error {
	m.cmps = 0
	if m.Lookup(name, now) != nil {
		return ErrJobExists
	}
	act := m.active(now)
	if len(act) >= m.maxJobs {
		return ErrTooManyJobs
	}
	// 活跃区间互不相交：只需看按起点排序的前驱与后继。
	// 起点最小的冲突者必是前驱（若它相交），否则是后继。
	idx := sort.Search(len(act), func(i int) bool { return act[i].A >= a })
	var conflict *Job
	if idx > 0 {
		m.cmps++
		if act[idx-1].B >= a {
			conflict = act[idx-1]
		}
	}
	if conflict == nil && idx < len(act) {
		m.cmps++
		if act[idx].A <= b {
			conflict = act[idx]
		}
	}
	if conflict != nil {
		return &OverlapError{Conflict: conflict.Name}
	}
	m.Sweep(now)
	j := &Job{Name: name, A: a, B: b, TTL: ttl, Deadline: now + ttl, staged: make(map[int]struct{})}
	m.byName[name] = j
	pos := sort.Search(len(m.sorted), func(i int) bool { return m.sorted[i].A >= a })
	m.sorted = append(m.sorted, nil)
	copy(m.sorted[pos+1:], m.sorted[pos:])
	m.sorted[pos] = j
	return nil
}

// Stage 暂存一个分区的新数据（不可见，重复暂存幂等，不续期）。
func (m *Manager) Stage(name string, p, now int) error {
	j := m.Lookup(name, now)
	if j == nil {
		return ErrNoJob
	}
	if p < j.A || p > j.B {
		return ErrOutOfRange
	}
	m.Sweep(now)
	j.staged[p] = struct{}{}
	return nil
}

// Heartbeat 把 deadline 改为 now+ttl。
func (m *Manager) Heartbeat(name string, now int) error {
	j := m.Lookup(name, now)
	if j == nil {
		return ErrNoJob
	}
	m.Sweep(now)
	j.Deadline = now + j.TTL
	return nil
}

// Finish 校验完整性（区间内每个分区都已暂存），落地过期取消并结束
// 作业、释放区间，返回作业供调用方原子提交版本。
func (m *Manager) Finish(name string, now int) (*Job, error) {
	j := m.Lookup(name, now)
	if j == nil {
		return nil, ErrNoJob
	}
	for p := j.A; p <= j.B; p++ {
		if !j.Staged(p) {
			return nil, &IncompleteError{Part: p}
		}
	}
	m.Sweep(now)
	m.remove(j)
	return j, nil
}

// Abort 放弃作业并丢弃暂存。
func (m *Manager) Abort(name string, now int) error {
	j := m.Lookup(name, now)
	if j == nil {
		return ErrNoJob
	}
	m.Sweep(now)
	m.remove(j)
	return nil
}

func (m *Manager) remove(j *Job) {
	delete(m.byName, j.Name)
	for i, x := range m.sorted {
		if x == j {
			m.sorted = append(m.sorted[:i], m.sorted[i+1:]...)
			return
		}
	}
}
