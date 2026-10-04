// Package engine 实现近实时索引引擎的写入、实时读、刷新、提交与崩溃恢复内核。
package engine

import (
	"errors"
	"sync"

	"ontology/searcher"
	"ontology/translog"
)

// Durability 是落盘策略。
type Durability uint8

const (
	Async   Durability = iota // 仅 Sync/Flush 落盘
	Request                   // 每个被接受的写操作返回前落盘
)

// Doc 是 Get/Search 返回的文档。
type Doc struct {
	ID   string
	Body []byte
	Seq  int64
}

type version struct {
	seq     int64
	deleted bool
	body    []byte
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotRecovered    = errors.New("engine not recovered")
	ErrInvalidState    = errors.New("invalid state")
	ErrConflict        = errors.New("version conflict")
	ErrNotFound        = errors.New("document not found")
)

// Engine 是近实时索引引擎。
type Engine struct {
	mu         sync.Mutex
	durability Durability
	crashed    bool

	versions map[string]version // 未刷新操作（含删除墓碑）
	view     *searcher.Searcher

	commitPoint map[string]searcher.Doc // 最近一次 Flush 持久化的提交点（恢复时用）

	log       *translog.TransLog
	maxSeq    int64
	committed int64
	synced    int64

	replayed   int // 最近一次 Recover 重放的日志条数
	getTouches int // 最近一次 Get 触碰的记录数
}

// New 创建引擎。
func New(durability Durability) *Engine {
	return &Engine{
		durability:  durability,
		versions:    map[string]version{},
		view:        searcher.New(),
		commitPoint: map[string]searcher.Doc{},
		log:         translog.New(0),
	}
}

// Index 写入文档；ifSeq 为 -1 无条件，否则要求实时最新 seq 恰为 ifSeq。返回分配的 seq。
func (e *Engine) Index(id string, body []byte, ifSeq int64) (int64, error) {
	if !validID(id) || len(body) > 65536 || (ifSeq != -1 && ifSeq < 1) {
		return 0, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrNotRecovered
	}
	if ifSeq != -1 {
		if cur, ok := e.liveVersion(id); !ok || cur.seq != ifSeq {
			return 0, ErrConflict
		}
	}
	seq := e.maxSeq + 1
	e.log.Append(seq, translog.IndexOp, id, body)
	e.versions[id] = version{seq: seq, body: append([]byte(nil), body...)}
	e.maxSeq = seq
	if e.durability == Request {
		e.log.Sync(seq)
		e.synced = seq
	}
	return seq, nil
}

// Delete 删除实时存活的文档，返回分配的 seq。
func (e *Engine) Delete(id string) (int64, error) {
	if !validID(id) {
		return 0, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrNotRecovered
	}
	if _, ok := e.liveVersion(id); !ok {
		return 0, ErrNotFound
	}
	seq := e.maxSeq + 1
	e.log.Append(seq, translog.DeleteOp, id, nil)
	e.versions[id] = version{seq: seq, deleted: true}
	e.maxSeq = seq
	if e.durability == Request {
		e.log.Sync(seq)
		e.synced = seq
	}
	return seq, nil
}

// Get 返回实时状态（版本表优先，其次搜索视图）。
func (e *Engine) Get(id string) (Doc, error) {
	if !validID(id) {
		return Doc{}, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return Doc{}, ErrNotRecovered
	}
	e.getTouches = 0
	if ver, ok := e.versions[id]; ok {
		e.getTouches = 1
		if ver.deleted {
			return Doc{}, ErrNotFound
		}
		return Doc{ID: id, Body: append([]byte(nil), ver.body...), Seq: ver.seq}, nil
	}
	e.getTouches = 2
	if doc, ok := e.view.Lookup(id); ok && !doc.Deleted {
		return Doc{ID: id, Body: append([]byte(nil), doc.Body...), Seq: doc.Seq}, nil
	}
	return Doc{}, ErrNotFound
}

// Search 返回搜索视图中存活的文档，按 id 字节序排列。
func (e *Engine) Search() ([]Doc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return nil, ErrNotRecovered
	}
	return fromSearcherDocs(e.view.Search()), nil
}

// Refresh 把版本表并入搜索视图并清空版本表。
func (e *Engine) Refresh() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.refreshLocked()
	return nil
}

// Sync 把日志落盘水位推进到 maxSeq。
func (e *Engine) Sync() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.log.Sync(e.maxSeq)
	e.synced = e.log.Synced()
	return nil
}

// Flush：Refresh 后整体提交，再换代日志。
func (e *Engine) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.refreshLocked()
	e.view.Commit()
	e.commitPoint = e.view.Snapshot()
	e.committed = e.maxSeq
	e.log.Sync(e.maxSeq)
	e.synced = e.maxSeq
	e.log.Rotate()
	return nil
}

// SetDurability 切换落盘策略；切到 Request 立即落盘一次。
func (e *Engine) SetDurability(mode Durability) error {
	if mode != Async && mode != Request {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.durability = mode
	if mode == Request {
		e.log.Sync(e.maxSeq)
		e.synced = e.log.Synced()
	}
	return nil
}

// Crash 模拟断电。
func (e *Engine) Crash() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.log.Crash()
	e.synced = e.log.Synced()
	e.maxSeq = e.synced
	e.versions = map[string]version{}
	e.view = searcher.New()
	e.crashed = true
	return nil
}

// Recover 从提交点装回并重放日志，返回重放条数。
func (e *Engine) Recover() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.crashed {
		return 0, ErrInvalidState
	}
	e.view.Load(e.commitPoint)
	e.versions = map[string]version{}
	entries := e.log.Replay(e.committed)
	e.replayed = len(entries)
	for _, entry := range entries {
		switch entry.Op {
		case translog.IndexOp:
			e.versions[entry.ID] = version{seq: entry.Seq, body: append([]byte(nil), entry.Body...)}
		case translog.DeleteOp:
			e.versions[entry.ID] = version{seq: entry.Seq, deleted: true}
		}
	}
	e.maxSeq = e.synced
	e.refreshLocked()
	e.crashed = false
	return e.replayed, nil
}

// MaxSeq 返回已分配的最大序号。
func (e *Engine) MaxSeq() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxSeq
}

// Synced 返回已落盘的最大序号。
func (e *Engine) Synced() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.synced
}

// Committed 返回已提交的最大序号。
func (e *Engine) Committed() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.committed
}

// Uncommitted 返回未提交操作数（恒等于事务日志条数）。
func (e *Engine) Uncommitted() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.log.Len()
}

func validID(id string) bool {
	return len(id) >= 1 && len(id) <= 512
}

// liveVersion 返回某 id 的实时存活版本；ok 为 false 表示实时不存活。
func (e *Engine) liveVersion(id string) (version, bool) {
	if ver, ok := e.versions[id]; ok {
		if ver.deleted {
			return version{}, false
		}
		return ver, true
	}
	if doc, ok := e.view.Lookup(id); ok && !doc.Deleted {
		return version{seq: doc.Seq, body: doc.Body}, true
	}
	return version{}, false
}

func (e *Engine) refreshLocked() {
	for id, ver := range e.versions {
		e.view.Apply(ver.seq, ver.deleted, id, ver.body)
	}
	e.versions = map[string]version{}
}

func fromSearcherDocs(docs []searcher.Doc) []Doc {
	if len(docs) == 0 {
		return []Doc{}
	}
	out := make([]Doc, len(docs))
	for i, doc := range docs {
		out[i] = Doc{ID: doc.ID, Body: doc.Body, Seq: doc.Seq}
	}
	return out
}
