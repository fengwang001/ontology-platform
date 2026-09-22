// Package store 对外提供多版本可见性存储：
// 写入、读视图、提交、回滚、崩溃恢复与增量回收。
//
// 锁序（防止死锁）：store.mu → reclaimer.mu → snapshot.Manager 内部锁。
// 任何锁都不会跨用户操作持有，长事务不阻塞其他键。
package store

import (
	"errors"
	"sync"

	"ontology/reclaim"
	"ontology/snapshot"
	"ontology/txid"
	"ontology/version"
)

// 三类资源超限错误，彼此可用 errors.Is 判定。
var (
	ErrChainLenExceeded = errors.New("store: 单键版本链长度超限")
	ErrVersionLimit     = errors.New("store: 总版本数超限")
	ErrSnapshotLimit    = snapshot.ErrLimit
)

// ErrTxClosed 表示事务已提交或回滚。
var ErrTxClosed = errors.New("store: 事务已关闭")

// ErrCrashed 表示提交在注入的崩溃点中断。
var ErrCrashed = errors.New("store: 提交中途崩溃")

// Lookup 是键查询的三种可判定结果。
type Lookup int

const (
	LookupNever   Lookup = iota // 键从未存在（对该快照）
	LookupDeleted               // 键已被删除（看到删除标记）
	LookupFound                 // 键存在
)

func (l Lookup) String() string {
	switch l {
	case LookupNever:
		return "never"
	case LookupDeleted:
		return "deleted"
	case LookupFound:
		return "found"
	}
	return "unknown"
}

// Config 是资源上限配置；零值表示不限制。
type Config struct {
	MaxChainLen  int // 单键已提交版本链长度上限
	MaxSnapshots int // 活跃快照数上限
	MaxVersions  int // 全局已提交版本总数上限
}

// Store 是多版本可见性存储。
type Store struct {
	mu    sync.RWMutex
	src   txid.Source
	snaps *snapshot.Manager
	rec   *reclaim.Reclaimer
	cfg   Config

	chains map[string]*version.Chain
	index  map[string][]*version.Version
	txs    map[txid.ID]*txRecord
	total  int

	crashHook func(CrashPoint) bool
}

// New 创建存储。事务号只从注入的 src 分配。
func New(src txid.Source, cfg Config) *Store {
	snaps := snapshot.NewManager(src, cfg.MaxSnapshots)
	return &Store{
		src:    src,
		snaps:  snaps,
		rec:    reclaim.New(snaps, src),
		cfg:    cfg,
		chains: map[string]*version.Chain{},
		index:  map[string][]*version.Version{},
		txs:    map[txid.ID]*txRecord{},
	}
}

// Begin 建立一个只读快照。
func (s *Store) Begin() (*snapshot.Snapshot, error) {
	return s.snaps.Begin()
}

// Release 关闭一个只读快照。
func (s *Store) Release(snap *snapshot.Snapshot) {
	s.snaps.Release(snap)
}

// ReadAt 在指定快照上读一个键。
func (s *Store) ReadAt(snap *snapshot.Snapshot, key string) ([]byte, Lookup) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain, ok := s.chains[key]
	if !ok {
		return nil, LookupNever
	}
	v, ok := chain.VisibleAt(snap)
	if !ok {
		return nil, LookupNever
	}
	if v.Deleted {
		return nil, LookupDeleted
	}
	return v.Value, LookupFound
}

// chainLen 返回键的已提交版本数（调用方须持有锁）。
func (s *Store) chainLen(key string) int {
	if c, ok := s.chains[key]; ok {
		return c.Len()
	}
	return 0
}
