// Package tier 维护等级注册表，并从令牌作用域推导生效等级。
package tier

import (
	"errors"
	"strings"
)

var (
	// ErrInvalidArgument 参数非法（name 为空、rank 越界、T/B 越界）。
	ErrInvalidArgument = errors.New("tier: 参数非法")
	// ErrDuplicate name 或 rank 已存在。
	ErrDuplicate = errors.New("tier: 名称或等级已存在")
	// ErrNoTiers 尚未登记任何等级。
	ErrNoTiers = errors.New("tier: 尚无任何等级")
)

// ScopePrefix 是令牌作用域中等级标记的前缀。
const ScopePrefix = "tier:"

const (
	maxRank  = 1000
	maxParam = 1_000_000
)

// Tier 描述一个等级：T 为毫秒/单位，B 为突发单位数。
type Tier struct {
	Name string
	Rank int
	T    int64
	B    int64
}

// Registry 是等级注册表。已登记的等级不可修改或删除。
// Registry 本身不加锁，并发安全由调用方（ratelimit.Limiter）保证。
type Registry struct {
	byName map[string]Tier
	byRank map[int]string
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Tier{}, byRank: map[int]string{}}
}

// Add 登记一个等级。拒绝顺序：参数非法 > 重复（name 或 rank 已存在）。
func (r *Registry) Add(name string, rank int, t, b int64) error {
	if name == "" || rank < 1 || rank > maxRank ||
		t < 1 || t > maxParam || b < 1 || b > maxParam {
		return ErrInvalidArgument
	}
	if _, ok := r.byName[name]; ok {
		return ErrDuplicate
	}
	if _, ok := r.byRank[rank]; ok {
		return ErrDuplicate
	}
	r.byName[name] = Tier{Name: name, Rank: rank, T: t, B: b}
	r.byRank[rank] = name
	return nil
}

// Empty 报告是否尚无任何等级。
func (r *Registry) Empty() bool {
	return len(r.byName) == 0
}

// Derive 从令牌作用域推导生效等级：
// 以 "tier:" 开头者去掉前缀得等级名，忽略未登记者，取 rank 最大者；
// 一个都没有则取 rank 最小的已登记等级；尚无任何等级则报 ErrNoTiers。
// scopes 视为集合，重复项无影响。
func (r *Registry) Derive(scopes []string) (Tier, error) {
	if r.Empty() {
		return Tier{}, ErrNoTiers
	}
	best, found := Tier{}, false
	for _, s := range scopes {
		if !strings.HasPrefix(s, ScopePrefix) {
			continue
		}
		tr, ok := r.byName[s[len(ScopePrefix):]]
		if !ok {
			continue // 未登记的等级名：忽略，向前兼容
		}
		if !found || tr.Rank > best.Rank {
			best, found = tr, true
		}
	}
	if found {
		return best, nil
	}
	min, haveMin := Tier{}, false
	for _, tr := range r.byName {
		if !haveMin || tr.Rank < min.Rank {
			min, haveMin = tr, true
		}
	}
	return min, nil
}
