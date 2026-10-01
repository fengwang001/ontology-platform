// Package ontology 提供按标签子集路由、带恐慌阈值、降键回退与全局回退策略的主机选择器。
package ontology

import "sync"

// 恐慌阈值的合法范围（百分数，闭区间）。
const (
	minPanicThreshold = 0
	maxPanicThreshold = 100
)

// FallbackPolicy 是全局回退策略。
type FallbackPolicy int

const (
	// FallbackNone 不回退。
	FallbackNone FallbackPolicy = iota
	// FallbackAnyEndpoint 回退到任意健康端点。
	FallbackAnyEndpoint
	// FallbackDefaultSubset 回退到默认标签子集。
	FallbackDefaultSubset
)

// SelectorSpec 描述一个选择器：键集合 KeySet 与可选降级键集合 FallbackKeys。
type SelectorSpec struct {
	KeySet       map[string]struct{}
	FallbackKeys map[string]struct{}
}

type host struct {
	id      string
	labels  map[string]string
	healthy bool
}

// HostSelector 是并发安全的标签子集主机选择器。
type HostSelector struct {
	mu             sync.Mutex
	selectors      []selector
	byKeySet       map[string]int
	policy         FallbackPolicy
	defaultLabels  map[string]string
	panicThreshold int
	hosts          map[string]*host
	globalCount    int
	defaultCount   int
	subsetCounts   map[string]int
}

type selector struct {
	keySet      map[string]struct{}
	keys        []string
	fallback    []string
	hasFallback bool
}

// 可区分的哨兵错误。
var (
	ErrInvalidConfig     = configError("invalid selector configuration")
	ErrUnsupportedPolicy = configError("unsupported fallback policy")
	ErrInvalidLabel      = labelError("labels contain empty key")
	ErrEmptyID           = idError("host id is empty")
	ErrDuplicateHost     = idError("host id already exists")
	ErrHostNotFound      = idError("host not found")
	ErrNoSelector        = routeError("no matching selector")
	ErrNoHealthyHost     = routeError("no healthy host")
)

type configError string

func (e configError) Error() string { return string(e) }

type labelError string

func (e labelError) Error() string { return string(e) }

type idError string

func (e idError) Error() string { return string(e) }

type routeError string

func (e routeError) Error() string { return string(e) }

// NewHostSelector 构造选择器。配置非法时整体拒绝（返回 ErrInvalidConfig 或 ErrUnsupportedPolicy）。
func NewHostSelector(selectors []SelectorSpec, policy FallbackPolicy, defaultLabels map[string]string, panicThreshold int) (*HostSelector, error) {
	if panicThreshold < minPanicThreshold || panicThreshold > maxPanicThreshold {
		return nil, ErrInvalidConfig
	}
	switch policy {
	case FallbackNone, FallbackAnyEndpoint, FallbackDefaultSubset:
	default:
		return nil, ErrUnsupportedPolicy
	}
	defaultCopy := make(map[string]string, len(defaultLabels))
	for k, v := range defaultLabels {
		defaultCopy[k] = v
	}
	if policy == FallbackDefaultSubset {
		if len(defaultCopy) == 0 {
			return nil, ErrInvalidConfig
		}
		for k := range defaultCopy {
			if k == "" {
				return nil, ErrInvalidConfig
			}
		}
	}
	if len(selectors) == 0 {
		return nil, ErrInvalidConfig
	}
	sels := make([]selector, 0, len(selectors))
	byKeySet := make(map[string]int, len(selectors))
	for _, spec := range selectors {
		sel, err := buildSelector(spec)
		if err != nil {
			return nil, err
		}
		signature := subsetKey(sel.keys)
		if _, exists := byKeySet[signature]; exists {
			return nil, ErrInvalidConfig
		}
		byKeySet[signature] = len(sels)
		sels = append(sels, sel)
	}
	return &HostSelector{
		selectors:      sels,
		byKeySet:       byKeySet,
		policy:         policy,
		defaultLabels:  defaultCopy,
		panicThreshold: panicThreshold,
		hosts:          make(map[string]*host),
		subsetCounts:   make(map[string]int),
	}, nil
}

func buildSelector(spec SelectorSpec) (selector, error) {
	if spec.KeySet == nil || len(spec.KeySet) == 0 {
		return selector{}, ErrInvalidConfig
	}
	keySet := make(map[string]struct{}, len(spec.KeySet))
	keys := make([]string, 0, len(spec.KeySet))
	for k := range spec.KeySet {
		if k == "" {
			return selector{}, ErrInvalidConfig
		}
		keySet[k] = struct{}{}
		keys = append(keys, k)
	}
	sortStrings(keys)
	sel := selector{keySet: keySet, keys: keys}
	if spec.FallbackKeys != nil {
		if len(spec.FallbackKeys) == 0 || len(spec.FallbackKeys) >= len(keySet) {
			return selector{}, ErrInvalidConfig
		}
		fallback := make([]string, 0, len(spec.FallbackKeys))
		for k := range spec.FallbackKeys {
			if k == "" {
				return selector{}, ErrInvalidConfig
			}
			if _, ok := keySet[k]; !ok {
				return selector{}, ErrInvalidConfig
			}
			fallback = append(fallback, k)
		}
		sortStrings(fallback)
		sel.fallback = fallback
		sel.hasFallback = true
	}
	return sel, nil
}

// sortStrings 按键的字典序升序排序，避免在顶部引入不必要的依赖差异。
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

// subsetKey 仅由有序键构成，用于判定键集合是否相等。
func subsetKey(keys []string) string {
	var b []byte
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, 0)
	}
	return string(b)
}

func canonicalSubsetForKeys(keys []string, labels map[string]string) string {
	var b []byte
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, 0)
		b = append(b, labels[k]...)
		b = append(b, 0)
	}
	return string(b)
}

func hasEmptyKey(labels map[string]string) bool {
	for k := range labels {
		if k == "" {
			return true
		}
	}
	return false
}

// AddHost 登记一台主机。
func (s *HostSelector) AddHost(id string, labels map[string]string, healthy bool) error {
	if id == "" {
		return ErrEmptyID
	}
	if hasEmptyKey(labels) {
		return ErrInvalidLabel
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.hosts[id]; exists {
		return ErrDuplicateHost
	}
	labelsCopy := make(map[string]string, len(labels))
	for k, v := range labels {
		labelsCopy[k] = v
	}
	s.hosts[id] = &host{id: id, labels: labelsCopy, healthy: healthy}
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

// Route 按标签选择主机；只有成功选出主机时推进对应计数。
func (s *HostSelector) Route(labels map[string]string) (string, error) {
	if hasEmptyKey(labels) {
		return "", ErrInvalidLabel
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sortStrings(keys)
	s.mu.Lock()
	defer s.mu.Unlock()

	matchedSelector := false
	if len(keys) > 0 {
		if idx, ok := s.byKeySet[subsetKey(keys)]; ok {
			matchedSelector = true
			sel := s.selectors[idx]
			if id, ok := s.evaluate(sel.keys, labels); ok {
				return id, nil
			}
			if sel.hasFallback {
				if id, ok := s.evaluate(sel.fallback, labels); ok {
					return id, nil
				}
			}
		}
	}

	switch s.policy {
	case FallbackNone:
		if matchedSelector {
			return "", ErrNoHealthyHost
		}
		return "", ErrNoSelector
	case FallbackAnyEndpoint:
		candidates := s.sortedHosts(func(h *host) bool { return h.healthy })
		if len(candidates) == 0 {
			return "", ErrNoHealthyHost
		}
		id := candidates[s.globalCount%len(candidates)].id
		s.globalCount++
		return id, nil
	case FallbackDefaultSubset:
		candidates := s.sortedHosts(func(h *host) bool {
			if !h.healthy {
				return false
			}
			for k, v := range s.defaultLabels {
				if hv, ok := h.labels[k]; !ok || hv != v {
					return false
				}
			}
			return true
		})
		if len(candidates) == 0 {
			return "", ErrNoHealthyHost
		}
		id := candidates[s.defaultCount%len(candidates)].id
		s.defaultCount++
		return id, nil
	default:
		return "", ErrUnsupportedPolicy
	}
}

// evaluate 对给定有序键集合与标签执行一次子集评估；成功时推进该子集计数。
func (s *HostSelector) evaluate(keys []string, labels map[string]string) (string, bool) {
	all := s.sortedHosts(func(h *host) bool {
		for _, k := range keys {
			hv, ok := h.labels[k]
			if !ok || hv != labels[k] {
				return false
			}
		}
		return true
	})
	if len(all) == 0 {
		return "", false
	}
	healthy := make([]*host, 0, len(all))
	for _, h := range all {
		if h.healthy {
			healthy = append(healthy, h)
		}
	}
	subset := canonicalSubsetForKeys(keys, labels)
	c := s.subsetCounts[subset]
	if len(healthy)*100 < s.panicThreshold*len(all) {
		picked := all[c%len(all)]
		s.subsetCounts[subset] = c + 1
		return picked.id, true
	}
	if len(healthy) == 0 {
		return "", false
	}
	picked := healthy[c%len(healthy)]
	s.subsetCounts[subset] = c + 1
	return picked.id, true
}

func (s *HostSelector) sortedHosts(match func(*host) bool) []*host {
	result := make([]*host, 0)
	for _, h := range s.hosts {
		if match(h) {
			result = append(result, h)
		}
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j-1].id > result[j].id; j-- {
			result[j-1], result[j] = result[j], result[j-1]
		}
	}
	return result
}

// SubsetCount 返回由 labels 的全部 (键, 值) 标识的子集当前轮转计数，仅供观测与测试。
func (s *HostSelector) SubsetCount(labels map[string]string) int {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sortStrings(keys)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subsetCounts[canonicalSubsetForKeys(keys, labels)]
}

// GlobalCount 返回任意端点回退的全局计数，仅供观测与测试。
func (s *HostSelector) GlobalCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.globalCount
}

// DefaultCount 返回默认子集回退的计数，仅供观测与测试。
func (s *HostSelector) DefaultCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.defaultCount
}

// HostCount 返回当前登记主机数，仅供观测与测试。
func (s *HostSelector) HostCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hosts)
}
