// Package placement 实现带必需亲和（含最少个数）、必需反亲和、
// 已有实例对称排斥与预留可见性的 Pod 放置过滤器。
package placement

import (
	"sort"
)

// Selector 是标签选择器：当且仅当目标标签集合包含其全部键值对时匹配。
// 空选择器匹配所有 Pod。
type Selector map[string]string

// AffinityTerm 是一条必需亲和项。
type AffinityTerm struct {
	Selector Selector
	Topology TopologyKey
	// MinRequired 为同域内至少需要匹配的已放置 Pod 个数，范围 1..100。
	MinRequired int
}

// AntiAffinityTerm 是一条必需反亲和项。
type AntiAffinityTerm struct {
	Selector Selector
	Topology TopologyKey
}

// TopologyKey 只有两种取值：TopologyNode 与 TopologyZone。
type TopologyKey string

const (
	// TopologyNode 表示拓扑域为节点名。
	TopologyNode TopologyKey = "node"
	// TopologyZone 表示拓扑域为节点所在可用区。
	TopologyZone TopologyKey = "zone"
)

// Pod 描述一个待放置或已放置的 Pod。
type Pod struct {
	ID           string
	Labels       map[string]string
	Affinity     []AffinityTerm
	AntiAffinity []AntiAffinityTerm
}

// Node 描述一个登记节点。
type Node struct {
	Name string
	Zone string
}

// Filter 是并发安全的 Pod 放置过滤器。
// 所有方法均通过同一把互斥锁串行化，因此并发调用的结果
// 等价于某个串行执行顺序。
type Filter struct {
	mu filterMu
}

// NewFilter 创建过滤器，Q 为预留中 Pod 数量的上限（1..1000）。
func NewFilter(Q int) *Filter {
	if Q < 1 || Q > 1000 {
		panic("placement: Q must be in [1,1000]")
	}
	f := &Filter{}
	f.mu.Q = Q
	f.mu.nodes = map[string]string{}
	f.mu.pods = map[string]*placedPod{}
	f.mu.reserved = map[string]struct{}{}
	return f
}

// AddNode 登记一个节点。
func (f *Filter) AddNode(n Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n.Name == "" {
		return errInvalid("node name must not be empty")
	}
	if n.Zone == "" {
		return errInvalid("zone must not be empty")
	}
	if _, ok := f.mu.nodes[n.Name]; ok {
		return &PlacementError{Reason: ReasonNodeExists, Detail: "node already exists", TermIndex: -1}
	}
	f.mu.nodes[n.Name] = n.Zone
	return nil
}

// RemoveNode 删除没有任何已放置 Pod 的节点。
func (f *Filter) RemoveNode(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "" {
		return errInvalid("node name must not be empty")
	}
	if _, ok := f.mu.nodes[name]; !ok {
		return &PlacementError{Reason: ReasonNodeNotFound, Detail: "node not found", TermIndex: -1}
	}
	for _, p := range f.mu.pods {
		if p.node == name {
			return &PlacementError{Reason: ReasonNodeInUse, Detail: "node still hosts placed pods", TermIndex: -1}
		}
	}
	delete(f.mu.nodes, name)
	return nil
}

// Reserve 判定通过后在节点 n 上预留 Pod x。
func (f *Filter) Reserve(x Pod, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := validatePod(x); e != nil {
		return e
	}
	if _, ok := f.mu.pods[x.ID]; ok {
		return &PlacementError{Reason: ReasonPodExists, Detail: "pod already exists", TermIndex: -1}
	}
	if _, ok := f.mu.nodes[n]; !ok {
		return &PlacementError{Reason: ReasonNodeNotFound, Detail: "node not found", TermIndex: -1}
	}
	if e := f.checkPlacement(x, n); e != nil {
		return e
	}
	// 预留额度最后判定。
	if len(f.mu.reserved) >= f.mu.Q {
		return &PlacementError{Reason: ReasonReservationFull, Detail: "reservation quota full", TermIndex: -1}
	}
	f.storePod(x, n, true)
	return nil
}

// Commit 把预留转为已提交，不再校验。
func (f *Filter) Commit(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" {
		return errInvalid("pod id must not be empty")
	}
	p, ok := f.mu.pods[id]
	if !ok {
		return &PlacementError{Reason: ReasonPodNotFound, Detail: "pod not found", TermIndex: -1}
	}
	if !p.reserved {
		return &PlacementError{Reason: ReasonNotReserved, Detail: "pod is not reserved", TermIndex: -1}
	}
	p.reserved = false
	delete(f.mu.reserved, id)
	return nil
}

// Cancel 删除一个预留。
func (f *Filter) Cancel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" {
		return errInvalid("pod id must not be empty")
	}
	p, ok := f.mu.pods[id]
	if !ok {
		return &PlacementError{Reason: ReasonPodNotFound, Detail: "pod not found", TermIndex: -1}
	}
	if !p.reserved {
		return &PlacementError{Reason: ReasonNotReserved, Detail: "pod is not reserved", TermIndex: -1}
	}
	delete(f.mu.pods, id)
	delete(f.mu.reserved, id)
	return nil
}

// Place 判定通过后直接提交 Pod，不占用预留额度。
func (f *Filter) Place(x Pod, n string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := validatePod(x); e != nil {
		return e
	}
	if _, ok := f.mu.pods[x.ID]; ok {
		return &PlacementError{Reason: ReasonPodExists, Detail: "pod already exists", TermIndex: -1}
	}
	if _, ok := f.mu.nodes[n]; !ok {
		return &PlacementError{Reason: ReasonNodeNotFound, Detail: "node not found", TermIndex: -1}
	}
	if e := f.checkPlacement(x, n); e != nil {
		return e
	}
	f.storePod(x, n, false)
	return nil
}

// Remove 只删除已提交的 Pod。
func (f *Filter) Remove(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" {
		return errInvalid("pod id must not be empty")
	}
	p, ok := f.mu.pods[id]
	if !ok {
		return &PlacementError{Reason: ReasonPodNotFound, Detail: "pod not found", TermIndex: -1}
	}
	if p.reserved {
		return &PlacementError{Reason: ReasonStillReserved, Detail: "pod is still reserved", TermIndex: -1}
	}
	delete(f.mu.pods, id)
	return nil
}

// Relabel 替换已放置 Pod 的标签。
func (f *Filter) Relabel(id string, labels map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id == "" {
		return errInvalid("pod id must not be empty")
	}
	if e := validateLabels(labels); e != nil {
		return e
	}
	p, ok := f.mu.pods[id]
	if !ok {
		return &PlacementError{Reason: ReasonPodNotFound, Detail: "pod not found", TermIndex: -1}
	}
	newLabels := cloneLabels(labels)
	if blocker := f.checkRelabel(p, newLabels); blocker != "" {
		return &PlacementError{
			Reason:    ReasonRejectedByExistingPod,
			Detail:    "relabel rejected by existing pod's anti-affinity",
			TermIndex: -1,
			BlockerID: blocker,
		}
	}
	p.pod.Labels = newLabels
	return nil
}

// Feasible 返回可放置 x 的全部节点名（字节序升序），不改变状态。
func (f *Filter) Feasible(x Pod) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := validatePod(x); e != nil {
		return nil, e
	}
	if _, ok := f.mu.pods[x.ID]; ok {
		return nil, &PlacementError{Reason: ReasonPodExists, Detail: "pod already exists", TermIndex: -1}
	}
	names := make([]string, 0, len(f.mu.nodes))
	for name := range f.mu.nodes {
		if f.checkPlacement(x, name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// storePod 在所有校验通过后登记一个已放置 Pod，深拷贝调用方数据。
func (f *Filter) storePod(x Pod, node string, reserved bool) {
	stored := Pod{
		ID:           x.ID,
		Labels:       cloneLabels(x.Labels),
		Affinity:     make([]AffinityTerm, len(x.Affinity)),
		AntiAffinity: make([]AntiAffinityTerm, len(x.AntiAffinity)),
	}
	for i, term := range x.Affinity {
		stored.Affinity[i] = AffinityTerm{
			Selector:    cloneSelector(term.Selector),
			Topology:    term.Topology,
			MinRequired: term.MinRequired,
		}
	}
	for i, term := range x.AntiAffinity {
		stored.AntiAffinity[i] = AntiAffinityTerm{
			Selector: cloneSelector(term.Selector),
			Topology: term.Topology,
		}
	}
	f.mu.pods[x.ID] = &placedPod{pod: stored, node: node, reserved: reserved}
	if reserved {
		f.mu.reserved[x.ID] = struct{}{}
	}
}

// cloneSelector 复制选择器映射。
func cloneSelector(s Selector) Selector {
	out := make(Selector, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}
