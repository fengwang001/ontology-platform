// Package acl 维护主体的 admin 标志与终止权限判定。
package acl

import (
	"bytes"
	"sync"
)

// ACL 记录哪些主体持有 admin 标志。
type ACL struct {
	mu     sync.RWMutex
	admins map[string]bool
}

// New 创建空的权限集合。
func New() *ACL {
	return &ACL{admins: make(map[string]bool)}
}

// Grant 授予主体 admin 标志。
func (a *ACL) Grant(p []byte) {
	if len(p) == 0 {
		return
	}
	a.mu.Lock()
	a.admins[string(p)] = true
	a.mu.Unlock()
}

// Revoke 撤销主体 admin 标志。
func (a *ACL) Revoke(p []byte) {
	if len(p) == 0 {
		return
	}
	a.mu.Lock()
	delete(a.admins, string(p))
	a.mu.Unlock()
}

// CanTerminate 判定主体是否可终止属于 owner 的运行中实例。
func (a *ACL) CanTerminate(p, owner []byte) bool {
	if len(p) == 0 {
		return false
	}
	if bytes.Equal(p, owner) {
		return true
	}
	a.mu.RLock()
	ok := a.admins[string(p)]
	a.mu.RUnlock()
	return ok
}
