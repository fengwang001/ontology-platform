// Package suppress 判定告警可见性：静默（半开时间窗内命中即隐藏）与
// 抑制（非传递、源告警自身可见性不影响资格）。本包不做并发控制。
package suppress

import (
	"errors"
	"fmt"

	"ontology/alertstore"
)

// 状态类错误。
var (
	ErrConflict = errors.New("suppress: silence id already exists")
	ErrNotFound = errors.New("suppress: silence id not found")
)

// Matchers 是 1 到 8 对键=值，全部相等才命中。
type Matchers map[string]string

// CheckMatchers 校验匹配器约束（键非空且不超过 64 字节，值不超过 64 字节）。
func CheckMatchers(m Matchers) error {
	if len(m) < 1 || len(m) > 8 {
		return fmt.Errorf("matchers count %d out of [1,8]", len(m))
	}
	for k, v := range m {
		if k == "" || len(k) > 64 {
			return fmt.Errorf("bad matcher key %q", k)
		}
		if len(v) > 64 {
			return fmt.Errorf("bad matcher value for key %q", k)
		}
	}
	return nil
}

// Matches 报告标签集是否命中该匹配器（全部键值相等）。
func (m Matchers) Matches(l alertstore.Labels) bool {
	for k, v := range m {
		if l[k] != v {
			return false
		}
	}
	return true
}

// Silence 是一条静默规则，在 [Start, End) 内对命中的告警生效。
type Silence struct {
	ID       string
	Matchers Matchers
	Start    int64
	End      int64
}

// Silencer 管理静默集合。
type Silencer struct {
	silences map[string]*Silence
}

// NewSilencer 创建空 Silencer。
func NewSilencer() *Silencer { return &Silencer{silences: make(map[string]*Silence)} }

// Add 新增静默；编号重复返回 ErrConflict。
func (s *Silencer) Add(id string, m Matchers, start, end int64) error {
	if _, ok := s.silences[id]; ok {
		return ErrConflict
	}
	s.silences[id] = &Silence{ID: id, Matchers: m, Start: start, End: end}
	return nil
}

// Expire 把编号为 id 的静默提前结束为 min(end, now)；不存在返回 ErrNotFound。
func (s *Silencer) Expire(now int64, id string) error {
	si, ok := s.silences[id]
	if !ok {
		return ErrNotFound
	}
	if now < si.End {
		si.End = now
	}
	return nil
}

// Silenced 报告标签集在时刻 t 是否被任一静默覆盖（start <= t < end）。
func (s *Silencer) Silenced(l alertstore.Labels, t int64) bool {
	for _, si := range s.silences {
		if si.Start <= t && t < si.End && si.Matchers.Matches(l) {
			return true
		}
	}
	return false
}

// Rule 是一条抑制规则：源匹配器命中的 firing 告警抑制目标匹配器命中的告警，
// 且 equal 中每个名字上两者取值相同（都缺失算相同）。
type Rule struct {
	Source Matchers
	Target Matchers
	Equal  []string
}

// CheckRule 校验抑制规则约束。
func CheckRule(r Rule) error {
	if err := CheckMatchers(r.Source); err != nil {
		return fmt.Errorf("source: %v", err)
	}
	if err := CheckMatchers(r.Target); err != nil {
		return fmt.Errorf("target: %v", err)
	}
	for _, name := range r.Equal {
		if name == "" || len(name) > 64 {
			return fmt.Errorf("bad equal name %q", name)
		}
	}
	return nil
}

// Inhibitor 按规则列表判定抑制，不传递、不递归。
type Inhibitor struct {
	rules []Rule
}

// NewInhibitor 创建 Inhibitor（规则须先通过 CheckRule）。
func NewInhibitor(rules []Rule) *Inhibitor {
	cp := make([]Rule, len(rules))
	copy(cp, rules)
	return &Inhibitor{rules: cp}
}

// Suppressed 报告 target 是否被 firing 中任一不同指纹的告警抑制。
// 源告警自身是否被静默或抑制不影响其资格。
func (in *Inhibitor) Suppressed(target *alertstore.Alert, firing []*alertstore.Alert) bool {
	for _, r := range in.rules {
		if !r.Target.Matches(target.Labels) {
			continue
		}
		for _, src := range firing {
			if src.Fp == target.Fp || !r.Source.Matches(src.Labels) {
				continue
			}
			ok := true
			for _, name := range r.Equal {
				if src.Labels[name] != target.Labels[name] {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}
