// Package suppress 维护静默与抑制规则并判定告警可见性。
package suppress

import (
	"errors"

	"ontology/alertstore"
)

var (
	// ErrSilenceExists 表示静默编号冲突。
	ErrSilenceExists = errors.New("silence id already exists")
	// ErrSilenceMissing 表示静默编号不存在。
	ErrSilenceMissing = errors.New("silence id does not exist")
)

// Matcher 是键值相等匹配器，全部键值都相等才命中。
type Matcher map[string]string

// Silence 是一条静默规则。
type Silence struct {
	ID       string
	Matchers Matcher
	Start    int64
	End      int64
}

// InhibitRule 是一条抑制规则。
type InhibitRule struct {
	Source Matcher
	Target Matcher
	Equal  []string
}

// Engine 维护静默与抑制规则。
type Engine struct {
	silences map[string]Silence
	rules    []InhibitRule
}

// New 创建判定引擎。
func New(rules []InhibitRule) *Engine {
	return &Engine{silences: map[string]Silence{}, rules: rules}
}

// AddSilence 添加一条静默。
func (e *Engine) AddSilence(now int64, id string, matchers Matcher, start, end int64) error {
	if _, ok := e.silences[id]; ok {
		return ErrSilenceExists
	}
	e.silences[id] = Silence{ID: id, Matchers: cloneMatcher(matchers), Start: start, End: end}
	return nil
}

// ExpireSilence 提前结束静默。
func (e *Engine) ExpireSilence(now int64, id string) error {
	s, ok := e.silences[id]
	if !ok {
		return ErrSilenceMissing
	}
	if now < s.End {
		s.End = now
		e.silences[id] = s
	}
	return nil
}

// Silenced 判断 labels 在 t 时刻是否被任一半开区间 [start,end) 静默命中。
func (e *Engine) Silenced(t int64, labels alertstore.Labels) bool {
	for _, s := range e.silences {
		if s.Start <= t && t < s.End && match(s.Matchers, labels) {
			return true
		}
	}
	return false
}

// Inhibited 判断目标告警 targetFp 是否被任一 firing 异指纹源告警抑制。
// 源告警自身是否被静默或抑制不影响其资格；判定不递归、不传递。
func (e *Engine) Inhibited(targetFp string, firing map[string]*alertstore.Alert) bool {
	target, ok := firing[targetFp]
	if !ok {
		return false
	}
	for _, rule := range e.rules {
		if !match(rule.Target, target.Labels) {
			continue
		}
		for srcFp, src := range firing {
			if srcFp == targetFp {
				continue
			}
			if !match(rule.Source, src.Labels) {
				continue
			}
			if equalOn(src.Labels, target.Labels, rule.Equal) {
				return true
			}
		}
	}
	return false
}

// Visible 判断 firing 告警在 t 时刻是否可见：未静默且未被抑制。
func (e *Engine) Visible(t int64, fp string, firing map[string]*alertstore.Alert) bool {
	a := firing[fp]
	return !e.Silenced(t, a.Labels) && !e.Inhibited(fp, firing)
}

func match(m Matcher, labels alertstore.Labels) bool {
	for k, v := range m {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func equalOn(a, b alertstore.Labels, names []string) bool {
	for _, name := range names {
		if a[name] != b[name] {
			return false
		}
	}
	return true
}

func cloneMatcher(m Matcher) Matcher {
	c := make(Matcher, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
