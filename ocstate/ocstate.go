// Package ocstate 维护各下游服务器的过载通告、欠额与在途窗口状态，
// 以及全局只进不退的逻辑时钟。限制到期是时间的纯函数，读取时惰性判定。
package ocstate

import (
	"errors"
	"fmt"
	"sync"
)

// 错误哨兵，调用方用 errors.Is 区分类别。
var (
	ErrInvalidParam   = errors.New("invalid parameter")
	ErrClockBack      = errors.New("clock moved backwards")
	ErrServerNotExist = errors.New("server does not exist")
	ErrServerExists   = errors.New("server already exists")
	ErrStaleReport    = errors.New("stale overload report")
	ErrNoInflight     = errors.New("no inflight message")
)

// 参数合法范围。
const (
	MinServerID = 1
	MaxServerID = 1_000_000
	MaxNow      = 1_000_000_000_000
	MaxValidity = 1_000_000_000
)

// Server 为单台下游服务器的状态。调用方须持有 Store 锁。
type Server struct {
	maxSeq    int64 // 已接受的最大通告序号
	p         int64 // 当前限制百分比（已到期时为 0）
	expiresAt int64 // 限制到期时刻，now>=expiresAt 即失效
	d         int64 // 欠额
	inflight  int64 // 在途数
	arrivals  int64 // 到达数（恒等于 forwarded+dropped）
	forwarded int64
	dropped   int64
}

// Settle 把到期限制物化：now>=expiresAt 时限制失效，p 与 D 归零。时间的纯函数。
func (s *Server) Settle(now int64) {
	if now >= s.expiresAt {
		s.p = 0
		s.d = 0
	}
}

// Inflight 返回当前在途数。
func (s *Server) Inflight() int64 { return s.inflight }

// D 返回当前欠额（调用前应先 Settle）。
func (s *Server) D() int64 { return s.d }

// Arrive 计入一次到达并按当前 p 累加欠额，封顶 cap。
func (s *Server) Arrive(cap int64) {
	s.arrivals++
	s.d += s.p
	if s.d > cap {
		s.d = cap
	}
}

// Drop 减载一条：欠额减 100。调用方保证 d>=100。
func (s *Server) Drop() {
	s.dropped++
	s.d -= 100
}

// Forward 转发一条：在途数加 1。
func (s *Server) Forward() {
	s.forwarded++
	s.inflight++
}

// Snapshot 为服务器状态的只读快照，供测试核对。
type Snapshot struct {
	MaxSeq    int64
	P         int64
	ExpiresAt int64
	D         int64
	Inflight  int64
	Arrivals  int64
	Forwarded int64
	Dropped   int64
	Exists    bool
}

// Store 为服务器注册表与全局时钟。所有方法可并发调用。
type Store struct {
	mu      sync.Mutex
	servers map[int]*Server
	maxNow  int64
	hasNow  bool
}

// NewStore 创建空注册表。
func NewStore() *Store {
	return &Store{servers: make(map[int]*Server)}
}

// Lock 锁定注册表，供 throttle 在一趟 Route 内原子访问多台服务器。
func (s *Store) Lock() { s.mu.Lock() }

// Unlock 解锁注册表。
func (s *Store) Unlock() { s.mu.Unlock() }

func validServerID(id int) bool { return id >= MinServerID && id <= MaxServerID }

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// CheckNow 校验时钟参数：先参数范围，再单调性。调用方须持有锁。
func (s *Store) CheckNow(now int64) error {
	if !validNow(now) {
		return fmt.Errorf("now %d: %w", now, ErrInvalidParam)
	}
	if s.hasNow && now < s.maxNow {
		return fmt.Errorf("now %d < max %d: %w", now, s.maxNow, ErrClockBack)
	}
	return nil
}

// Advance 在接受操作后推进时钟。调用方须持有锁。
func (s *Store) Advance(now int64) {
	if !s.hasNow || now > s.maxNow {
		s.maxNow = now
		s.hasNow = true
	}
}

// Exists 报告服务器是否已登记。调用方须持有锁。
func (s *Store) Exists(id int) bool {
	_, ok := s.servers[id]
	return ok
}

// Server 返回已登记服务器的状态，未登记返回 nil。调用方须持有锁。
func (s *Store) Server(id int) *Server { return s.servers[id] }

// AddServer 登记服务器，重复登记报 ErrServerExists。
func (s *Store) AddServer(id int) error {
	if !validServerID(id) {
		return fmt.Errorf("server id %d: %w", id, ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.servers[id]; ok {
		return fmt.Errorf("server %d: %w", id, ErrServerExists)
	}
	s.servers[id] = &Server{maxSeq: -1}
	return nil
}

// Report 接受过载通告。seq 须严格大于已接受的最大 seq；validity=0 表示撤销
// 限制（p 置 0、欠额清零），否则 p=percent、e=now+validity、欠额保留。
func (s *Store) Report(server int, seq, percent, validity, now int64) error {
	if !validServerID(server) || seq < 0 ||
		percent < 0 || percent > 100 ||
		validity < 0 || validity > MaxValidity {
		return fmt.Errorf("report(server=%d seq=%d p=%d v=%d): %w",
			server, seq, percent, validity, ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckNow(now); err != nil {
		return err
	}
	srv, ok := s.servers[server]
	if !ok {
		return fmt.Errorf("server %d: %w", server, ErrServerNotExist)
	}
	if seq <= srv.maxSeq {
		return fmt.Errorf("server %d seq %d <= %d: %w",
			server, seq, srv.maxSeq, ErrStaleReport)
	}
	srv.Settle(now)
	srv.maxSeq = seq
	if validity == 0 {
		srv.p = 0
		srv.d = 0
		srv.expiresAt = now
	} else {
		srv.p = percent
		srv.expiresAt = now + validity
	}
	s.Advance(now)
	return nil
}

// Done 使服务器在途数减 1，在途为 0 时报 ErrNoInflight。
func (s *Store) Done(server int, now int64) error {
	if !validServerID(server) {
		return fmt.Errorf("server id %d: %w", server, ErrInvalidParam)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckNow(now); err != nil {
		return err
	}
	srv, ok := s.servers[server]
	if !ok {
		return fmt.Errorf("server %d: %w", server, ErrServerNotExist)
	}
	if srv.inflight == 0 {
		return fmt.Errorf("server %d: %w", server, ErrNoInflight)
	}
	srv.inflight--
	s.Advance(now)
	return nil
}

// Inspect 返回服务器状态快照，供测试核对。
func (s *Store) Inspect(server int) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[server]
	if !ok {
		return Snapshot{}
	}
	return Snapshot{
		MaxSeq:    srv.maxSeq,
		P:         srv.p,
		ExpiresAt: srv.expiresAt,
		D:         srv.d,
		Inflight:  srv.inflight,
		Arrivals:  srv.arrivals,
		Forwarded: srv.forwarded,
		Dropped:   srv.dropped,
		Exists:    true,
	}
}
