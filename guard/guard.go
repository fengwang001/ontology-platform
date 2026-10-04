package guard

import (
	"errors"
	"sync"
)

var (
	ErrInvalid  = errors.New("guard: invalid argument")
	ErrConflict = errors.New("guard: fingerprint conflict")
	ErrLocked   = errors.New("guard: sn is locked")
)

const (
	minM       = 1
	maxM       = 10
	maxLk      = 1_000_000
	capK       = 7
	maxBackoff = 6 // 封顶指数：2^6
)

type Sn string

// Entry 是某个 sn 的错误尝试计数 e、历史锁定次数 k 与锁定截止。
type Entry struct {
	E         int
	K         int
	LockUntil int64
}

type Guard struct {
	mu    sync.Mutex
	m     int
	lk    int64
	entry map[Sn]*Entry
}

func New(m int, lk int64) (*Guard, error) {
	if m < minM || m > maxM || lk < 1 || lk > maxLk {
		return nil, ErrInvalid
	}
	return &Guard{m: m, lk: lk, entry: make(map[Sn]*Entry)}, nil
}

func (g *Guard) get(sn Sn) *Entry {
	e, ok := g.entry[sn]
	if !ok {
		e = &Entry{}
		g.entry[sn] = e
	}
	return e
}

// Locked 报告 sn 在 now 是否仍处锁定期；now==lockUntil 即已解锁。
func (g *Guard) Locked(sn Sn, now int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.get(sn)
	return now < e.LockUntil
}

// Note 记录一次错误尝试。达到阈值 M 的那次调用返回 locked=true，
// 同时 k 加 1、e 清零、lockUntil=now+Lk*2^(min(k,7)-1)。
func (g *Guard) Note(sn Sn, now int64) (Entry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.get(sn)
	e.E++
	if e.E < g.m {
		return *e, false
	}
	e.E = 0
	e.K++
	exp := e.K - 1
	if exp > maxBackoff {
		exp = maxBackoff
	}
	e.LockUntil = now + g.lk<<exp
	return *e, true
}

// Clear 清除错误计数与当前锁定，保留历史锁定次数 k。
func (g *Guard) Clear(sn Sn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.get(sn)
	e.E = 0
	e.LockUntil = 0
}

// Peek 返回 guard 状态快照（测试与朴素模型对照用）。
func (g *Guard) Peek(sn Sn) Entry {
	g.mu.Lock()
	defer g.mu.Unlock()
	return *g.get(sn)
}
