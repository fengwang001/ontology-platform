package ontology

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// FallbackPolicy 决定请求未命中健康主机时的全局回退行为。
type FallbackPolicy int

const (
	// FallbackNone 不回退：无匹配选择器或无健康主机直接报错。
	FallbackNone FallbackPolicy = iota
	// FallbackAny 回退到任意健康主机。
	FallbackAny
	// FallbackDefaultSubset 回退到包含全部默认标签的健康主机。
	FallbackDefaultSubset
)

var (
	// ErrInvalidConfig 构造参数非法。
	ErrInvalidConfig = errors.New("invalid selector configuration")
	// ErrEmptyID 主机 id 为空。
	ErrEmptyID = errors.New("host id must not be empty")
	// ErrEmptyLabelKey 标签键为空（AddHost）。
	ErrEmptyLabelKey = errors.New("label key must not be empty")
	// ErrHostExists 同 id 主机已存在。
	ErrHostExists = errors.New("host already exists")
	// ErrHostNotFound SetHealth/RemoveHost 的 id 不存在。
	ErrHostNotFound = errors.New("host not found")
	// ErrInvalidLabels 请求标签含空键（Route）。
	ErrInvalidLabels = errors.New("labels contain empty key")
	// ErrNoMatchingSelector 请求键集合不等于任何选择器且策略为不回退。
	ErrNoMatchingSelector = errors.New("no matching selector")
	// ErrNoHealthyHost 选择器命中但评估全部落空，或回退候选为空。
	ErrNoHealthyHost = errors.New("no healthy host")
)

// SelectorSpec 描述一个选择器：非空标签键集合 Keys 与可选降级键集合 FallbackKeys。
// FallbackKeys 若非空必须是 Keys 的非空真子集；为空表示不降级。
type SelectorSpec struct {
	Keys         []string
	FallbackKeys []string
}

type host struct {
	id      string
	labels  map[string]string
	healthy bool
}

// HostSelector 按标签子集路由、支持恐慌阈值与逐级回退的主机选择器。
type HostSelector struct {
	mu             sync.Mutex
	policy         FallbackPolicy
	panicThreshold int

	// 选择器表：规范化键集合 -> 选择器。
	selectors map[string]selectorEntry
	// 默认标签（已校验，按键规范化为有序列表）。
	defaultKeys   []string
	defaultLabels map[string]string

	// 主机表。
	hosts map[string]*host

	// 各子集计数；子集以“按键排序的 (键,值) 列表”为身份。
	subsetCounters map[string]int64
	// 任意端点回退的全局计数。
	globalCounter int64
	// 默认子集回退计数。
	defaultCounter int64
}

type selectorEntry struct {
	keys         []string
	keysSet      map[string]struct{}
	fallbackKeys []string // 为空表示无 FK
}

// canonicalKey 返回键集合的规范化身份（排序后用 \x00 分隔，空键已在调用前拒绝）。
func canonicalKeys(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\x00")
}

// subsetID 返回“按键排序的 (键,值) 列表”身份。
// 段格式为 <keylen>:<key>=<value>，键顺序固定，因此与计数一一对应且无歧义。
func subsetID(labels map[string]string, keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	var b strings.Builder
	for _, k := range sorted {
		fmt.Fprintf(&b, "%d:%s=%s\n", len(k), k, labels[k])
	}
	return b.String()
}

// hasEmptyKey 检查标签中是否含空键。
func hasEmptyKey(labels map[string]string) bool {
	for k := range labels {
		if k == "" {
			return true
		}
	}
	return false
}

// copyLabels 复制标签，避免外部修改影响内部状态。
func copyLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// NewHostSelector 构造选择器。
func NewHostSelector(selectors []SelectorSpec, policy FallbackPolicy, defaultLabels map[string]string, panicThreshold int) (*HostSelector, error) {
	if panicThreshold < 0 || panicThreshold > 100 {
		return nil, ErrInvalidConfig
	}
	switch policy {
	case FallbackNone, FallbackAny, FallbackDefaultSubset:
	default:
		return nil, ErrInvalidConfig
	}
	if len(selectors) == 0 {
		return nil, ErrInvalidConfig
	}

	s := &HostSelector{
		policy:         policy,
		panicThreshold: panicThreshold,
		selectors:      make(map[string]selectorEntry),
		hosts:          make(map[string]*host),
		subsetCounters: make(map[string]int64),
	}

	for _, spec := range selectors {
		if len(spec.Keys) == 0 {
			return nil, ErrInvalidConfig
		}
		keySet := make(map[string]struct{}, len(spec.Keys))
		for _, k := range spec.Keys {
			if k == "" {
				return nil, ErrInvalidConfig
			}
			if _, dup := keySet[k]; dup {
				return nil, ErrInvalidConfig
			}
			keySet[k] = struct{}{}
		}
		id := canonicalKeys(spec.Keys)
		if _, exists := s.selectors[id]; exists {
			return nil, ErrInvalidConfig
		}

		var fk []string
		if len(spec.FallbackKeys) > 0 {
			fkSet := make(map[string]struct{}, len(spec.FallbackKeys))
			for _, k := range spec.FallbackKeys {
				if k == "" {
					return nil, ErrInvalidConfig
				}
				if _, dup := fkSet[k]; dup {
					return nil, ErrInvalidConfig
				}
				if _, ok := keySet[k]; !ok {
					return nil, ErrInvalidConfig
				}
				fkSet[k] = struct{}{}
			}
			// FK 必须是真子集：非空且不等于键集合。
			if len(fkSet) >= len(keySet) {
				return nil, ErrInvalidConfig
			}
			fk = append([]string(nil), spec.FallbackKeys...)
			sort.Strings(fk)
		}

		keys := append([]string(nil), spec.Keys...)
		sort.Strings(keys)
		s.selectors[id] = selectorEntry{keys: keys, keysSet: keySet, fallbackKeys: fk}
	}

	if policy == FallbackDefaultSubset {
		if len(defaultLabels) == 0 || hasEmptyKey(defaultLabels) {
			return nil, ErrInvalidConfig
		}
		s.defaultLabels = copyLabels(defaultLabels)
		for k := range s.defaultLabels {
			s.defaultKeys = append(s.defaultKeys, k)
		}
		sort.Strings(s.defaultKeys)
	}

	return s, nil
}

// AddHost 登记主机。
func (s *HostSelector) AddHost(id string, labels map[string]string, healthy bool) error {
	if id == "" || hasEmptyKey(labels) {
		if id == "" {
			return ErrEmptyID
		}
		return ErrEmptyLabelKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.hosts[id]; exists {
		return ErrHostExists
	}
	s.hosts[id] = &host{id: id, labels: copyLabels(labels), healthy: healthy}
	return nil
}

// SetHealth 变更主机健康状态。
func (s *HostSelector) SetHealth(id string, healthy bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hosts[id]
	if !ok {
		return ErrHostNotFound
	}
	h.healthy = healthy
	return nil
}

// RemoveHost 移除主机。
func (s *HostSelector) RemoveHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hosts[id]; !ok {
		return ErrHostNotFound
	}
	delete(s.hosts, id)
	return nil
}

// Route 按请求标签选择一个主机 id。
func (s *HostSelector) Route(labels map[string]string) (string, error) {
	if hasEmptyKey(labels) {
		return "", ErrInvalidLabels
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	reqKeys := make([]string, 0, len(labels))
	for k := range labels {
		reqKeys = append(reqKeys, k)
	}

	if len(reqKeys) > 0 {
		if sel, ok := s.selectors[canonicalKeys(reqKeys)]; ok {
			// 直接评估 (K, labels)。
			if picked, ok := s.evaluate(labels, sel.keys); ok {
				return picked, nil
			}
			// 降键评估，仅一级、不递归。
			if len(sel.fallbackKeys) > 0 {
				restricted := restrictLabels(labels, sel.fallbackKeys)
				if picked, ok := s.evaluate(restricted, sel.fallbackKeys); ok {
					return picked, nil
				}
			}
			// 全部落空：全局回退；不回退时报“无健康主机”。
			return s.globalFallback(labels, false)
		}
	}

	// K 为空或不与任何选择器键集合相等：全局回退；不回退时报“无匹配选择器”。
	return s.globalFallback(labels, true)
}

// evaluate 对给定键集合与标签执行一次子集评估；成功选中时推进该子集计数。
// 调用方必须持有 s.mu。
func (s *HostSelector) evaluate(labels map[string]string, keys []string) (string, bool) {
	var all, healthy []*host
	for _, h := range s.hosts {
		if hostMatchesAll(h, labels, keys) {
			all = append(all, h)
			if h.healthy {
				healthy = append(healthy, h)
			}
		}
	}
	if len(all) == 0 {
		return "", false
	}
	sort.Slice(all, func(i, j int) bool { return all[i].id < all[j].id })
	sort.Slice(healthy, func(i, j int) bool { return healthy[i].id < healthy[j].id })

	id := subsetID(labels, keys)
	c := s.subsetCounters[id]

	var picked string
	// 恐慌：|H|*100 < Pt*|A|。
	if len(healthy)*100 < s.panicThreshold*len(all) {
		picked = all[c%int64(len(all))].id
	} else if len(healthy) > 0 {
		picked = healthy[c%int64(len(healthy))].id
	} else {
		return "", false
	}
	s.subsetCounters[id] = c + 1
	return picked, true
}

// hostMatchesAll 判断主机是否对 keys 中每个键的标签值都与 labels 精确相等。
func hostMatchesAll(h *host, labels map[string]string, keys []string) bool {
	for _, k := range keys {
		v, ok := h.labels[k]
		if !ok || v != labels[k] {
			return false
		}
	}
	return true
}

// restrictLabels 把 labels 限制到 keys 指定的键（取交集内容）。
func restrictLabels(labels map[string]string, keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := labels[k]; ok {
			out[k] = v
		}
	}
	return out
}

// globalFallback 执行全局回退。不适用恐慌阈值。
// noSelectorMatched=true 表示 K 为空或无选择器键集合相等（不回退时报 ErrNoMatchingSelector）；
// 否则为选择器命中但评估全部落空（报 ErrNoHealthyHost）。
// 调用方必须持有 s.mu。
func (s *HostSelector) globalFallback(labels map[string]string, noSelectorMatched bool) (string, error) {
	switch s.policy {
	case FallbackNone:
		if noSelectorMatched {
			return "", ErrNoMatchingSelector
		}
		return "", ErrNoHealthyHost
	case FallbackAny:
		candidates := s.healthyHosts(nil)
		if len(candidates) == 0 {
			return "", ErrNoHealthyHost
		}
		picked := candidates[s.globalCounter%int64(len(candidates))].id
		s.globalCounter++
		return picked, nil
	case FallbackDefaultSubset:
		candidates := s.healthyHosts(func(h *host) bool {
			return hostMatchesAll(h, s.defaultLabels, s.defaultKeys)
		})
		if len(candidates) == 0 {
			return "", ErrNoHealthyHost
		}
		picked := candidates[s.defaultCounter%int64(len(candidates))].id
		s.defaultCounter++
		return picked, nil
	default:
		return "", ErrInvalidConfig
	}
}

// healthyHosts 返回按 id 升序的健康主机；extra 非空时还需满足额外过滤。
// 调用方必须持有 s.mu。
func (s *HostSelector) healthyHosts(extra func(*host) bool) []*host {
	var out []*host
	for _, h := range s.hosts {
		if !h.healthy {
			continue
		}
		if extra != nil && !extra(h) {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
