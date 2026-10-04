// Package engine 实现近实时索引引擎内核：写入、实时读、提交与崩溃恢复。
//
// 实时状态 = 版本表（id 到最新未刷新操作）优先，其次搜索视图。
// 持久性完全由事务日志保证：Refresh 不落盘，Flush 建立提交点并换代，
// Crash 丢弃水位之后的日志尾部与全部内存状态，Recover 从提交点
// 装回视图并重放 (committed, synced] 区间的日志条目。
// 所有导出方法持有同一把互斥锁，结果等价于某个串行顺序。
package engine

import (
	"errors"
	"sync"

	"ontology/searcher"
	"ontology/translog"
)

var (
	ErrInvalidArgument  = errors.New("engine: invalid argument")
	ErrNotRecovered     = errors.New("engine: not recovered from crash")
	ErrVersionConflict  = errors.New("engine: version conflict")
	ErrDocumentNotFound = errors.New("engine: document not found")
	ErrStateMismatch    = errors.New("engine: operation not allowed in current state")
)

// Durability 是写操作的落盘策略。
type Durability int

const (
	// Async 仅 Sync/Flush 时落盘。
	Async Durability = iota
	// Request 每个被接受的写操作返回前落盘。
	Request
)

const (
	maxIDLen   = 512
	maxBodyLen = 65536
)

// pendingOp 是版本表中一个 id 的最新未刷新操作。
type pendingOp struct {
	kind translog.Kind
	body []byte
	seq  int64
}

// Engine 是索引引擎内核。
type Engine struct {
	mu            sync.Mutex
	tl            *translog.Log
	view          *searcher.View
	committedView *searcher.View
	vtab          map[string]pendingOp
	maxSeq        int64
	committed     int64
	synced        int64
	mode          Durability
	crashed       bool
	replayed      int
	getTouches    int
}

// New 创建一个空引擎，默认 Async 落盘策略。
func New() *Engine {
	return &Engine{
		tl:            translog.New(1),
		view:          searcher.New(),
		committedView: searcher.New(),
		vtab:          make(map[string]pendingOp),
		mode:          Async,
	}
}

func validID(id []byte) bool {
	return len(id) >= 1 && len(id) <= maxIDLen
}

// realtime 返回 id 的实时状态：版本表优先，其次搜索视图。
func (e *Engine) realtime(id []byte) (body []byte, seq int64, alive bool) {
	if op, ok := e.vtab[string(id)]; ok {
		if op.kind == translog.KindDelete {
			return nil, op.seq, false
		}
		return op.body, op.seq, true
	}
	doc, ok := e.view.Get(id)
	if !ok {
		return nil, 0, false
	}
	return doc.Body, doc.Seq, true
}

// Index 写入文档。ifSeq 为 -1 表示无条件；否则要求该 id 实时存活
// 且其最新操作的 seq 恰等于 ifSeq。
func (e *Engine) Index(id, body []byte, ifSeq int64) (int64, error) {
	if !validID(id) || len(body) > maxBodyLen || ifSeq < -1 {
		return 0, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrNotRecovered
	}
	if ifSeq != -1 {
		_, seq, alive := e.realtime(id)
		if !alive || seq != ifSeq {
			return 0, ErrVersionConflict
		}
	}
	return e.appendLocked(translog.Op{
		Kind: translog.KindIndex,
		ID:   append([]byte(nil), id...),
		Body: append([]byte(nil), body...),
	}), nil
}

// Delete 删除文档。实时不存活的 id 报文档不存在。
func (e *Engine) Delete(id []byte) (int64, error) {
	if !validID(id) {
		return 0, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrNotRecovered
	}
	if _, _, alive := e.realtime(id); !alive {
		return 0, ErrDocumentNotFound
	}
	return e.appendLocked(translog.Op{
		Kind: translog.KindDelete,
		ID:   append([]byte(nil), id...),
	}), nil
}

// appendLocked 分配序号、追加日志、记入版本表，并按策略落盘。
func (e *Engine) appendLocked(op translog.Op) int64 {
	e.maxSeq++
	op.Seq = e.maxSeq
	e.tl.Append(op)
	e.vtab[string(op.ID)] = pendingOp{kind: op.Kind, body: op.Body, seq: op.Seq}
	if e.mode == Request {
		e.syncLocked()
	}
	return op.Seq
}

// syncLocked 把落盘水位推进到 maxSeq。
func (e *Engine) syncLocked() {
	e.synced = e.maxSeq
	e.tl.SyncTo(e.maxSeq)
}

// Get 返回 id 的实时 body 与 seq；不存活报文档不存在。
func (e *Engine) Get(id []byte) ([]byte, int64, error) {
	if !validID(id) {
		return nil, 0, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return nil, 0, ErrNotRecovered
	}
	e.getTouches = 0
	if op, ok := e.vtab[string(id)]; ok {
		e.getTouches++
		if op.kind == translog.KindDelete {
			return nil, 0, ErrDocumentNotFound
		}
		return append([]byte(nil), op.body...), op.seq, nil
	}
	e.getTouches++
	doc, ok := e.view.Get(id)
	if !ok {
		return nil, 0, ErrDocumentNotFound
	}
	return append([]byte(nil), doc.Body...), doc.Seq, nil
}

// Search 返回搜索视图中存活的文档（id 字节序），看不到未刷新的操作。
func (e *Engine) Search() ([]searcher.Doc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return nil, ErrNotRecovered
	}
	return e.view.List(), nil
}

// Refresh 把版本表中的全部操作并入搜索视图并清空版本表。
// 不落盘、不动事务日志。
func (e *Engine) Refresh() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.refreshLocked()
	return nil
}

func (e *Engine) refreshLocked() {
	for id, op := range e.vtab {
		if op.kind == translog.KindDelete {
			e.view.ApplyDelete([]byte(id))
		} else {
			e.view.ApplyIndex([]byte(id), op.body, op.seq)
		}
	}
	e.vtab = make(map[string]pendingOp)
}

// Sync 把事务日志落盘到 maxSeq。
func (e *Engine) Sync() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.syncLocked()
	return nil
}

// Flush 先 Refresh，再把搜索视图整体提交为提交点
// （committed=maxSeq、synced=maxSeq），然后事务日志换代。
func (e *Engine) Flush() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.refreshLocked()
	e.committedView = e.view.Snapshot()
	e.committed = e.maxSeq
	e.syncLocked()
	e.tl.Rollover()
	return nil
}

// SetDurability 切换落盘策略；切到 Request 时立即落盘一次。
func (e *Engine) SetDurability(mode Durability) error {
	if mode != Async && mode != Request {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.mode = mode
	if mode == Request {
		e.syncLocked()
	}
	return nil
}

// Crash 模拟断电：日志中 seq 大于 synced 的尾部丢失，
// 版本表与未提交的搜索视图全部丢失，引擎进入崩溃态。
func (e *Engine) Crash() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrNotRecovered
	}
	e.tl.Crash()
	e.vtab = make(map[string]pendingOp)
	e.view = nil
	e.crashed = true
	return nil
}

// Recover 从提交点装回搜索视图，按序重放 (committed, synced]
// 区间的日志条目，重放末尾做一次 Refresh。maxSeq 回到 synced，
// 丢失的序号会被重新分配。返回重放条数。
func (e *Engine) Recover() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.crashed {
		return 0, ErrStateMismatch
	}
	e.view = e.committedView.Snapshot()
	e.replayed = 0
	for _, op := range e.tl.Entries() {
		if op.Seq <= e.committed || op.Seq > e.synced {
			continue
		}
		if op.Kind == translog.KindDelete {
			e.view.ApplyDelete(op.ID)
		} else {
			e.view.ApplyIndex(op.ID, op.Body, op.Seq)
		}
		e.replayed++
	}
	e.maxSeq = e.synced
	e.crashed = false
	return e.replayed, nil
}
