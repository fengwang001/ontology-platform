// Package store 负责实例持久化与版本仲裁：
// 版本链存储、系统时间序号分配、乐观并发校验、可追溯边界校验。
package store

import (
	"fmt"
	"sync"
	"time"

	"ontology/core"
)

// chain 是单个主键的追加式版本链。
type chain struct {
	versions    []core.Version
	minBizStart int64
}

// Store 是版本仲裁器。所有写入经 Commit 串行化，
// 最终效果等价于按某个全局串行顺序逐一应用。
type Store struct {
	mu     sync.Mutex
	chains map[core.Key]*chain
	now    func() time.Time
}

// New 创建空 Store。
func New() *Store {
	return &Store{chains: make(map[core.Key]*chain), now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// Commit 校验并提交一次写入。
//
// 拒绝次序（只报第一个命中的原因）：
//  1. 参数非法（主键为空、业务时间起点为负、并发凭证格式非法）；
//  2. 乐观并发凭证与当前最新版本不一致；
//  3. 业务时间起点早于该主键已确认提交的最早可追溯边界。
//
// 被拒绝的写入不占用系统时间序号、不改变最新版本指针、不产生任何
// 可被索引看到的版本。所有校验与追加在同一把锁内完成，因此并发写入
// 的最终效果等价于按某个全局串行顺序逐一应用。
func (s *Store) Commit(req core.WriteRequest) (core.Version, *core.Error) {
	expected, verr := validate(req)
	if verr != nil {
		return core.Version{}, verr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c := s.chains[req.Key]
	var latest uint64
	if c != nil {
		latest = uint64(len(c.versions))
	}
	if expected != latest {
		return core.Version{}, &core.Error{
			Code: core.ErrConcurrencyConflict,
			Message: fmt.Sprintf("key %s: credential %q bases on seq %d, current latest is seq %d",
				req.Key, req.Credential, expected, latest),
		}
	}
	if c != nil && len(c.versions) > 0 && req.BizStart < c.minBizStart {
		return core.Version{}, &core.Error{
			Code: core.ErrBeforeBoundary,
			Message: fmt.Sprintf("key %s: bizStart %d is before committed traceable boundary %d",
				req.Key, req.BizStart, c.minBizStart),
		}
	}

	v := core.Version{
		Seq:       latest + 1,
		BizStart:  req.BizStart,
		Payload:   req.Payload,
		Deleted:   req.Delete,
		WallClock: s.now(),
	}
	if c == nil {
		c = &chain{minBizStart: req.BizStart}
		s.chains[req.Key] = c
	}
	c.versions = append(c.versions, v)
	if req.BizStart < c.minBizStart {
		c.minBizStart = req.BizStart
	}
	return v, nil
}

// validate 做与状态无关的参数校验，返回凭证声明的前序版本号。
func validate(req core.WriteRequest) (uint64, *core.Error) {
	if req.Key.Type == "" || req.Key.ID == "" {
		return 0, &core.Error{Code: core.ErrInvalidArgument, Message: "object type and primary key must both be non-empty"}
	}
	if req.BizStart < 0 {
		return 0, &core.Error{Code: core.ErrInvalidArgument,
			Message: fmt.Sprintf("bizStart %d is negative", req.BizStart)}
	}
	n, err := core.ParseCredential(req.Credential)
	if err != nil {
		return 0, &core.Error{Code: core.ErrInvalidArgument, Message: err.Error()}
	}
	return n, nil
}

// Snapshot 返回主键的版本链拷贝与最早可追溯边界。
func (s *Store) Snapshot(k core.Key) ([]core.Version, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.chains[k]
	if c == nil {
		return nil, 0, false
	}
	out := make([]core.Version, len(c.versions))
	copy(out, c.versions)
	return out, c.minBizStart, true
}

// LatestSeq 返回主键当前最新版本号（无版本时为 0）。
func (s *Store) LatestSeq(k core.Key) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chains[k]; c != nil {
		return uint64(len(c.versions))
	}
	return 0
}
