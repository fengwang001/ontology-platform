package ontology

import "sort"

// 本文件实现与 Platform 行为等价的「朴素参考模型」NaivePlatform：
//
//   - 独立维护全量对象类型、链接类型、实例、链接与权限覆盖声明，
//     不与 Platform 共享任何内存结构；
//   - 每次计算可见性时都从操作者出发完整遍历整个可达角色层级，
//     收集全部覆盖后再按「距离最短、同距离 seq 最新」择优；
//   - 不使用任何缓存、剪枝或增量结构，作为差分测试的判定基准。
//
// 朴素模型故意写得直白，便于人工核对其与需求文字逐条对应。

type naiveGrant struct {
	subjectKind int
	subject     string
	instance    string
	property    string
	vis         Visibility
	seq         int64
}

// NaivePlatform 是独立全量朴素模型。
type NaivePlatform struct {
	typeProps map[string]map[string]PropertySpec
	linkTypes map[string]LinkTypeSpec
	instances map[string]string
	links     map[string]Link
	linkSrc   map[string]string // linkKey -> source property
	linkTgt   map[string]string // linkKey -> target property
	srcCount  map[slotKey]int
	tgtCount  map[slotKey]int
	actorRole map[string]map[string]bool
	parents   map[string]map[string]bool
	grants    []naiveGrant
	clock     int64
}

// NewNaivePlatform 创建朴素参考模型。
func NewNaivePlatform() *NaivePlatform {
	return &NaivePlatform{
		typeProps: make(map[string]map[string]PropertySpec),
		linkTypes: make(map[string]LinkTypeSpec),
		instances: make(map[string]string),
		links:     make(map[string]Link),
		linkSrc:   make(map[string]string),
		linkTgt:   make(map[string]string),
		srcCount:  make(map[slotKey]int),
		tgtCount:  make(map[slotKey]int),
		actorRole: make(map[string]map[string]bool),
		parents:   make(map[string]map[string]bool),
	}
}

func (n *NaivePlatform) DefineObjectType(name string, props []PropertySpec) {
	m := make(map[string]PropertySpec, len(props))
	for _, pr := range props {
		m[pr.Name] = pr
	}
	n.typeProps[name] = m
}

func (n *NaivePlatform) DefineLinkType(spec LinkTypeSpec) *DecisionError {
	if spec.Name == "" {
		return invalid("[naive] link type name is empty")
	}
	if _, ok := n.linkTypes[spec.Name]; ok {
		return invalid("[naive] duplicate link type %q", spec.Name)
	}
	if !n.endpointOK(spec.Source) || !n.endpointOK(spec.Target) {
		return invalid("[naive] bad endpoint on link type %q", spec.Name)
	}
	if spec.SourceMax < 0 || spec.TargetMax < 0 {
		return invalid("[naive] negative cardinality")
	}
	n.linkTypes[spec.Name] = spec
	return nil
}

func (n *NaivePlatform) endpointOK(ep EndpointSpec) bool {
	pr, ok := n.typeProps[ep.ObjectType][ep.Property]
	return ok && pr.LinkSource
}

func (n *NaivePlatform) CreateInstance(id, objectType string) *DecisionError {
	if id == "" {
		return invalid("[naive] empty instance id")
	}
	if _, ok := n.typeProps[objectType]; !ok {
		return invalid("[naive] unknown object type %q", objectType)
	}
	if _, ok := n.instances[id]; ok {
		return invalid("[naive] duplicate instance %q", id)
	}
	n.instances[id] = objectType
	return nil
}

func (n *NaivePlatform) GrantActor(actor, instance, property string, vis Visibility) {
	n.clock++
	n.grants = append(n.grants, naiveGrant{0, actor, instance, property, vis, n.clock})
}

func (n *NaivePlatform) GrantRole(role, instance, property string, vis Visibility) {
	n.clock++
	n.grants = append(n.grants, naiveGrant{1, role, instance, property, vis, n.clock})
}

func (n *NaivePlatform) AddActorRole(actor, role string) {
	n.clock++
	if n.actorRole[actor] == nil {
		n.actorRole[actor] = make(map[string]bool)
	}
	n.actorRole[actor][role] = true
}

func (n *NaivePlatform) AddRoleContains(contains, inner string) {
	n.clock++
	if n.parents[contains] == nil {
		n.parents[contains] = make(map[string]bool)
	}
	n.parents[contains][inner] = true
}

// distances 完整遍历可达角色层级，返回角色 -> 从操作者出发的最短距离。
func (n *NaivePlatform) distances(actor string) map[string]int {
	dist := make(map[string]int)
	var frontier []string
	for role := range n.actorRole[actor] {
		if _, seen := dist[role]; !seen {
			dist[role] = 1
			frontier = append(frontier, role)
		}
	}
	for len(frontier) > 0 {
		role := frontier[0]
		frontier = frontier[1:]
		for parent := range n.parents[role] {
			if _, seen := dist[parent]; !seen {
				dist[parent] = dist[role] + 1
				frontier = append(frontier, parent)
			}
		}
	}
	return dist
}

// Visibility 每次都完整重遍历整个层级、扫描全部覆盖声明。
func (n *NaivePlatform) Visibility(actor, instance, property string) Visibility {
	bestDist := 1 << 30
	bestSeq := int64(-1)
	found := false
	result := Invisible

	consider := func(distance int, g naiveGrant) {
		if g.instance != instance || g.property != property {
			return
		}
		if !found || distance < bestDist || (distance == bestDist && g.seq > bestSeq) {
			found = true
			bestDist = distance
			bestSeq = g.seq
			result = g.vis
		}
	}

	dist := n.distances(actor)
	for _, g := range n.grants {
		switch g.subjectKind {
		case 0:
			if g.subject == actor {
				consider(0, g)
			}
		case 1:
			if d, ok := dist[g.subject]; ok {
				consider(d, g)
			}
		}
	}
	if !found {
		t := n.instances[instance]
		return n.typeProps[t][property].DefaultVis
	}
	return result
}

func (n *NaivePlatform) validate(linkType, source, target string) (LinkTypeSpec, bool) {
	spec, ok := n.linkTypes[linkType]
	if !ok || source == "" || target == "" {
		return LinkTypeSpec{}, false
	}
	if n.instances[source] != spec.Source.ObjectType ||
		n.instances[target] != spec.Target.ObjectType {
		return LinkTypeSpec{}, false
	}
	return spec, true
}

// CreateLink 与 Platform 相同的优先级：参数 → 权限 → 起点基数 → 终点基数。
func (n *NaivePlatform) CreateLink(actor, linkType, source, target string) *DecisionError {
	if actor == "" {
		return invalid("[naive] empty actor")
	}
	spec, ok := n.validate(linkType, source, target)
	if !ok {
		return invalid("[naive] invalid link arguments")
	}
	link := Link{linkType, source, target}
	if n.Visibility(actor, source, spec.Source.Property) != Visible {
		return denied("[naive] source property invisible")
	}
	if n.Visibility(actor, target, spec.Target.Property) != Visible {
		return denied("[naive] target property invisible")
	}
	if _, dup := n.links[link.Key()]; dup {
		return invalid("[naive] link already exists")
	}
	sk := slotKey{source, spec.Source.Property}
	tk := slotKey{target, spec.Target.Property}
	if spec.SourceMax > 0 && n.srcCount[sk] >= spec.SourceMax {
		return sourceCard("[naive] source cardinality exceeded")
	}
	if spec.TargetMax > 0 && n.tgtCount[tk] >= spec.TargetMax {
		return targetCard("[naive] target cardinality exceeded")
	}
	n.clock++
	n.links[link.Key()] = link
	n.linkSrc[link.Key()] = spec.Source.Property
	n.linkTgt[link.Key()] = spec.Target.Property
	n.srcCount[sk]++
	n.tgtCount[tk]++
	return nil
}

// DeleteLink 参数非法在前；不可见与不存在合并为同一 ErrNotFound。
func (n *NaivePlatform) DeleteLink(actor, linkType, source, target string) *DecisionError {
	if actor == "" {
		return invalid("[naive] empty actor")
	}
	spec, ok := n.validate(linkType, source, target)
	if !ok {
		return invalid("[naive] invalid link arguments")
	}
	link := Link{linkType, source, target}
	if _, exists := n.links[link.Key()]; exists {
		if n.Visibility(actor, source, spec.Source.Property) != Visible ||
			n.Visibility(actor, target, spec.Target.Property) != Visible {
			return notFound("[naive] link not visible")
		}
	}
	key := link.Key()
	if _, exists := n.links[key]; !exists {
		return notFound("[naive] link does not exist")
	}
	delete(n.links, key)
	n.srcCount[slotKey{source, n.linkSrc[key]}]--
	n.tgtCount[slotKey{target, n.linkTgt[key]}]--
	delete(n.linkSrc, key)
	delete(n.linkTgt, key)
	n.clock++
	return nil
}

// VisibleLinks 返回操作者可见链接（按键排序，与 Platform 快照顺序一致）。
func (n *NaivePlatform) VisibleLinks(actor string) []Link {
	var out []Link
	keys := make([]string, 0, len(n.links))
	for k := range n.links {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		link := n.links[k]
		spec := n.linkTypes[link.LinkType]
		if n.Visibility(actor, link.SourceInstance, spec.Source.Property) == Visible &&
			n.Visibility(actor, link.TargetInstance, spec.Target.Property) == Visible {
			out = append(out, link)
		}
	}
	return out
}

// AllLinks 返回全部物理链接。
func (n *NaivePlatform) AllLinks() []Link {
	keys := make([]string, 0, len(n.links))
	for k := range n.links {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Link, 0, len(keys))
	for _, k := range keys {
		out = append(out, n.links[k])
	}
	return out
}
