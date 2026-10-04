// Package shard 维护各游戏服的容量与在途迁移预留。
package shard

import "errors"

// 哨兵错误。
var (
	ErrInvalidParam = errors.New("shard: invalid parameter")
	ErrShardExists  = errors.New("shard: shard already exists")
	ErrNoShard      = errors.New("shard: shard not found")
	ErrCapReached   = errors.New("shard: load reached capacity")
)

// Shard 表示一个游戏服。
type Shard struct {
	id  string
	cap int

	residents int // 现有角色数
	reserved  int // 以本服为目的地的有效在途迁移单数
}

// ID 返回服标识。
func (s *Shard) ID() string { return s.id }

// Cap 返回容量。
func (s *Shard) Cap() int { return s.cap }

// Residents 返回现有角色数。
func (s *Shard) Residents() int { return s.residents }

// Reserved 返回有效在途预留数。
func (s *Shard) Reserved() int { return s.reserved }

// Load 为现有角色数 + 有效在途预留数。
func (s *Shard) Load() int { return s.residents + s.reserved }

// Registry 管理全部服。
type Registry struct {
	shards map[string]*Shard
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{shards: map[string]*Shard{}}
}

// NewShard 创建并登记一个容量为 cap 的服。
func (r *Registry) NewShard(id string, cap int) (*Shard, error) {
	if id == "" || cap < 1 {
		return nil, ErrInvalidParam
	}
	if _, ok := r.shards[id]; ok {
		return nil, ErrShardExists
	}
	sh := &Shard{id: id, cap: cap}
	r.shards[id] = sh
	return sh, nil
}

// Get 返回服。
func (r *Registry) Get(id string) (*Shard, bool) {
	sh, ok := r.shards[id]
	return sh, ok
}

// AddResident 角色进入本服。
func (r *Registry) AddResident(id string) error {
	sh, ok := r.shards[id]
	if !ok {
		return ErrNoShard
	}
	if sh.Load() >= sh.cap {
		return ErrCapReached
	}
	sh.residents++
	return nil
}

// RemoveResident 角色离开本服。
func (r *Registry) RemoveResident(id string) error {
	sh, ok := r.shards[id]
	if !ok {
		return ErrNoShard
	}
	if sh.residents == 0 {
		return ErrInvalidParam
	}
	sh.residents--
	return nil
}

// Reserve 在目的服占用一个在途预留。
func (r *Registry) Reserve(id string) error {
	sh, ok := r.shards[id]
	if !ok {
		return ErrNoShard
	}
	if sh.Load() >= sh.cap {
		return ErrCapReached
	}
	sh.reserved++
	return nil
}

// ReleaseReserved 释放一个在途预留（过期取消或回迁发起）。
func (r *Registry) ReleaseReserved(id string) {
	sh, ok := r.shards[id]
	if !ok || sh.reserved == 0 {
		return
	}
	sh.reserved--
}

// AdoptReserved 将一个在途预留转为常驻角色。
func (r *Registry) AdoptReserved(id string) {
	sh, ok := r.shards[id]
	if !ok || sh.reserved == 0 {
		return
	}
	sh.reserved--
	sh.residents++
}
