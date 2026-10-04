// Package tier 维护作用域到等级的映射，并从令牌作用域推导命中等级。
package tier

import (
	"errors"
	"strings"
	"sync"
)

// ScopePrefix 是令牌作用域中等级标记的前缀。
const ScopePrefix = "tier:"

var (
	// ErrInvalid 表示 AddTier 参数非法。
	ErrInvalid = errors.New("tier: invalid parameter")
	// ErrDuplicate 表示等级名或 rank 已存在。
	ErrDuplicate = errors.New("tier: duplicate name or rank")
	// ErrEmpty 表示尚未登记任何等级。
	ErrEmpty = errors.New("tier: no tier registered")
)

// Tier 描述一个等级：T 为毫秒/单位，B 为突发单位数。
type Tier struct {
	Name string
	Rank int
	T    int64
	B    int64
}

// Set 是已登记等级的集合，可并发使用。
type Set struct {
	mu     sync.RWMutex
	byName map[string]Tier
	ranks  map[int]struct{}
	min    Tier // rank 最小的等级，仅在 byName 非空时有效
}

// NewSet 返回空的等级集合。
func NewSet() *Set {
	return &Set{
		byName: make(map[string]Tier),
		ranks:  make(map[int]struct{}),
	}
}

// Add 登记一个等级。已登记的等级不可修改或删除。
// 拒绝顺序：参数非法 > 重复（name 或 rank 已存在）。
func (s *Set) Add(name string, rank int, T, B int64) error {
	if name == "" || rank < 1 || rank > 1000 || T < 1 || T > 1_000_000 || B < 1 || B > 1_000_000 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byName[name]; ok {
		return ErrDuplicate
	}
	if _, ok := s.ranks[rank]; ok {
		return ErrDuplicate
	}
	t := Tier{Name: name, Rank: rank, T: T, B: B}
	s.byName[name] = t
	s.ranks[rank] = struct{}{}
	if len(s.byName) == 1 || rank < s.min.Rank {
		s.min = t
	}
	return nil
}

// Derive 按令牌作用域推导等级：取 scopes 中 "tier:" 前缀且已登记者 rank 最大者；
// 一个都没有则取 rank 最小的已登记等级；尚无任何等级则报 ErrEmpty。
// 未登记的等级名被忽略（向前兼容）；scopes 视为集合，重复项无影响。
func (s *Set) Derive(scopes []string) (Tier, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.byName) == 0 {
		return Tier{}, ErrEmpty
	}
	best := Tier{}
	found := false
	for _, sc := range scopes {
		name, ok := strings.CutPrefix(sc, ScopePrefix)
		if !ok {
			continue
		}
		t, ok := s.byName[name]
		if !ok {
			continue
		}
		if !found || t.Rank > best.Rank {
			best, found = t, true
		}
	}
	if !found {
		return s.min, nil
	}
	return best, nil
}
