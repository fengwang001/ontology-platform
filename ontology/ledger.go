package ontology

import (
	"fmt"
	"sync"
)

// 基本领域标识符。
type (
	ObjectTypeName string
	LinkTypeName   string
	InstanceID     string
)

// BoundKind 描述一端基数上限的形态。
type BoundKind int

const (
	BoundUnlimited BoundKind = iota
	BoundExactlyOne
	BoundAtMostOne
	BoundAtMost
)

// Bound 是链接一端声明的基数上限。
//
// 上限的语义只与一个数字有关：ExactlyOne 与 AtMostOne 都是 1；
// AtMost(n) 是给定正整数 n；零值 Bound{}（Unlimited）表示无限制。
// ExactlyOne 相对 AtMostOne 的差异在于「恰好一」是一种更强的存在性约束，
// 当前账本只负责创建期的「上限」校验，下限（必须存在）不在本系统的校验范围内。
type Bound struct {
	Kind BoundKind
	Max  int // 仅 Kind == BoundAtMost 时使用
}

func ExactlyOne() Bound  { return Bound{Kind: BoundExactlyOne} }
func AtMostOne() Bound   { return Bound{Kind: BoundAtMostOne} }
func AtMost(n int) Bound { return Bound{Kind: BoundAtMost, Max: n} }
func Unlimited() Bound   { return Bound{} }

// limit 返回该端的数值上限以及是否有限。
func (b Bound) limit() (int, bool) {
	switch b.Kind {
	case BoundExactlyOne, BoundAtMostOne:
		return 1, true
	case BoundAtMost:
		if b.Max <= 0 {
			return 0, false
		}
		return b.Max, true
	default:
		return 0, false
	}
}

// LinkTypeSpec 声明一个链接类型：两端对象类型及各自一侧的基数上限。
type LinkTypeSpec struct {
	Name        LinkTypeName
	SourceType  ObjectTypeName
	TargetType  ObjectTypeName
	SourceBound Bound
	TargetBound Bound
}

// LinkKey 唯一确定一条链接：链接类型 + 有序实例对。
type LinkKey struct {
	Type   LinkTypeName
	Source InstanceID
	Target InstanceID
}

// endpointSide 区分链接类型的两端。
type endpointSide int

const (
	sideSource endpointSide = iota
	sideTarget
)

// Side 是对外暴露的端点侧标识，用于 Occupied/CheckCost 查询。
type Side int

const (
	SideSource Side = Side(sideSource)
	SideTarget Side = Side(sideTarget)
)

// countKey 定位「某链接类型、某一端、某具体实例」当前占用的基数计数。
type countKey struct {
	linkType LinkTypeName
	side     endpointSide
	instance InstanceID
}

// linkTypeEntry 缓存链接类型的声明信息。
type linkTypeEntry struct {
	spec        LinkTypeSpec
	sourceLimit int
	targetLimit int
	sourceCap   bool
	targetCap   bool
}

// Ledger 负责链接存储与两端基数账本的维护。
//
// 关键取舍：所有读写都经同一把互斥锁串行化，从而对「创建 / 删除 / 批量导入」
// 提供可线性化（linearizable）的全局串行顺序；规模可控的内存映射足以支撑
// 本系统，刻意不引入分片锁带来的跨端顺序不确定性。
//
// 基数账本是每个「(链接类型, 端, 实例)」一个独立计数。校验一条待创建链接
// 只需读取两端两个计数并各做一次常数比较，开销只取决于该具体实例的占用值
// 是否命中上限（O(1) 次映射访问），与该对象类型下的链接总数无关。
type Ledger struct {
	mu        sync.Mutex
	objects   map[ObjectTypeName]map[InstanceID]struct{}
	linkTypes map[LinkTypeName]*linkTypeEntry
	links     map[LinkKey]struct{}
	counts    map[countKey]int
}

func NewLedger() *Ledger {
	return &Ledger{
		objects:   make(map[ObjectTypeName]map[InstanceID]struct{}),
		linkTypes: make(map[LinkTypeName]*linkTypeEntry),
		links:     make(map[LinkKey]struct{}),
		counts:    make(map[countKey]int),
	}
}

// lock/Unlock 供同包的 Importer 复用同一条全局串行顺序。
func (l *Ledger) lock()   { l.mu.Lock() }
func (l *Ledger) unlock() { l.mu.Unlock() }

// RegisterLinkType 注册一个链接类型；重复注册返回参数非法错误。
func (l *Ledger) RegisterLinkType(spec LinkTypeSpec) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if spec.Name == "" {
		return invalidArgument("link type name must not be empty")
	}
	if spec.SourceType == "" || spec.TargetType == "" {
		return invalidArgument("link type %q must declare both endpoint object types", spec.Name)
	}
	if _, dup := l.linkTypes[spec.Name]; dup {
		return invalidArgument("link type %q already registered", spec.Name)
	}
	if spec.SourceBound.Kind == BoundAtMost && spec.SourceBound.Max <= 0 {
		return invalidArgument("source bound of link type %q must be a positive integer", spec.Name)
	}
	if spec.TargetBound.Kind == BoundAtMost && spec.TargetBound.Max <= 0 {
		return invalidArgument("target bound of link type %q must be a positive integer", spec.Name)
	}
	entry := &linkTypeEntry{spec: spec}
	if max, ok := spec.SourceBound.limit(); ok {
		entry.sourceCap, entry.sourceLimit = true, max
	}
	if max, ok := spec.TargetBound.limit(); ok {
		entry.targetCap, entry.targetLimit = true, max
	}
	l.linkTypes[spec.Name] = entry
	return nil
}

// CreateObject 登记一个具体对象实例；同类型下重复 ID 返回参数非法错误。
func (l *Ledger) CreateObject(typ ObjectTypeName, id InstanceID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if typ == "" || id == "" {
		return invalidArgument("object type and instance id must not be empty")
	}
	ids := l.objects[typ]
	if ids == nil {
		ids = make(map[InstanceID]struct{})
		l.objects[typ] = ids
	}
	if _, dup := ids[id]; dup {
		return invalidArgument("instance %q of type %q already exists", id, typ)
	}
	ids[id] = struct{}{}
	return nil
}

func (l *Ledger) instanceExists(typ ObjectTypeName, id InstanceID) bool {
	_, ok := l.objects[typ][id]
	return ok
}

// validateCreate 执行创建前的完整校验，严格按错误优先级只返回第一个命中原因。
// 调用方必须持有 l.mu。
func (l *Ledger) validateCreate(linkType LinkTypeName, source, target InstanceID) (*linkTypeEntry, *LinkError) {
	entry, ok := l.linkTypes[linkType]
	if !ok {
		return nil, invalidArgument("link type %q is not registered", linkType)
	}
	// 参数非法优先：实例不存在先于任何基数判断。
	if !l.instanceExists(entry.spec.SourceType, source) {
		return nil, invalidArgument("source instance %q (type %q) does not exist", source, entry.spec.SourceType)
	}
	if !l.instanceExists(entry.spec.TargetType, target) {
		return nil, invalidArgument("target instance %q (type %q) does not exist", target, entry.spec.TargetType)
	}
	key := LinkKey{Type: linkType, Source: source, Target: target}
	if _, dup := l.links[key]; dup {
		return nil, invalidArgument("ordered pair (%q,%q) is already linked by %q", source, target, linkType)
	}
	// 起点一侧基数超限次之。
	if entry.sourceCap {
		cur := l.counts[countKey{linkType, sideSource, source}]
		if cur >= entry.sourceLimit {
			return nil, sourceCapExceeded(
				"source %q already occupies %d/%d of link type %q",
				source, cur, entry.sourceLimit, linkType)
		}
	}
	// 终点一侧基数超限再次之。注意：即使起点超限本分支不可达，
	// 两端的结论本身互不影响——这里只是错误归一化要求只报第一个原因。
	if entry.targetCap {
		cur := l.counts[countKey{linkType, sideTarget, target}]
		if cur >= entry.targetLimit {
			return nil, targetCapExceeded(
				"target %q already occupies %d/%d of link type %q",
				target, cur, entry.targetLimit, linkType)
		}
	}
	return entry, nil
}

// applyCreate 落库并增加两端计数；调用方必须持有 l.mu 且已通过 validateCreate。
func (l *Ledger) applyCreate(linkType LinkTypeName, source, target InstanceID) {
	l.links[LinkKey{Type: linkType, Source: source, Target: target}] = struct{}{}
	l.counts[countKey{linkType, sideSource, source}]++
	l.counts[countKey{linkType, sideTarget, target}]++
}

// CreateLink 创建一条链接。任一端基数超限即拒绝且不留任何痕迹。
func (l *Ledger) CreateLink(linkType LinkTypeName, source, target InstanceID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.validateCreate(linkType, source, target)
	if err != nil {
		return err
	}
	l.applyCreate(linkType, source, target)
	return nil
}

// DeleteLink 删除链接并立即释放两端各一个基数名额。
// 释放与删除在同一个临界区内完成，对后续并发创建的可见时刻就是
// 本次删除的生效时刻，不存在任何额外时延。
// 删除不存在的链接返回独立的 ReasonLinkNotFound。
func (l *Ledger) DeleteLink(linkType LinkTypeName, source, target InstanceID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := LinkKey{Type: linkType, Source: source, Target: target}
	if _, ok := l.links[key]; !ok {
		return linkNotFound("link (%q: %q -> %q) does not exist", linkType, source, target)
	}
	delete(l.links, key)
	l.decCount(linkType, sideSource, source)
	l.decCount(linkType, sideTarget, target)
	return nil
}

func (l *Ledger) decCount(linkType LinkTypeName, side endpointSide, instance InstanceID) {
	k := countKey{linkType, side, instance}
	cur := l.counts[k]
	if cur <= 1 {
		delete(l.counts, k)
		return
	}
	l.counts[k] = cur - 1
}

// HasLink 报告链接是否存在。
func (l *Ledger) HasLink(linkType LinkTypeName, source, target InstanceID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.links[LinkKey{Type: linkType, Source: source, Target: target}]
	return ok
}

// LinkCount 返回当前链接总数。
func (l *Ledger) LinkCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.links)
}

// Occupied 返回某实例在某链接类型某一侧当前已占用的基数名额。
func (l *Ledger) Occupied(linkType LinkTypeName, side Side, instance InstanceID) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.counts[countKey{linkType, endpointSide(side), instance}]
}

// Snapshot 返回当前链接集合的一个副本，供测试与差分对照使用。
func (l *Ledger) Snapshot() map[LinkKey]struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[LinkKey]struct{}, len(l.links))
	for k := range l.links {
		out[k] = struct{}{}
	}
	return out
}

// CheckCost 以「基础映射访问次数」为单位，报告对单条待创建链接做基数
// 校验所执行的工作。它返回的数字只依赖声明与固定数量的映射查找：
//
//	1  查链接类型声明
//	2  查起点/终点实例是否存在
//	1  查有序对是否已存在
//	+2 若两端均声明上限，各查一次该实例的计数
//
// 该值不会随任一对象类型下链接总数的增长而增大，是复杂度声明的
// 可验证依据（见 ontology/cost_verification_test.go 与 docs/design.md）。
func (l *Ledger) CheckCost(linkType LinkTypeName) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.linkTypes[linkType]
	if !ok {
		return 0, fmt.Errorf("link type %q is not registered", linkType)
	}
	cost := 4 // 类型声明 + 两个实例存在性 + 有序对去重
	if entry.sourceCap {
		cost++
	}
	if entry.targetCap {
		cost++
	}
	return cost, nil
}
