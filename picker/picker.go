// Package picker 实现带峰值时延估计、在途惩罚与饱和背压的二选一（P2C）
// 后端端点选择器。
//
// 所有方法可并发调用，单互斥锁使结果等价于某个串行顺序。
// 拒绝（错误）只报第一个，顺序为：参数非法 > 时间非法 > 时钟回退 > 业务错误。
// 被拒绝的操作不改变任何状态（含票据号、最大 now 与估计）。
package picker

import (
	"errors"
	"fmt"
	"sync"

	"ontology/pool"
)

var (
	ErrEmptyID         = errors.New("picker: endpoint id is empty")
	ErrInvalidRTT      = errors.New("picker: rtt out of range [0,1e9]")
	ErrInvalidTime     = errors.New("picker: now out of range [0,1e15]")
	ErrClockRegression = errors.New("picker: now regresses below max seen now")
	ErrTicketUnknown   = errors.New("picker: ticket unknown or already released")
	ErrSaturated       = errors.New("picker: all endpoints at inflight limit")
	ErrNoEndpoints     = errors.New("picker: no endpoints available")

	ErrExists   = pool.ErrExists
	ErrFull     = pool.ErrFull
	ErrNotFound = pool.ErrNotFound
	ErrDraining = pool.ErrDraining
)

const (
	maxParam = int64(1_000_000_000) // τ、P0、Pf、rtt 的上界
	maxNow   = int64(1_000_000_000_000_000)
	minLimit = 1
	maxM     = 1_000_000
	maxNmax  = 100_000
)

// Selector 是 P2C 端点选择器。
type Selector struct {
	mu         sync.Mutex
	tau        int64
	p0         int64
	pf         int64
	pl         *pool.Pool
	nextTicket uint64
	tickets    map[uint64]*pool.Endpoint
	maxNow     int64
}

// New 构造选择器。tau、p0、pf ∈ [1,1e9]（毫秒），m ∈ [1,1e6]，nmax ∈ [1,1e5]。
func New(tau, p0, pf int64, m, nmax int) (*Selector, error) {
	if tau < 1 || tau > maxParam || p0 < 1 || p0 > maxParam || pf < 1 || pf > maxParam {
		return nil, fmt.Errorf("picker: tau/p0/pf must be in [1,1e9]: %d %d %d", tau, p0, pf)
	}
	if m < minLimit || m > maxM {
		return nil, fmt.Errorf("picker: m must be in [1,1e6]: %d", m)
	}
	if nmax < minLimit || nmax > maxNmax {
		return nil, fmt.Errorf("picker: nmax must be in [1,1e5]: %d", nmax)
	}
	return &Selector{
		tau:     tau,
		p0:      p0,
		pf:      pf,
		pl:      pool.New(m, nmax),
		tickets: make(map[uint64]*pool.Endpoint),
	}, nil
}

// checkTime 校验时间合法性与时钟回退，不加锁（调用方持锁）。
func (s *Selector) checkTime(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	return nil
}

// Pick 用两个随机数做二选一选路，成功返回票据号（从 1 起）与端点 id。
func (s *Selector) Pick(now int64, r1, r2 uint64) (uint64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkTime(now); err != nil {
		return 0, "", err
	}
	elig := s.pl.Eligible()
	n := len(elig)
	if n == 0 {
		if s.pl.HasActive() {
			return 0, "", ErrSaturated
		}
		return 0, "", ErrNoEndpoints
	}
	win := elig[0]
	if n > 1 {
		i := int(r1 % uint64(n))
		j := (i + 1 + int(r2%uint64(n-1))) % n
		a, b := elig[i], elig[j]
		ca := a.Est.Val(now, s.tau, s.p0) * int64(a.Inflight+1)
		cb := b.Est.Val(now, s.tau, s.p0) * int64(b.Inflight+1)
		switch {
		case ca < cb:
			win = a
		case cb < ca:
			win = b
		case a.ID < b.ID:
			win = a
		default:
			win = b
		}
	}
	win.Inflight++
	s.nextTicket++
	s.tickets[s.nextTicket] = win
	s.maxNow = now
	return s.nextTicket, win.ID, nil
}

// Release 归还票据：在途减 1，以 ok?rtt:Pf 为样本更新估计；
// 排空中端点在途归零时移除。
func (s *Selector) Release(ticket uint64, rtt int64, ok bool, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rtt < 0 || rtt > maxParam {
		return ErrInvalidRTT
	}
	if err := s.checkTime(now); err != nil {
		return err
	}
	ep, ok2 := s.tickets[ticket]
	if !ok2 {
		return ErrTicketUnknown
	}
	delete(s.tickets, ticket)
	sample := rtt
	if !ok {
		sample = s.pf
	}
	ep.Est.Update(sample, now, s.tau)
	s.pl.ReleaseOne(ep)
	s.maxNow = now
	return nil
}

// AddEndpoint 新增端点。已存在（含排空中）报 ErrExists，达 Nmax 报 ErrFull。
func (s *Selector) AddEndpoint(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return ErrEmptyID
	}
	return s.pl.Add(id)
}

// RemoveEndpoint 摘除端点：在途为 0 立即移除，否则转排空。
func (s *Selector) RemoveEndpoint(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return ErrEmptyID
	}
	return s.pl.Remove(id)
}
