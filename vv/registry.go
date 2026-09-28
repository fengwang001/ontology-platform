package vv

import "sync"

// Change 是一条全局唯一的键值变更，由 (Origin, Seq) 标识。
type Change struct {
	Origin    string
	Seq       int64
	Key       string
	Value     string
	Timestamp string
}

// VersionVector 记录每个已见来源的最大已应用序号。
type VersionVector map[string]int64

// Registry 管理一组已注册副本名与共享限制。
type Registry struct {
	mu        sync.RWMutex
	members   map[string]bool
	maxLog    int
	replicas  map[string]*Replica
}

// NewRegistry 建立注册表，registered 为合法副本名集合。
func NewRegistry(registered []string, maxLogEntries int) *Registry {
	reg := &Registry{
		members:  make(map[string]bool, len(registered)),
		maxLog:   maxLogEntries,
		replicas: make(map[string]*Replica),
	}
	for _, name := range registered {
		reg.members[name] = true
	}
	return reg
}

// IsRegistered 报告 name 是否为注册副本。
func (r *Registry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.members[name]
}

// validateVector 校验向量：来源必须全部注册，值必须为正（>=0），
// 且不得超过该来源在注册表内已知的最大序号。
func (r *Registry) validateVector(v VersionVector) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for origin, seq := range v {
		if !r.members[origin] {
			return newError(KindInvalidVector, "vector contains unregistered source %q", origin)
		}
		if seq < 0 {
			return newError(KindInvalidVector, "vector entry %q has negative value %d", origin, seq)
		}
	}
	return nil
}

// Replica 表示一个可独立写入、可与他人同步的副本。
type Replica struct {
	registry *Registry
	name     string

	// mu 保护 log 与 vector。log 始终保持 (来源, 序号) 全局有序。
	mu     sync.Mutex
	log    []Change
	vector VersionVector
}

// Name 返回副本名。
func (rep *Replica) Name() string { return rep.name }
