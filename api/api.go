// Package api 对外包装租约管理器，并提供内置操作序列的自检。依赖 mgr。
package api

import (
	"errors"
	"fmt"

	"ontology/lease"
	"ontology/mgr"
)

// API 是租约服务的对外入口，所有方法可并发调用。
type API struct{ m *mgr.Mgr }

// New 以固定租期 ttl 创建服务。
func New(ttl int) *API { return &API{m: mgr.New(ttl)} }

func (a *API) Acquire(name, owner string, now int) (int, error) {
	return a.m.Acquire(name, owner, now)
}
func (a *API) Renew(name string, token, now int) error { return a.m.Renew(name, token, now) }
func (a *API) Expired(name string, now int) bool       { return a.m.Expired(name, now) }
func (a *API) ExpiredAll(now int) []string             { return a.m.ExpiredAll(now) }

// Lookup 查询租约现状（owner、token、expiry）；未授予的名字报 mgr.ErrNotFound。
func (a *API) Lookup(name string) (string, int, int, error) { return a.m.Lookup(name) }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	ck := New(10)
	// 不变量2：栅栏单调——token 严格递增，旧 token 被栅栏。
	t1, _ := ck.Acquire("x", "A", 0)
	t2, _ := ck.Acquire("x", "B", 1) // expiry=11
	if t2 != t1+1 {
		return fmt.Errorf("selfcheck: token 未严格递增 %d->%d", t1, t2)
	}
	if err := ck.Renew("x", t1, 2); !errors.Is(err, lease.ErrStaleToken) {
		return fmt.Errorf("selfcheck: 旧 token 未被栅栏: %v", err)
	}
	// 不变量3：续期只在存活期——now==expiry 即拒绝。
	if err := ck.Renew("x", t2, 11); !errors.Is(err, lease.ErrExpired) {
		return fmt.Errorf("selfcheck: 到期续期未拒绝: %v", err)
	}
	// 不变量1：Expired 与逐字比较 now>=expiry 的朴素结果一致。
	for now := 0; now <= 12; now++ {
		if ck.Expired("x", now) != (now >= 11) {
			return fmt.Errorf("selfcheck: Expired(%d) 与朴素比较不一致", now)
		}
	}
	// 不变量4：失败不留痕——四类拒绝后状态原样、仍可正常使用。
	o1, k1, e1, _ := ck.Lookup("x")
	ck.Acquire("", "C", 0)
	ck.Renew("ghost", 1, 0)
	ck.Renew("x", 999, 2)
	ck.Renew("x", t2, 11)
	o2, k2, e2, _ := ck.Lookup("x")
	if o1 != o2 || k1 != k2 || e1 != e2 {
		return errors.New("selfcheck: 被拒操作改变了状态")
	}
	if err := ck.Renew("x", t2, 5); err != nil {
		return fmt.Errorf("selfcheck: 拒绝后无法正常续期: %v", err)
	}
	return nil
}
