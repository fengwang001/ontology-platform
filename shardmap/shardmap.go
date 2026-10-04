// Package shardmap 维护各索引的分片数、路由槽数、路由分区大小与写阻塞状态，
// 并裁决分裂与收缩的合法性。
package shardmap

import (
	"errors"
	"sync"

	"ontology/slot"
)

// 可被 errors.Is 区分的哨兵错误（拒绝次序见 DESIGN.md）。
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrExists          = errors.New("index already exists")
	ErrNotFound        = errors.New("index not found")
	ErrReadOnly        = errors.New("index is read-only")
	ErrNotWriteBlocked = errors.New("index is not write-blocked")
	ErrNotSplittable   = errors.New("index is not splittable")
	ErrNotShrinkable   = errors.New("index is not shrinkable")
	ErrIDConflict      = errors.New("document id conflict during shrink")
	ErrMissingRouting  = slot.ErrMissingRouting
	ErrDocumentMissing = errors.New("document not found")
)

// HashFunc 复用 slot 包的哈希函数类型。
type HashFunc = slot.HashFunc

// FNV32 重新导出默认哈希，便于调用方注入。
var FNV32 = slot.FNV32

// State 是某个索引某一时刻的不可变参数快照。
type State struct {
	N      int
	R      int
	P      int
	Block  bool
	Hasher slot.HashFunc
}

// Params 把快照转为 slot 定位参数。
func (s State) Params() slot.Params {
	return slot.Params{N: s.N, R: s.R, P: s.P, H: s.Hasher}
}

// Resharder 在分裂/收缩合法性通过后执行文档重分布；
// 返回非 nil conflictID 表示收缩撞 id（须为字节序最小的冲突 id）。
type Resharder interface {
	Reshard(indexName string, oldN, newN, r, p int, h slot.HashFunc) (conflictID []byte, err error)
}

type entry struct {
	n, r, p int
	block   bool
	hasher  slot.HashFunc
}

var (
	mu        sync.RWMutex
	indexes   = make(map[string]*entry)
	resharder Resharder
)

// RegisterResharder 注册重分布执行器（docstore 初始化时调用）。
func RegisterResharder(r Resharder) {
	mu.Lock()
	resharder = r
	mu.Unlock()
}

func validCreate(n, r, p int) bool {
	if n < 1 || n > 1024 {
		return false
	}
	if r < n || r > 1<<20 || r%n != 0 {
		return false
	}
	// P==1 关闭；否则 1 < P < N。
	if p != 1 && !(p > 1 && p < n) {
		return false
	}
	return true
}

// CreateIndex 创建索引。hasher 最多一个，缺省为 32 位 FNV-1a。
func CreateIndex(name string, n, r, p int, hasher ...slot.HashFunc) error {
	if len(hasher) > 1 || !validCreate(n, r, p) {
		return ErrInvalidArgument
	}
	h := slot.FNV32
	if len(hasher) == 1 && hasher[0] != nil {
		h = hasher[0]
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := indexes[name]; ok {
		return ErrExists
	}
	indexes[name] = &entry{n: n, r: r, p: p, hasher: h}
	return nil
}

// Exists 报告索引是否存在。
func Exists(name string) bool {
	mu.RLock()
	_, ok := indexes[name]
	mu.RUnlock()
	return ok
}

// Snapshot 返回索引当前状态的只读副本；不存在返回 ErrNotFound。
func Snapshot(name string) (State, error) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := indexes[name]
	if !ok {
		return State{}, ErrNotFound
	}
	return State{N: e.n, R: e.r, P: e.p, Block: e.block, Hasher: e.hasher}, nil
}

// SetWriteBlock 置/清写阻塞。
func SetWriteBlock(name string, on bool) error {
	mu.Lock()
	defer mu.Unlock()
	e, ok := indexes[name]
	if !ok {
		return ErrNotFound
	}
	e.block = on
	return nil
}

// Split 把分片数增大到 n2。要求已置写阻塞，且 n2>n、n|n2、n2|r。
func Split(name string, n2 int) error {
	if n2 < 1 || n2 > 1024 {
		return ErrInvalidArgument
	}
	mu.Lock()
	defer mu.Unlock()
	e, ok := indexes[name]
	if !ok {
		return ErrNotFound
	}
	if !e.block {
		return ErrNotWriteBlocked
	}
	if n2 <= e.n || n2%e.n != 0 || e.r%n2 != 0 {
		return ErrNotSplittable
	}
	if resharder != nil {
		if conflictID, err := resharder.Reshard(name, e.n, n2, e.r, e.p, e.hasher); err != nil {
			return err
		} else if conflictID != nil {
			return ErrIDConflict // 分裂理论上不会发生，防御性处理
		}
	}
	e.n = n2
	return nil
}

// Shrink 把分片数减小到 n2。要求已置写阻塞、n2<n、n2|n；P>1 时还需 n2>P。
// 收缩撞 id 时返回包裹了最小冲突 id 的 ErrIDConflict，且状态不变。
func Shrink(name string, n2 int) error {
	if n2 < 1 || n2 > 1024 {
		return ErrInvalidArgument
	}
	mu.Lock()
	defer mu.Unlock()
	e, ok := indexes[name]
	if !ok {
		return ErrNotFound
	}
	if !e.block {
		return ErrNotWriteBlocked
	}
	if n2 >= e.n || e.n%n2 != 0 {
		return ErrNotShrinkable
	}
	if e.p > 1 && n2 <= e.p {
		return ErrNotShrinkable
	}
	if resharder != nil {
		conflictID, err := resharder.Reshard(name, e.n, n2, e.r, e.p, e.hasher)
		if err != nil {
			return err
		}
		if conflictID != nil {
			return &IDConflictError{ID: conflictID}
		}
	}
	e.n = n2
	return nil
}

// IDConflictError 实现 ErrIDConflict，并携带字节序最小的冲突 id。
type IDConflictError struct {
	ID []byte
}

func (e *IDConflictError) Error() string        { return ErrIDConflict.Error() + ": " + string(e.ID) }
func (e *IDConflictError) Is(target error) bool { return target == ErrIDConflict }

// SearchShards 返回该 routing 的文档可能落入的全部分片（升序去重）。
func SearchShards(name string, routing []byte) ([]int, error) {
	mu.RLock()
	e, ok := indexes[name]
	if !ok {
		mu.RUnlock()
		return nil, ErrNotFound
	}
	st := State{N: e.n, R: e.r, P: e.p, Hasher: e.hasher}
	mu.RUnlock()
	if len(routing) == 0 {
		return nil, ErrMissingRouting
	}
	return slot.SearchShards(st.Params(), routing)
}
