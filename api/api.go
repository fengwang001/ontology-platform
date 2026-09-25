// Package api 是对外门面：存储创建、租户读写、管理操作、
// View 快照与 SelfCheck 自检。仅依赖 store 包。
package api

import (
	"fmt"

	"ontology/store"
)

// 全部哨兵错误在 api 层原样可用，调用方只需依赖 api 一个包即可判定。
var (
	ErrNoTenant        = store.ErrNoTenant
	ErrDuplicateTenant = store.ErrDuplicateTenant
	ErrInvalidID       = store.ErrInvalidID
	ErrInvalidQuota    = store.ErrInvalidQuota
	ErrUnauthorized    = store.ErrUnauthorized
	ErrEmptyKey        = store.ErrEmptyKey
	ErrNotFound        = store.ErrNotFound
	ErrQuotaKeys       = store.ErrQuotaKeys
	ErrQuotaBytes      = store.ErrQuotaBytes
)

// TenantView 重导出 store 的快照类型。
type TenantView = store.TenantView

// API 是多租户命名空间与配额隔离存储的对外接口。
type API struct {
	s *store.Store
}

// New 创建存储；adminToken 非空，是 Purge/SetQuota 的凭据。
func New(adminToken string) *API {
	return &API{s: store.New(adminToken)}
}

func (a *API) Register(id string, maxKeys, maxBytes int) error {
	return a.s.Register(id, maxKeys, maxBytes)
}

func (a *API) Put(id, key, val string) error { return a.s.Put(id, key, val) }

func (a *API) Get(id, key string) (string, error) { return a.s.Get(id, key) }

func (a *API) Del(id, key string) error { return a.s.Del(id, key) }

func (a *API) Usage(id string) (int, int, error) { return a.s.Usage(id) }

func (a *API) Purge(token, id string) error { return a.s.Purge(token, id) }

func (a *API) SetQuota(token, id string, maxKeys, maxBytes int) error {
	return a.s.SetQuota(token, id, maxKeys, maxBytes)
}

// View 返回全部租户的只读快照（Items 深拷贝）。
func (a *API) View() []TenantView { return a.s.View() }

// SelfCheck 对内置操作序列核验四条不变量，全部成立返回 nil。
func (a *API) SelfCheck() error {
	// 隔离 + 朴素参照：与每个租户各自的朴素 map 同步执行同序列操作。
	ref := map[string]map[string]string{"A": {}, "B": {}}
	must := func(cond bool, msg string) error {
		if !cond {
			return fmt.Errorf("selfcheck: %s", msg)
		}
		return nil
	}
	if err := a.Register("A", 2, 100); err != nil {
		return err
	}
	if err := a.Register("B", 2, 100); err != nil {
		return err
	}
	seq := []struct {
		id, k, v string
	}{{"A", "x", "1"}, {"A", "y", "22"}, {"A", "z", "333"},
		{"A", "x", "4444"}, {"B", "x", "B"}, {"B", "y", "BB"}}
	for _, op := range seq {
		err := a.Put(op.id, op.k, op.v)
		if err == nil {
			ref[op.id][op.k] = op.v // 朴素 map 只在成功时更新
		}
		if err != nil && err != store.ErrQuotaKeys && err != store.ErrQuotaBytes {
			return err
		}
	}
	// 不变量 1/2：每个 Get 与朴素 map 完全一致，两租户同名 key 不同值。
	for id, m := range ref {
		for k, want := range m {
			got, err := a.Get(id, k)
			if err != nil || got != want {
				return must(false, fmt.Sprintf("%s.%s got %q want %q", id, k, got, want))
			}
		}
	}
	if g, _ := a.Get("A", "x"); g != "4444" {
		return must(false, "A.x="+g)
	}
	if g, _ := a.Get("B", "x"); g != "B" {
		return must(false, "B.x="+g)
	}
	// 不变量 3：Usage 等于朴素重算，且不超过配额。
	for id, m := range ref {
		kc, tb, err := a.Usage(id)
		if err != nil {
			return err
		}
		var nBytes int
		for _, v := range m {
			nBytes += len(v)
		}
		if kc != len(m) || tb != nBytes || kc > 2 || tb > 100 {
			return must(false, fmt.Sprintf("usage %s=(%d,%d) ref=(%d,%d)", id, kc, tb, len(m), nBytes))
		}
	}
	// 不变量 4：越权 Purge 被拒且 B 状态不变。
	kc0, tb0, _ := a.Usage("B")
	if err := a.Purge("wrong", "B"); err != store.ErrUnauthorized {
		return must(false, "purge auth")
	}
	kc1, tb1, _ := a.Usage("B")
	if kc0 != kc1 || tb0 != tb1 {
		return must(false, "trace after denied purge")
	}
	return nil
}
