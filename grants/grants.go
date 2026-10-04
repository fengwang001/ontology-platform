// Package grants 维护主体（非空字节串）的 64 位权限掩码。
package grants

import (
	"errors"
	"sync"
)

// ApproveBit 是审批权限位（位 63）；位 0 到 62 是业务权限位。
const ApproveBit = uint64(1) << 63

// ErrParam 表示参数非法：主体为空或掩码为零。
var ErrParam = errors.New("grants: invalid parameter")

// Registry 记录每个主体的权限掩码，默认掩码为 0。并发安全。
type Registry struct {
	mu    sync.RWMutex
	masks map[string]uint64
}

// New 返回空的注册表。
func New() *Registry {
	return &Registry{masks: make(map[string]uint64)}
}

func valid(p string, m uint64) bool {
	return p != "" && m != 0
}

// Grant 给主体 p 追加掩码 m 中的权限位。
func (r *Registry) Grant(p string, m uint64) error {
	if !valid(p, m) {
		return ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.masks[p] |= m
	return nil
}

// Revoke 移除主体 p 的掩码 m 中的权限位。
func (r *Registry) Revoke(p string, m uint64) error {
	if !valid(p, m) {
		return ErrParam
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.masks[p] &^= m
	return nil
}

// Mask 返回主体 p 的当前掩码；未知主体为 0。
func (r *Registry) Mask(p string) uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.masks[p]
}
