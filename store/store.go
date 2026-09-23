// Package store 对外存储：串起写入、读视图、提交与回收。
// 全部状态在进程内存；事务号只走注入的 txid.Source。
package store

import (
	"errors"
	"sync"

	"ontology/reclaim"
	"ontology/snapshot"
	"ontology/txid"
	"ontology/version"
)

var (
	ErrChainTooLong     = errors.New("store: version chain too long")
	ErrTooManySnapshots = errors.New("store: too many active snapshots")
	ErrTooManyVersions  = errors.New("store: too many versions total")
	ErrNotFound         = errors.New("store: key not found")
	ErrTxClosed         = errors.New("store: transaction closed")
)

// Config 是资源上限。零值表示不限制。
type Config struct {
	MaxVersionsPerKey int
	MaxSnapshots      int
	MaxTotalVersions  int
}

// Status 是键在某一快照下的存在性状态，三种结果彼此可判定。
type Status int

const (
	NeverExisted Status = iota // 该快照下无任何可见版本
	Deleted                    // 可见版本是删除标记
	Exists                     // 可见版本是普通值
)

// Stats 是只读查询结果。
type Stats struct {
	KeyVersions   int    // 该键当前版本数（已回收或从未存在为 0）
	Snapshots     int    // 全局活跃快照数
	Watermark     txid.T // 回收水位
	TotalVersions int    // 总版本数
}

// commitRecord 是提交过程的崩溃恢复索引（pending 索引）。
type commitRecord struct {
	keys      []string
	committed bool
}

// Store 是多版本可见性存储。并发安全：所有公开方法持同一把锁，
// 长事务在两次操作之间不持锁，不会阻塞其他键的读写。
type Store struct {
	mu     sync.Mutex
	src    txid.Source
	cfg    Config
	chains map[string]*version.Chain
	reg    *snapshot.Registry
	rec    *reclaim.Reclaimer
	txs    map[txid.T]*Tx
	pend   map[txid.T]*commitRecord
	total  int
	hook   func(point int)
}

// New 创建存储。src 为事务号注入源。
func New(src txid.Source, cfg Config) *Store {
	return &Store{
		src:    src,
		cfg:    cfg,
		chains: make(map[string]*version.Chain),
		reg:    snapshot.NewRegistry(),
		rec:    reclaim.New(),
		txs:    make(map[txid.T]*Tx),
		pend:   make(map[txid.T]*commitRecord),
	}
}

// SetCrashHook 注入「写入中途崩溃」钩子，在提交的每个原子步触发。
func (s *Store) SetCrashHook(h func(point int)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = h
}

func (s *Store) callHook(p int) {
	if s.hook != nil {
		s.hook(p)
	}
}

// snapshotLocked 在当前锁内建立快照：point 与活跃集合原子固化。
func (s *Store) snapshotLocked() snapshot.Snapshot {
	active := make([]txid.T, 0, len(s.txs))
	for id := range s.txs {
		active = append(active, id)
	}
	return snapshot.New(s.src.Peek(), active)
}

func (s *Store) getLocked(snap snapshot.Snapshot, key string) ([]byte, error) {
	ch := s.chains[key]
	if ch == nil {
		return nil, ErrNotFound
	}
	v, ok := ch.Visible(snap.Point, snap.IsActive)
	if !ok || v.Del {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v.Value...), nil
}

// Collect 推进水位并增量回收一轮，返回摘除的版本数。
// 与 BeginView 共用同一把锁，排除水位/快照竞态。
func (s *Store) Collect() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec.Advance(s.reg, s.src.Peek())
	n := 0
	for _, c := range s.rec.Collect() {
		ch := s.chains[c.Key]
		if ch == nil {
			continue
		}
		if top, ok := ch.Top(); ok && top != c.Commit && ch.Remove(c.Commit) {
			s.total--
			n++
		}
	}
	return n
}

// Recover 崩溃恢复：遍历 pending 索引（非全表），未提交的事务
// 按索引摘除尸体版本，已提交的仅丢弃索引。两种结局都无残留。
func (s *Store) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rec := range s.pend {
		if !rec.committed {
			for _, key := range rec.keys {
				if ch := s.chains[key]; ch != nil {
					s.total -= ch.RemoveUncommitted(id)
				}
			}
		}
		delete(s.pend, id)
		delete(s.txs, id)
	}
}

// Stats 只读查询。不推进除惰性回收以外的任何状态（本实现不触发
// 回收），同一时刻连查两次结果完全相同。
func (s *Store) Stats(key string) Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{
		Snapshots:     s.reg.Len(),
		Watermark:     s.rec.Watermark(),
		TotalVersions: s.total,
	}
	if ch := s.chains[key]; ch != nil {
		st.KeyVersions = ch.Len()
	}
	return st
}

// Examined 返回回收器累计考察的版本数（增量性证据）。
func (s *Store) Examined() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.Examined()
}
