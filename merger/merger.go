// Package merger 实现带最小唤醒间隔与批量上限的周期定时器唤醒合并器。
//
// 每个定时器的窗口为 [n, n+s]，合并器把各窗口合并成尽量少的实际唤醒：
// 下一次唤醒时刻 w = max(e, last+g)，其中 e 为全部定时器 n+s 的最小值，
// last 为上一次实际唤醒时刻（尚无唤醒时只取 e）。单次唤醒按 (n+s, 编号)
// 升序从候选（n <= w）中取前 B 个触发，被迫错过窗口的定时器按名义栅格
// 追赶：k = floor((w-n)/P)，n 增加 (k+1)*P。
package merger

import (
	"errors"
	"sort"
	"sync"
)

const (
	maxG            int64 = 1_000_000_000
	maxB            int64 = 64
	maxP            int64 = 1_000_000_000
	maxN            int64 = 1_000_000_000_000_000
	maxT            int64 = 1_000_000_000_000_000
	maxTimers             = 64
	maxIDBytes            = 32
	maxAdvanceWakes       = 100_000
)

// 可区分的拒绝原因，同一操作只按此顺序报告第一个。
var (
	ErrInvalidParam = errors.New("merger: invalid parameter")
	ErrDuplicate    = errors.New("merger: duplicate timer id")
	ErrNotFound     = errors.New("merger: timer id not found")
	ErrPastNominal  = errors.New("merger: nominal time before current clock")
	ErrFull         = errors.New("merger: timer capacity full")
	ErrNoTimers     = errors.New("merger: no timers")
	ErrClockBack    = errors.New("merger: clock regression")
)

// Fired 描述一次唤醒中被触发的单个定时器。
type Fired struct {
	ID   string // 定时器编号
	Late int64  // 延迟 late = max(0, w-(n+s))
	K    int64  // 跳过数 k = floor((w-n)/P)
}

// WakeResult 描述一次实际唤醒。
type WakeResult struct {
	W     int64   // 唤醒时刻
	Fired []Fired // 按触发次序的清单
	Left  int     // 达到批量上限后被留下的候选个数
}

// Stats 是累计统计。
type Stats struct {
	Wakes   int64 // 累计唤醒数
	Fired   int64 // 累计触发数
	Late    int64 // 累计延迟次数（late > 0 的触发）
	Skipped int64 // 累计跳过总数（各次 k 之和）
}

// timer 是定时器的内部表示。
type timer struct {
	id   string
	p, s int64
	n    int64
}

func (t *timer) end() int64 { return t.n + t.s }

// lessEnd 以 (n+s, 编号字节序) 升序，用于维护 e 的堆与候选排序。
func lessEnd(a, b *timer) bool {
	ea, eb := a.end(), b.end()
	if ea != eb {
		return ea < eb
	}
	return a.id < b.id
}

// lessStart 以 (n, 编号字节序) 升序，用于按候选条件 n <= w 取元素。
func lessStart(a, b *timer) bool {
	if a.n != b.n {
		return a.n < b.n
	}
	return a.id < b.id
}

// Merger 是唤醒合并器。所有方法可并发调用，效果等价于某个串行顺序。
type Merger struct {
	mu       sync.Mutex
	g        int64
	batch    int64
	timers   map[string]*timer
	byEnd    *timerHeap // 键 (n+s, id)：Next 取 e 为 O(1)
	byStart  *timerHeap // 键 (n, id)：Wake 取候选只考察候选个数 +1 次
	last     int64
	hasLast  bool
	stats    Stats
	wakeExam int64 // 最近一次 Wake 的候选考察次数（测试观测用）
}

// New 构造合并器：g 为最小唤醒间隔（0..1e9），batch 为单次唤醒最多触发个数（1..64）。
func New(g, batch int64) (*Merger, error) {
	if g < 0 || g > maxG || batch < 1 || batch > maxB {
		return nil, ErrInvalidParam
	}
	return &Merger{
		g:       g,
		batch:   batch,
		timers:  make(map[string]*timer),
		byEnd:   newTimerHeap(lessEnd),
		byStart: newTimerHeap(lessStart),
	}, nil
}

func validID(id string) bool {
	return len(id) >= 1 && len(id) <= maxIDBytes
}

func validParams(id string, p, s, n int64) bool {
	return validID(id) &&
		p >= 1 && p <= maxP &&
		s >= 0 && s < p &&
		n >= 0 && n <= maxN
}

// Add 加入定时器。拒绝顺序：参数非法、编号重复、名义时刻早于当前时钟、容量已满。
func (m *Merger) Add(id string, p, s, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validParams(id, p, s, n) {
		return ErrInvalidParam
	}
	if _, ok := m.timers[id]; ok {
		return ErrDuplicate
	}
	if n < m.last {
		return ErrPastNominal
	}
	if len(m.timers) >= maxTimers {
		return ErrFull
	}
	t := &timer{id: id, p: p, s: s, n: n}
	m.timers[id] = t
	m.byEnd.push(t)
	m.byStart.push(t)
	return nil
}

// Remove 移除定时器，使其立即退出 e 与候选的计算。
func (m *Merger) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validID(id) {
		return ErrInvalidParam
	}
	t, ok := m.timers[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.timers, id)
	m.byEnd.remove(t)
	m.byStart.remove(t)
	return nil
}

// Next 返回下一次唤醒时刻，不改变任何状态。
func (m *Merger) Next() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nextLocked()
}

func (m *Merger) nextLocked() (int64, error) {
	top := m.byEnd.top()
	if top == nil {
		return 0, ErrNoTimers
	}
	e := top.end()
	if !m.hasLast {
		return e, nil
	}
	if w := m.last + m.g; w > e {
		return w, nil
	}
	return e, nil
}

// Stats 返回累计统计快照。
func (m *Merger) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

// sortCandidates 用于 Wake 内对候选按 (n+s, 编号) 升序排序。
func sortCandidates(cand []*timer) {
	sort.Slice(cand, func(i, j int) bool { return lessEnd(cand[i], cand[j]) })
}
