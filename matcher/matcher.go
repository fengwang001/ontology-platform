// Package matcher 实现带预付摊销与分级覆盖的云承诺折扣用量匹配器。
package matcher

import (
	"errors"
	"sort"
	"sync"
)

// 操作被拒绝时返回的错误，按校验顺序只报第一个。
var (
	ErrInvalidArgument = errors.New("matcher: invalid argument")
	ErrDuplicateID     = errors.New("matcher: duplicate commit id")
	ErrStartPassed     = errors.New("matcher: commit start hour already passed")
	ErrTimeRegression  = errors.New("matcher: apply hour not after lastHour")
)

// Line 是一条用量行：族 f（1..99）与按需价 p（分）。
type Line struct {
	Fam int64
	P   int64
}

// CommitReport 是单个承诺在某一小时的用量报告。
type CommitReport struct {
	ID      int64
	Used    int64 // 本小时被用量消耗的额度
	Unused  int64 // 本小时未被消耗的额度，Used+Unused == h
	Covered int64 // 本承诺覆盖的按需价总额
}

// HourReport 是一个小时的处理报告。
type HourReport struct {
	Hour    int64
	Bill    int64 // 各有效承诺 (h+摊销) 之和 + 全部用量行剩余按需价之和
	Commits []CommitReport
}

type commit struct {
	id    int64
	fam   int64
	start int64
	n     int64
	h     int64
	up    int64
	d     int64
}

// Matcher 按小时把用量行匹配到承诺额度上。零值不可用，请用 New 构造。
type Matcher struct {
	mu       sync.Mutex
	lastHour int64
	commits  map[int64]*commit
}

// New 构造一个空匹配器，lastHour 初值为 -1。
func New() *Matcher {
	return &Matcher{lastHour: -1, commits: make(map[int64]*commit)}
}

// AddCommit 登记一个承诺。
func (m *Matcher) AddCommit(id, fam, start, n, h, up, d int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addCommitLocked(id, fam, start, n, h, up, d)
}

// Apply 处理到小时 t，返回小时 lastHour+1..t 各一份报告。
func (m *Matcher) Apply(t int64, lines []Line) ([]HourReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.applyLocked(t, lines)
}

func (m *Matcher) addCommitLocked(id, fam, start, n, h, up, d int64) error {
	if id < 0 || id > 1e6 ||
		fam < 0 || fam > 99 ||
		start < 0 ||
		n < 1 || n > 1e5 ||
		h < 1 || h > 1e9 ||
		up < 0 || up > 1e12 ||
		d < 1 || d > 9999 {
		return ErrInvalidArgument
	}
	if _, ok := m.commits[id]; ok {
		return ErrDuplicateID
	}
	if start <= m.lastHour {
		return ErrStartPassed
	}
	m.commits[id] = &commit{id: id, fam: fam, start: start, n: n, h: h, up: up, d: d}
	return nil
}

func (m *Matcher) applyLocked(t int64, lines []Line) ([]HourReport, error) {
	if t < 0 || t > m.lastHour+10000 || len(lines) > 1000 {
		return nil, ErrInvalidArgument
	}
	for _, ln := range lines {
		if ln.Fam < 1 || ln.Fam > 99 || ln.P < 1 || ln.P > 1e9 {
			return nil, ErrInvalidArgument
		}
	}
	if t <= m.lastHour {
		return nil, ErrTimeRegression
	}

	reports := make([]HourReport, 0, t-m.lastHour)
	for hour := m.lastHour + 1; hour <= t; hour++ {
		var hourLines []Line
		if hour == t {
			hourLines = lines
		}
		reports = append(reports, m.processHour(hour, hourLines))
	}
	m.lastHour = t
	return reports, nil
}

// processHour 处理单个小时：lines 为该小时的用量行（可为空）。
func (m *Matcher) processHour(hour int64, lines []Line) HourReport {
	active := make([]*commit, 0, len(m.commits))
	for _, c := range m.commits {
		if c.start <= hour && hour < c.start+c.n {
			active = append(active, c)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].d != active[j].d {
			return active[i].d > active[j].d
		}
		return active[i].id < active[j].id
	})

	remaining := make([]int64, len(lines))
	for i, ln := range lines {
		remaining[i] = ln.P
	}

	rep := HourReport{Hour: hour}
	for _, c := range active {
		r := c.h
		var covered int64
		mult := 10000 - c.d
		for i, ln := range lines {
			p := remaining[i]
			if p == 0 || (c.fam != 0 && c.fam != ln.Fam) {
				continue
			}
			e := (p*mult + 9999) / 10000 // ceil(p*mult/10000)
			if r >= e {
				r -= e
				covered += p
				remaining[i] = 0
				continue
			}
			if r == 0 {
				break
			}
			q := r * 10000 / mult // floor(r*10000/mult)，可证 1<=q<p
			covered += q
			remaining[i] = p - q
			r = 0
			break
		}
		rep.Commits = append(rep.Commits, CommitReport{
			ID:      c.id,
			Used:    c.h - r,
			Unused:  r,
			Covered: covered,
		})
		rep.Bill += c.h + amortization(c, hour)
	}
	for _, p := range remaining {
		rep.Bill += p
	}
	return rep
}

// amortization 返回承诺 c 在小时 hour 的预付摊销额：
// 前 n-1 个有效小时各摊 floor(up/n)，最后一个有效小时摊尾项。
func amortization(c *commit, hour int64) int64 {
	base := c.up / c.n
	if hour-c.start < c.n-1 {
		return base
	}
	return c.up - (c.n-1)*base
}
