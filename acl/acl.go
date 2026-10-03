// Package acl 维护主体的 admin 标志，并判定终止权限。
package acl

import (
	"errors"
	"sync"
)

// ErrEmptyPrincipal 表示主体为空字节串。
var ErrEmptyPrincipal = errors.New("acl: empty principal")

// ACL 是主体权限表。
type ACL struct {
	mu     sync.RWMutex
	admins map[string]bool
}

// New 创建空权限表。
func New() *ACL { return &ACL{admins: map[string]bool{}} }

// Grant 授予 p admin 标志。
// 空主体视为参数非法；Grant/Revoke 可与 registry 操作并发。
func (a *ACL) Grant(p []byte) error {
	if len(p) == 0 {
		return ErrEmptyPrincipal
	}
	a.mu.Lock()
	a.admins[string(p)] = true
	a.mu.Unlock()
	return nil
}

// Revoke 撤销 p 的 admin 标志。
func (a *ACL) Revoke(p []byte) error {
	if len(p) == 0 {
		return ErrEmptyPrincipal
	}
	a.mu.Lock()
	delete(a.admins, string(p))
	a.mu.Unlock()
	return nil
}

// IsAdmin 返回 p 是否持有 admin。
func (a *ACL) IsAdmin(p []byte) bool {
	a.mu.RLock()
	ok := a.admins[string(p)]
	a.mu.RUnlock()
	return ok
}

// CanTerminate 报告 p 是否可终止 owner 拥有的实例。
// 可终止当且仅当 p 是该 run 的所有者，或 p 持有 admin。
func (a *ACL) CanTerminate(p, owner []byte) bool {
	if string(p) == string(owner) {
		return true
	}
	return a.IsAdmin(p)
}
