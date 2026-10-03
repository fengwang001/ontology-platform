// Package grants 维护主体的 64 位权限掩码。
//
// 位 63 是审批权限位（ApproverBit），位 0–62 是业务权限位。
// 主体为非空字节串；未登记的主体掩码默认为 0。
package grants

import (
	"errors"
	"sync"
)

// ApproverBit 是审批权限位（位 63）。
const ApproverBit uint64 = uint64(1) << 63

// ErrArg 表示参数非法：主体为空或掩码为零。
var ErrArg = errors.New("grants: illegal argument")

// Registry 是并发安全的主体权限表。
type Registry struct {
	mu    sync.RWMutex
	masks map[string]uint64
	reads int // Mask 的调用次数（非导出，供包内测试核对）
}

// New 创建空的权限表。
func New() *Registry {
	return &Registry{masks: make(map[string]uint64)}
}

// Grant 给主体 p 增加掩码 m 中的全部位；m 必须非零。
func (r *Registry) Grant(p []byte, m uint64) error {
	if len(p) == 0 || m == 0 {
		return ErrArg
	}
	k := string(p)
	r.mu.Lock()
	r.masks[k] |= m
	r.mu.Unlock()
	return nil
}

// Revoke 撤销主体 p 掩码 m 中的全部位；m 必须非零。重复撤销是幂等的。
func (r *Registry) Revoke(p []byte, m uint64) error {
	if len(p) == 0 || m == 0 {
		return ErrArg
	}
	k := string(p)
	r.mu.Lock()
	r.masks[k] &^= m
	r.mu.Unlock()
	return nil
}

// Mask 返回主体 p 当前的权限掩码；未登记时为 0。
// 每次调用恰好构成一次读，调用方据此保证 StartStep 只读一次权限。
func (r *Registry) Mask(p []byte) uint64 {
	r.mu.Lock()
	v := r.masks[string(p)]
	r.reads++
	r.mu.Unlock()
	return v
}
