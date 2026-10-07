// Package store 负责对象实例的持久化与版本仲裁：乐观并发校验、
// 系统时间单调分配、逻辑删除、拒绝排序与重放。
package store

import (
	"fmt"
	"sync"

	"ontology/index"
)

// OpKind 区分普通写入与逻辑删除。
type OpKind int

const (
	// KindPut 普通写入，携带业务内容。
	KindPut OpKind = iota
	// KindDelete 逻辑删除，不携带业务内容，表示事实自 BizStart 起不再成立。
	KindDelete
)

// MaxBizStart 是业务时间起点的最大合法值；math.MaxInt64 保留为开放终点哨兵。
const MaxBizStart = int64(1<<63 - 2)

// Version 是主键版本链上的一条版本。
type Version struct {
	Seq      int64  // 主键内从 1 开始严格递增的版本号
	Sys      int64  // 系统时间，存储子系统单调分配，主键内严格递增
	BizStart int64  // 业务时间起点
	Kind     OpKind // 普通写入或逻辑删除
	Payload  string // 业务内容；逻辑删除时为空
}

// Status 是双时态查询的结果标记。
type Status int

const (
	// StatusNeverWritten 该主键从未写入。
	StatusNeverWritten Status = iota
	// StatusNoVersionAtTime 主键已写入，但该（系统时间, 业务时间）坐标下无覆盖版本。
	StatusNoVersionAtTime
	// StatusDeleted 坐标命中一个逻辑删除版本（事实此时不成立）。
	StatusDeleted
	// StatusFound 坐标命中一个普通版本。
	StatusFound
)

// ErrKind 是可相互区分的写入拒绝原因。
type ErrKind int

const (
	// ErrInvalidArgument 参数非法：主键为空、业务时间起点非法、并发凭证格式非法。
	ErrInvalidArgument ErrKind = iota
	// ErrConcurrencyConflict 乐观并发凭证与当前最新版本不一致。
	ErrConcurrencyConflict
	// ErrBizBoundary 业务时间起点早于该主键已确认提交的最早可追溯边界。
	ErrBizBoundary
)

// RejectError 是写入被拒绝的错误，三类原因可相互区分。
type RejectError struct {
	Kind    ErrKind
	Message string
}

func (e *RejectError) Error() string { return e.Message }

// Clock 提供系统时间。生产环境使用真实时钟；测试与重放使用确定性时钟。
type Clock interface {
	Now() int64
}

// chain 是单个主键的版本链与仲裁状态。
type chain struct {
	mu       sync.Mutex
	versions []Version
	minBiz   int64 // 已提交版本的最小业务起点，即最早可追溯边界
	lastSys  int64
	idx      *index.Chain
}

func chainKey(objectType, pk string) string { return objectType + "\x00" + pk }

// Store 管理全部对象实例的版本链，是写入仲裁的唯一入口。
type Store struct {
	clock  Clock
	wal    WAL
	mu     sync.Mutex
	chains map[string]*chain
}

// Option 配置 Store。
type Option func(*Store)

// WithWAL 配置先写日志；每条已提交操作在内存提交前追加。
func WithWAL(w WAL) Option { return func(s *Store) { s.wal = w } }

// New 创建 Store。
func New(clock Clock, opts ...Option) *Store {
	s := &Store{clock: clock, chains: map[string]*chain{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Store) getChain(key string) *chain {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chains[key]
}

func (s *Store) getOrCreateChain(key string) *chain {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chains[key]
	if !ok {
		c = &chain{idx: index.NewChain()}
		s.chains[key] = c
	}
	return c
}

func (s *Store) write(objectType, pk string, token, bizStart int64, kind OpKind, payload string) (Version, error) {
	// 拒绝次序一：参数非法。
	if objectType == "" || pk == "" {
		return Version{}, &RejectError{ErrInvalidArgument, "对象类型与主键不能为空"}
	}
	if bizStart < 0 || bizStart > MaxBizStart {
		return Version{}, &RejectError{ErrInvalidArgument, fmt.Sprintf("业务时间起点 %d 非法", bizStart)}
	}
	if token < 0 {
		return Version{}, &RejectError{ErrInvalidArgument, fmt.Sprintf("并发凭证 %d 非法", token)}
	}

	c := s.getOrCreateChain(chainKey(objectType, pk))
	c.mu.Lock()
	defer c.mu.Unlock()

	// 拒绝次序二：乐观并发凭证不匹配。
	latest := int64(len(c.versions))
	if token != latest {
		return Version{}, &RejectError{ErrConcurrencyConflict,
			fmt.Sprintf("凭证 v%d 与当前最新版本 v%d 不一致", token, latest)}
	}
	// 拒绝次序三：业务时间起点早于最早可追溯边界。
	if latest > 0 && bizStart < c.minBiz {
		return Version{}, &RejectError{ErrBizBoundary,
			fmt.Sprintf("业务时间起点 %d 早于最早可追溯边界 %d", bizStart, c.minBiz)}
	}

	// 系统时间单调分配：真实时刻，主键内严格递增、不得回退。
	sys := s.clock.Now()
	if sys <= c.lastSys {
		sys = c.lastSys + 1
	}
	v := Version{Seq: latest + 1, Sys: sys, BizStart: bizStart, Kind: kind, Payload: payload}

	// 先写日志后提交；任一步失败都不占用版本号、不移动最新指针。
	if s.wal != nil {
		rec := Record{ObjectType: objectType, PK: pk, Kind: kind, Token: token,
			BizStart: bizStart, Seq: v.Seq, Sys: v.Sys, Payload: payload}
		if err := s.wal.Append(rec); err != nil {
			return Version{}, fmt.Errorf("wal 追加失败: %w", err)
		}
	}
	c.versions = append(c.versions, v)
	c.idx.Add(bizStart, sys, v.Seq)
	c.lastSys = sys
	if latest == 0 || bizStart < c.minBiz {
		c.minBiz = bizStart
	}
	return v, nil
}

// Write 普通写入。token 为调用方声明的前序版本号（0 表示期望创建）。
func (s *Store) Write(objectType, pk string, token, bizStart int64, payload string) (Version, error) {
	return s.write(objectType, pk, token, bizStart, KindPut, payload)
}

// Delete 逻辑删除：占用一个版本号，声明事实自 bizStart 起不再成立。
func (s *Store) Delete(objectType, pk string, token, bizStart int64) (Version, error) {
	return s.write(objectType, pk, token, bizStart, KindDelete, "")
}

// Query 按（系统时间, 业务时间）双坐标查询实例状态。
func (s *Store) Query(objectType, pk string, sysQ, bizQ int64) (Version, Status) {
	c := s.getChain(chainKey(objectType, pk))
	if c == nil {
		return Version{}, StatusNeverWritten
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.versions) == 0 {
		return Version{}, StatusNeverWritten
	}
	seq, ok := c.idx.Query(sysQ, bizQ)
	if !ok {
		return Version{}, StatusNoVersionAtTime
	}
	v := c.versions[seq-1]
	if v.Kind == KindDelete {
		return v, StatusDeleted
	}
	return v, StatusFound
}

// LatestSeq 返回主键当前最新版本号；从未写入时返回 0。
func (s *Store) LatestSeq(objectType, pk string) int64 {
	c := s.getChain(chainKey(objectType, pk))
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.versions))
}

// Versions 返回主键完整版本链的副本，用于校验与重放比对。
func (s *Store) Versions(objectType, pk string) []Version {
	c := s.getChain(chainKey(objectType, pk))
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Version(nil), c.versions...)
}

// QueryVisited 返回该主键最近一次 Query 在索引中访问的节点数，
// 主键不存在时返回 0。用于查询复杂度证明。
func (s *Store) QueryVisited(objectType, pk string) int {
	c := s.getChain(chainKey(objectType, pk))
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idx.LastQueryVisited()
}
