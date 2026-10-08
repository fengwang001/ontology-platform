// Package instance 负责对象实例的持久化与版本仲裁。
//
// Store 是版本链的唯一权威：单调分配系统时间版本号、校验乐观并发凭证、
// 维护每个 (对象类型, 主键) 的最新版本指针，并把成功提交投影到双时态索引。
package instance

import (
	"sync"

	"ontology/internal/bitemporal"
)

// 拒绝原因。三类错误互不相同，可由调用方用 errors.Is 精确区分；
// 判定次序固定为 参数非法 → 凭证冲突 → 早于最早可追溯边界。
var (
	ErrInvalidArgument   = StoreError{kind: "invalid_argument"}
	ErrConflict          = StoreError{kind: "optimistic_conflict"}
	ErrBeforeEarliestBiz = StoreError{kind: "before_earliest_biz"}
)

// StoreError 携带原因与可读信息，零值不可用；请通过上面的哨兵错误比较。
type StoreError struct {
	kind string
	msg  string
}

func (e StoreError) Error() string { return e.kind + ": " + e.msg }
func (e StoreError) with(msg string) StoreError {
	e.msg = msg
	return e
}

// Is 让带不同 msg 的同类错误仍能匹配哨兵。
func (e StoreError) Is(target error) bool {
	t, ok := target.(StoreError)
	return ok && t.kind == e.kind
}

// Version 是一条不可变的实例版本。
type Version struct {
	SysVersion int64  // 系统时间版本号（全局单调递增）
	BizStart   int64  // 业务时间起点
	Base       int64  // 乐观并发凭证：写入所基于的前序版本号（0 表示新建）
	Deleted    bool   // 逻辑删除标记
	Payload    string // 业务内容（删除时为空）
}

// Store 是实例版本存储。零值无效，请用 New 构造。
type Store struct {
	mu     sync.RWMutex
	clock  int64
	chains map[key]*chain
}

type key struct {
	objType string
	pk      string
}

type chain struct {
	commits []Version
	index   bitemporal.Index
}

// New 创建空存储。
func New() *Store {
	return &Store{chains: make(map[key]*chain)}
}

// WriteRequest 是一次写入（含逻辑删除）请求。
type WriteRequest struct {
	ObjectType string
	PrimaryKey string
	BizStart   int64
	Base       int64 // 乐观并发凭证：前序系统版本号，0 表示要求该主键尚不存在
	Deleted    bool
	Payload    string
}

// Write 在全局串行点内完成凭证校验、版本号分配与索引更新，返回新版本。
//
// 语义要点：
//   - 被拒绝的写入在分配系统版本号之前返回，因此不占用序号、不动最新指针、
//     不进入双时态索引；
//   - 系统版本号在全部校验通过后才单调 +1，天然成为全局串行顺序的编号；
//   - 所有拒绝判定集中在此处，按题目规定次序只报第一个命中的原因。
func (s *Store) Write(req WriteRequest) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1) 参数非法（最先判定）。
	if req.ObjectType == "" {
		return Version{}, ErrInvalidArgument.with("object type is empty")
	}
	if req.PrimaryKey == "" {
		return Version{}, ErrInvalidArgument.with("primary key is empty")
	}
	if req.BizStart < 0 {
		return Version{}, ErrInvalidArgument.with("business start must be >= 0")
	}
	if req.Base < 0 {
		return Version{}, ErrInvalidArgument.with("base version must be >= 0")
	}
	if !req.Deleted && req.Payload == "" {
		return Version{}, ErrInvalidArgument.with("payload is empty")
	}

	k := key{objType: req.ObjectType, pk: req.PrimaryKey}
	c := s.chains[k]
	var latest int64
	var earliestBiz int64
	if c != nil {
		latest = c.commits[len(c.commits)-1].SysVersion
		earliestBiz = c.commits[0].BizStart
	}

	// 2) 乐观并发凭证不匹配。
	if req.Base != latest {
		return Version{}, ErrConflict.with(
			"base version mismatch")
	}

	// 3) 业务时间起点早于最早可追溯边界。
	if c != nil && req.BizStart < earliestBiz {
		return Version{}, ErrBeforeEarliestBiz.with(
			"business start before earliest traceable boundary")
	}

	// 全部通过：提交点。版本号与指针、索引在同一临界区原子推进。
	s.clock++
	v := Version{
		SysVersion: s.clock,
		BizStart:   req.BizStart,
		Base:       req.Base,
		Deleted:    req.Deleted,
		Payload:    req.Payload,
	}
	if c == nil {
		c = &chain{}
		s.chains[k] = c
	}
	c.commits = append(c.commits, v)
	c.index = c.index.Apply(bitemporal.Commit{
		SysVersion: v.SysVersion,
		BizStart:   v.BizStart,
	})
	return v, nil
}

// StateKind 区分双时态查询的三种结果。
type StateKind int

const (
	StateUnknown StateKind = iota // 该主键从未写入
	StateAbsent                   // 写入过，但该业务时点处于删除区间
	StatePresent                  // 存在生效事实
)

// State 是双时态查询定位到的状态。
type State struct {
	Kind    StateKind
	Version Version // Kind != Unknown 时填充为定位到的版本（删除或存活）
}

// GetAt 按双坐标查询某主键在指定时点的状态。
func (s *Store) GetAt(objType, pk string, asOfSys, bizAt int64) (State, error) {
	if objType == "" || pk == "" || asOfSys < 0 || bizAt < 0 {
		return State{}, ErrInvalidArgument.with("invalid query argument")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	c := s.chains[key{objType: objType, pk: pk}]
	if c == nil || asOfSys < c.commits[0].SysVersion {
		return State{Kind: StateUnknown}, nil
	}
	sysVer, ok := c.index.AsOf(asOfSys, bizAt)
	if !ok {
		// 有提交记录，但该业务时间点落在任何已声明起点之前（例如
		// 系统时间晚于全部记录、而业务时间早于最早业务起点）。
		return State{Kind: StateUnknown}, nil
	}
	return s.stateAtVersion(c, sysVer), nil
}

// Segment 是系统时间 asOfSys 时可见的一个业务时间段。
type Segment struct {
	Start int64
	End   int64
	State State
}

// HistoryAsOf 重建系统时间 asOfSys 时该主键可见的完整业务时间轴；
// ok=false 表示该主键在该系统时间尚不存在。
func (s *Store) HistoryAsOf(objType, pk string, asOfSys int64) ([]Segment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c := s.chains[key{objType: objType, pk: pk}]
	if c == nil || asOfSys < c.commits[0].SysVersion {
		return nil, false
	}
	raw := c.index.HistoryAsOf(asOfSys)
	segs := make([]Segment, 0, len(raw))
	for _, seg := range raw {
		segs = append(segs, Segment{
			Start: seg.Start,
			End:   seg.End,
			State: s.stateAtVersion(c, seg.SysVersion),
		})
	}
	return segs, true
}

// stateAtVersion 用系统版本号在提交序列上二分定位版本本身。
// 提交序列的 SysVersion 全局单调，故链内也严格递增。
func (s *Store) stateAtVersion(c *chain, sysVer int64) State {
	lo, hi := 0, len(c.commits)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if c.commits[mid].SysVersion < sysVer {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	v := c.commits[lo]
	kind := StatePresent
	if v.Deleted {
		kind = StateAbsent
	}
	return State{Kind: kind, Version: v}
}

// Latest 返回某主键当前最新版本指针；不存在时 ok=false。供测试/接口层使用。
func (s *Store) Latest(objType, pk string) (Version, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.chains[key{objType: objType, pk: pk}]
	if c == nil {
		return Version{}, false
	}
	return c.commits[len(c.commits)-1], true
}

// Clock 返回已分配的最大系统版本号（被拒绝的写入不消耗序号）。
func (s *Store) Clock() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clock
}
