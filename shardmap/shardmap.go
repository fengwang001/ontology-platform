// Package shardmap 维护索引的分片数、路由槽数、路由分区大小与写阻塞状态，
// 并负责创建、分裂、收缩的合法性判定。
package shardmap

import (
	"errors"
	"sync"

	"ontology/slot"
)

var (
	ErrInvalidParam       = errors.New("invalid parameter")
	ErrIndexExists        = errors.New("index already exists")
	ErrIndexNotFound      = errors.New("index not found")
	ErrIndexReadOnly      = errors.New("index is read-only")
	ErrWriteBlockRequired = errors.New("write block required")
	ErrMissingRouting     = errors.New("missing routing")
	ErrCannotSplit        = errors.New("cannot split")
	ErrCannotShrink       = errors.New("cannot shrink")
)

// Option 配置 CreateIndex。
type Option func(*options)

type options struct {
	hash slot.HashFunc
}

// WithHash 注入哈希函数；缺省为 32 位 FNV-1a。
func WithHash(h slot.HashFunc) Option {
	return func(o *options) { o.hash = h }
}

// Index 是一份索引的路由元数据。
type Index struct {
	mu sync.RWMutex

	name    string
	n       int
	r       int
	p       int
	hash    slot.HashFunc
	blocked bool
}

// Snapshot 是元数据的只读快照。
type Snapshot struct {
	N, R, P int
	Hash    slot.HashFunc
}

// CreateIndex 创建并登记索引。
func CreateIndex(name string, N, R, P int, opts ...Option) (*Index, error) {
	if !validCreateParams(N, R, P) {
		return nil, ErrInvalidParam
	}
	cfg := options{hash: slot.FNV1a32}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.hash == nil {
		return nil, ErrInvalidParam
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[name]; ok {
		return nil, ErrIndexExists
	}
	x := &Index{name: name, n: N, r: R, p: P, hash: cfg.hash}
	registry[name] = x
	return x, nil
}

// Get 按名获取索引。
func Get(name string) (*Index, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	x, ok := registry[name]
	if !ok {
		return nil, ErrIndexNotFound
	}
	return x, nil
}

// SetWriteBlock 设置索引写阻塞状态。
func SetWriteBlock(name string, on bool) error {
	x, err := Get(name)
	if err != nil {
		return err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.blocked = on
	return nil
}

// Name 返回索引名。
func (x *Index) Name() string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.name
}

// Snapshot 返回当前元数据快照。
func (x *Index) Snapshot() Snapshot {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return Snapshot{N: x.n, R: x.r, P: x.p, Hash: x.hash}
}

// WriteBlocked 返回是否已置写阻塞。
func (x *Index) WriteBlocked() bool {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.blocked
}

// ValidateSplit 判定分裂到 n2 是否合法。
// n2 超出 1..1024 为参数非法；n2<=N、N 不整除 n2、n2 不整除 R 均为不可分裂。
func (x *Index) ValidateSplit(n2 int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if n2 < 1 || n2 > 1024 {
		return ErrInvalidParam
	}
	if n2 <= x.n || n2%x.n != 0 || x.r%n2 != 0 {
		return ErrCannotSplit
	}
	return nil
}

// ValidateShrink 判定收缩到 n2 是否合法。
// n2 超出 1..1024 为参数非法；n2>=N、n2 不整除 N、P>1 且 n2<=P 均为不可收缩。
func (x *Index) ValidateShrink(n2 int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if n2 < 1 || n2 > 1024 {
		return ErrInvalidParam
	}
	if n2 >= x.n || x.n%n2 != 0 || (x.p > 1 && n2 <= x.p) {
		return ErrCannotShrink
	}
	return nil
}

// CommitN 在文档重分布成功后提交新的分片数。
func (x *Index) CommitN(n2 int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.n = n2
}

func validCreateParams(N, R, P int) bool {
	if N < 1 || N > 1024 {
		return false
	}
	if R < N || R > 1<<20 || R%N != 0 {
		return false
	}
	if P == 1 {
		return true
	}
	return P > 1 && P < N
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*Index{}
)
