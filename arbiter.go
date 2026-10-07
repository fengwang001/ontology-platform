package ontology

import (
	"fmt"
	"sync"
)

// 本文件实现「联合仲裁与错误归一化」模块（Platform）。
//
// Platform 持有：
//   - Ledger：链接基数账本（存在性与两端槽位计数）；
//   - Resolver：权限继承与覆盖解析；
//   - 类型/链接类型 schema 与全局逻辑时钟。
//
// 每一次创建、删除、授权、成员/包含关系变更都在同一把互斥锁内按到达
// 顺序原子应用并消费一个单调递增的逻辑时钟值。因此并发请求的实际效果
// 等价于按锁获取顺序形成的某个全局串行序列；相同的操作序列重放，
// 必然得到完全相同的链接集合与可见性结果。

// linkMeta 缓存一个链接类型仲裁所需的静态信息。
type linkMeta struct {
	spec           LinkTypeSpec
	sourceProperty string
	targetProperty string
}

// Platform 是链接基数账本、权限继承覆盖解析、联合仲裁三模块的协作入口。
type Platform struct {
	mu        sync.Mutex
	types     map[string]map[string]PropertySpec
	links     map[string]linkMeta
	instances map[string]string // 实例 -> 对象类型（与 Resolver 镜像）
	ledger    *Ledger
	resolver  *Resolver
	clock     int64
}

// NewPlatform 创建一个空平台。
func NewPlatform() *Platform {
	p := &Platform{
		types:     make(map[string]map[string]PropertySpec),
		links:     make(map[string]linkMeta),
		instances: make(map[string]string),
		ledger:    NewLedger(),
		resolver:  NewResolver(),
	}
	return p
}

// DefineObjectType 声明一个对象类型及其属性默认可见性。
func (p *Platform) DefineObjectType(name string, props []PropertySpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[string]PropertySpec, len(props))
	for _, pr := range props {
		m[pr.Name] = pr
	}
	p.types[name] = m
	for _, pr := range props {
		p.resolver.SetTypeDefault(name, pr.Name, pr.DefaultVis)
	}
}

// DefineLinkType 声明一个链接类型及其两端基数上限。
func (p *Platform) DefineLinkType(spec LinkTypeSpec) *DecisionError {
	p.mu.Lock()
	defer p.mu.Unlock()
	if spec.Name == "" {
		return invalid("link type name is empty")
	}
	if _, ok := p.links[spec.Name]; ok {
		return invalid("link type %q already defined", spec.Name)
	}
	if err := p.checkEndpoint(spec.Source); err != nil {
		return err
	}
	if err := p.checkEndpoint(spec.Target); err != nil {
		return err
	}
	if spec.SourceMax < 0 || spec.TargetMax < 0 {
		return invalid("cardinality limits must be >= 0 (0 means unbounded)")
	}
	p.links[spec.Name] = linkMeta{
		spec:           spec,
		sourceProperty: spec.Source.Property,
		targetProperty: spec.Target.Property,
	}
	return nil
}

func (p *Platform) checkEndpoint(ep EndpointSpec) *DecisionError {
	props, ok := p.types[ep.ObjectType]
	if !ok {
		return invalid("object type %q is not defined", ep.ObjectType)
	}
	pr, ok := props[ep.Property]
	if !ok {
		return invalid("property %q is not defined on object type %q", ep.Property, ep.ObjectType)
	}
	if !pr.LinkSource {
		return invalid("property %q on object type %q is not declared as a link source", ep.Property, ep.ObjectType)
	}
	return nil
}

// CreateInstance 创建实例。
func (p *Platform) CreateInstance(id, objectType string) *DecisionError {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" {
		return invalid("instance id is empty")
	}
	if _, ok := p.types[objectType]; !ok {
		return invalid("object type %q is not defined", objectType)
	}
	if _, ok := p.instances[id]; ok {
		return invalid("instance %q already exists", id)
	}
	p.instances[id] = objectType
	p.resolver.SetInstanceType(id, objectType)
	return nil
}

// GrantActor / GrantRole 变更权限覆盖；每次变更消费一个逻辑时钟值，
// 作为同距离覆盖「声明时间更晚」的时间戳。
func (p *Platform) GrantActor(actor, instance, property string, vis Visibility) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock++
	p.resolver.GrantActor(actor, instance, property, vis, p.clock)
}

func (p *Platform) GrantRole(role, instance, property string, vis Visibility) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock++
	p.resolver.GrantRole(role, instance, property, vis, p.clock)
}

// AddActorRole / AddRoleContains 变更角色层级。
func (p *Platform) AddActorRole(actor, role string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock++
	p.resolver.AddActorRole(actor, role)
}

func (p *Platform) AddRoleContains(contains, inner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clock++
	p.resolver.AddRoleContains(contains, inner)
}

// LinkExists 报告链接物理存在性（不经权限，仅供管理/测试用途）。
func (p *Platform) LinkExists(link Link) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ledger.Exists(link)
}

// Visibility 返回操作者对某实例来源属性的最终可见性。
func (p *Platform) Visibility(actor, instance, property string) Visibility {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resolver.Visibility(actor, instance, property)
}

// CreateLink 按统一优先级仲裁创建链接：
//
//  1. 参数非法；
//  2. 操作者对任一端点来源属性无可见权限（起点端先判定）；
//  3. 起点一侧基数超限；
//  4. 终点一侧基数超限。
//
// 四类错误可相互区分，只报第一个命中的原因；全部通过才落账。
func (p *Platform) CreateLink(actor, linkType, source, target string) *DecisionError {
	p.mu.Lock()
	defer p.mu.Unlock()

	if actor == "" {
		return invalid("actor is empty")
	}
	meta, err := p.validateLink(linkType, source, target)
	if err != nil {
		return err
	}
	link := Link{LinkType: linkType, SourceInstance: source, TargetInstance: target}

	if v := p.resolver.Visibility(actor, source, meta.sourceProperty); v != Visible {
		return denied("actor %q cannot see source property %q on instance %q",
			actor, meta.sourceProperty, source)
	}
	if v := p.resolver.Visibility(actor, target, meta.targetProperty); v != Visible {
		return denied("actor %q cannot see target property %q on instance %q",
			actor, meta.targetProperty, target)
	}

	// 重复检测置于权限之后：不可见操作者不能借 already exists 探测链接存在。
	if p.ledger.Exists(link) {
		return invalid("link %s already exists", link)
	}

	if !p.ledger.CheckSource(source, meta.sourceProperty, meta.spec.SourceMax) {
		return sourceCard("source cardinality exceeded: instance %q property %q already at limit %d",
			source, meta.sourceProperty, meta.spec.SourceMax)
	}
	if !p.ledger.CheckTarget(target, meta.targetProperty, meta.spec.TargetMax) {
		return targetCard("target cardinality exceeded: instance %q property %q already at limit %d",
			target, meta.targetProperty, meta.spec.TargetMax)
	}

	p.clock++
	p.ledger.AddOnSlots(link, meta.sourceProperty, meta.targetProperty)
	return nil
}

// DeleteLink 删除链接。拒绝次序简化为：
//
//  1. 参数非法；
//  2. 不可见或链接不存在 —— 两者合并为完全相同的 ErrNotFound，
//     因而无法据响应区分链接是不存在还是仅对该操作者隐藏。
//
// 删除不涉及任何基数超限判断。
func (p *Platform) DeleteLink(actor, linkType, source, target string) *DecisionError {
	p.mu.Lock()
	defer p.mu.Unlock()

	if actor == "" {
		return invalid("actor is empty")
	}
	meta, err := p.validateLink(linkType, source, target)
	if err != nil {
		return err
	}
	link := Link{LinkType: linkType, SourceInstance: source, TargetInstance: target}

	// 先查存在性，但对不可见操作者把「存在」与「不存在」归一化为同一错误。
	exists := p.ledger.Exists(link)
	if exists {
		srcVis := p.resolver.Visibility(actor, source, meta.sourceProperty)
		tgtVis := p.resolver.Visibility(actor, target, meta.targetProperty)
		if srcVis != Visible || tgtVis != Visible {
			return notFound("link %s is not visible to actor %q", link, actor)
		}
	}
	if !p.ledger.Remove(link, meta.sourceProperty, meta.targetProperty) {
		return notFound("link %s does not exist", link)
	}
	p.clock++
	return nil
}

func (p *Platform) validateLink(linkType, source, target string) (linkMeta, *DecisionError) {
	if linkType == "" || source == "" || target == "" {
		return linkMeta{}, invalid("link type, source and target must be non-empty")
	}
	meta, ok := p.links[linkType]
	if !ok {
		return linkMeta{}, invalid("link type %q is not defined", linkType)
	}
	if got := p.instances[source]; got != meta.spec.Source.ObjectType {
		return linkMeta{}, invalid("source instance %q is not of object type %q",
			source, meta.spec.Source.ObjectType)
	}
	if got := p.instances[target]; got != meta.spec.Target.ObjectType {
		return linkMeta{}, invalid("target instance %q is not of object type %q",
			target, meta.spec.Target.ObjectType)
	}
	return meta, nil
}

// LinkVisible 报告一条现存链接对该操作者当前是否可见（两端均须可见）。
// 链接对操作者不可见时只是读取视图过滤，账本存在性与基数占用均不变。
func (p *Platform) LinkVisible(actor string, link Link) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	meta, ok := p.links[link.LinkType]
	if !ok || !p.ledger.Exists(link) {
		return false
	}
	return p.resolver.Visibility(actor, link.SourceInstance, meta.sourceProperty) == Visible &&
		p.resolver.Visibility(actor, link.TargetInstance, meta.targetProperty) == Visible
}

// VisibleLinks 返回某操作者当前可见的全部现存链接（按键排序）。
func (p *Platform) VisibleLinks(actor string) []Link {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Link
	for _, link := range p.ledger.All() {
		meta := p.links[link.LinkType]
		if p.resolver.Visibility(actor, link.SourceInstance, meta.sourceProperty) == Visible &&
			p.resolver.Visibility(actor, link.TargetInstance, meta.targetProperty) == Visible {
			out = append(out, link)
		}
	}
	return out
}

// AllLinks 返回全部现存链接的物理快照，不经权限过滤（管理/对照用途）。
func (p *Platform) AllLinks() []Link {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ledger.All()
}

// SourceCount / TargetCount 暴露当前槽位占用，供测试与文档演示核验：
// 权限收紧不改变这些计数。
func (p *Platform) SourceCount(linkType, instance string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	meta := p.links[linkType]
	return p.ledger.CountSource(instance, meta.sourceProperty)
}

func (p *Platform) TargetCount(linkType, instance string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	meta := p.links[linkType]
	return p.ledger.CountTarget(instance, meta.targetProperty)
}

func invalid(format string, args ...any) *DecisionError {
	return &DecisionError{Class: ErrInvalidArg, Message: fmt.Sprintf(format, args...)}
}
func denied(format string, args ...any) *DecisionError {
	return &DecisionError{Class: ErrPermissionDenied, Message: fmt.Sprintf(format, args...)}
}
func sourceCard(format string, args ...any) *DecisionError {
	return &DecisionError{Class: ErrSourceCardExceeded, Message: fmt.Sprintf(format, args...)}
}
func targetCard(format string, args ...any) *DecisionError {
	return &DecisionError{Class: ErrTargetCardExceeded, Message: fmt.Sprintf(format, args...)}
}
func notFound(format string, args ...any) *DecisionError {
	return &DecisionError{Class: ErrNotFound, Message: fmt.Sprintf(format, args...)}
}
