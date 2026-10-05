// Package shard 维护各服的容量与在途迁移预留计数。
// 服的负载 = 现有角色数 + 以它为目的地有效在途迁移单数。
// 本包不加锁，并发安全由上层 transfer.System 的互斥锁保证。
package shard

// Shard 是一个服，容量固定。
type Shard struct {
	ID       int64
	Cap      int
	chars    int // 现有角色数
	reserved int // 有效在途预留数
}

// Registry 持有全部服。
type Registry struct {
	shards map[int64]*Shard
}

// NewRegistry 返回空的服注册表。
func NewRegistry() *Registry {
	return &Registry{shards: make(map[int64]*Shard)}
}

// Add 注册一个服；id 已存在时返回 false。
func (r *Registry) Add(id int64, cap int) bool {
	if _, ok := r.shards[id]; ok {
		return false
	}
	r.shards[id] = &Shard{ID: id, Cap: cap}
	return true
}

// Exists 报告服是否存在。
func (r *Registry) Exists(id int64) bool {
	_, ok := r.shards[id]
	return ok
}

// Load 返回服的负载：角色数 + 在途预留数。
func (r *Registry) Load(id int64) int {
	s := r.shards[id]
	return s.chars + s.reserved
}

// Cap 返回服的容量。
func (r *Registry) Cap(id int64) int {
	return r.shards[id].Cap
}

// Reserve 为一张迁移单占用一个在途预留；负载已满时返回 false。
func (r *Registry) Reserve(id int64) bool {
	s := r.shards[id]
	if s.chars+s.reserved >= s.Cap {
		return false
	}
	s.reserved++
	return true
}

// Release 释放一个在途预留（取消或过期）。
func (r *Registry) Release(id int64) {
	r.shards[id].reserved--
}

// Arrive 把一个在途预留转为常驻角色。
func (r *Registry) Arrive(id int64) {
	s := r.shards[id]
	s.reserved--
	s.chars++
}

// AddChar 直接登记一个常驻角色（Create）；负载已满时返回 false。
func (r *Registry) AddChar(id int64) bool {
	s := r.shards[id]
	if s.chars+s.reserved >= s.Cap {
		return false
	}
	s.chars++
	return true
}

// Depart 移除一个常驻角色。
func (r *Registry) Depart(id int64) {
	r.shards[id].chars--
}
