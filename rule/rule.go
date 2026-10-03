// Package rule 定义生命周期规则及其匹配（最长前缀优先，并列取 id 最小）。
package rule

import (
	"errors"
	"fmt"
	"strings"
)

// Kind 是规则类型。
type Kind int

const (
	// Expire 当前数据版本到期则追加删除标记。
	Expire Kind = iota
	// NoncurrentExpire 非当前数据版本自成为非当前起 days 天后到期。
	NoncurrentExpire
	// OrphanMarker 键只剩一个删除标记时永久删除之。
	OrphanMarker
)

func (k Kind) String() string {
	switch k {
	case Expire:
		return "Expire"
	case NoncurrentExpire:
		return "NoncurrentExpire"
	case OrphanMarker:
		return "OrphanMarker"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

const (
	MinDays = 1
	MaxDays = 3650
	MaxKeep = 1000
)

var ErrInvalid = errors.New("rule: invalid rule")

// Rule 生命周期规则，仅对键以 Prefix 开头的键生效。
type Rule struct {
	ID     string
	Prefix string
	Kind   Kind
	Days   int
	Keep   int
}

func (r Rule) String() string {
	return fmt.Sprintf("{id:%q prefix:%q kind:%v days:%d keep:%d}",
		r.ID, r.Prefix, r.Kind, r.Days, r.Keep)
}

// Set 是一组构造时校验过的规则。
type Set struct {
	rules []Rule
}

// NewSet 校验并构造规则集：id 非空且不重复，kind 合法，
// days 在 [1,3650]，keep 在 [0,1000]。
func NewSet(rs ...Rule) (*Set, error) {
	seen := make(map[string]bool, len(rs))
	for _, r := range rs {
		if r.ID == "" {
			return nil, fmt.Errorf("%w: empty id", ErrInvalid)
		}
		if r.Kind < Expire || r.Kind > OrphanMarker {
			return nil, fmt.Errorf("%w: rule %q bad kind %d", ErrInvalid, r.ID, int(r.Kind))
		}
		if r.Days < MinDays || r.Days > MaxDays {
			return nil, fmt.Errorf("%w: rule %q days %d out of range", ErrInvalid, r.ID, r.Days)
		}
		if r.Keep < 0 || r.Keep > MaxKeep {
			return nil, fmt.Errorf("%w: rule %q keep %d out of range", ErrInvalid, r.ID, r.Keep)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("%w: duplicate id %q", ErrInvalid, r.ID)
		}
		seen[r.ID] = true
	}
	return &Set{rules: append([]Rule(nil), rs...)}, nil
}

// Rules 返回规则副本，供日志与测试使用。
func (s *Set) Rules() []Rule { return append([]Rule(nil), s.rules...) }

// Match 返回对 key 生效的指定 kind 的唯一规则：匹配前缀最长者，
// 并列取 id 字节序最小者。
func (s *Set) Match(key string, k Kind) (Rule, bool) {
	var best Rule
	found := false
	for _, r := range s.rules {
		if r.Kind != k || !strings.HasPrefix(key, r.Prefix) {
			continue
		}
		if !found || len(r.Prefix) > len(best.Prefix) ||
			(len(r.Prefix) == len(best.Prefix) && r.ID < best.ID) {
			best, found = r, true
		}
	}
	return best, found
}
