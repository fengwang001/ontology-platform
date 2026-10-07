package ontology

import "sync"

// Path 声明一条固定长度的链接路径。
// Types[i] 是第 i 跳（0 基）源对象允许的类型集合，Types[n] 是终点类型集合。
// Links[i] 是第 i 跳使用的链接（边类型）名称。
type Path struct {
	Types [][]string
	Links []string
}

// ViewSpec 声明一个聚合视图：沿 Path 传播，对终点 Attr 数值属性取最大值。
type ViewSpec struct {
	Name string
	Path Path
	Attr string
}

// Aggregate 是某个起点实例的聚合结果。Present 为 false 表示明确的“不存在”。
type Aggregate struct {
	Present bool
	Value   int64
}

// LinkOptions 控制一次链接增删的行为。
type LinkOptions struct {
	// RejectCycle 为 true 时，若该新增会在无法静态排除环的视图上产生路径环，则拒绝变更。
	RejectCycle bool
}

// Logger 接收每次变更的输入、受影响起点集合与判定依据。
type Logger func(line string)

// Graph 是本体对象与链接的存储，同时持有全部已注册聚合视图。
type Graph struct {
	mu sync.RWMutex

	types     map[string]struct{}
	linkTypes map[string][]linkSig
	objects   map[string]*object

	// out[link][src][dst] = 该链接上 src->dst 边实例的条数。
	out map[string]map[string]map[string]int64
	// in[link][dst][src] = 反向边条数。
	in map[string]map[string]map[string]int64

	views  []*viewState
	byName map[string]*viewState

	logf Logger
}

// NewGraph 创建空本体图。
func NewGraph() *Graph {
	return &Graph{
		types:     map[string]struct{}{},
		linkTypes: map[string][]linkSig{},
		objects:   map[string]*object{},
		out:       map[string]map[string]map[string]int64{},
		in:        map[string]map[string]map[string]int64{},
		byName:    map[string]*viewState{},
	}
}

// SetLogger 安装变更日志回调。
func (g *Graph) SetLogger(f Logger) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.logf = f
}

// AddObjectType 注册一种对象类型。
func (g *Graph) AddObjectType(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.types[name] = struct{}{}
}

// AddLinkType 声明一种链接及其源/目标对象类型签名。
// 同一链接名可多次调用以追加允许的类型对（服务于跳类型集合扩展）。
func (g *Graph) AddLinkType(name, srcType, dstType string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.types[srcType]; !ok {
		return errf(KindTypeMismatch, "link %q source type %q not registered", name, srcType)
	}
	if _, ok := g.types[dstType]; !ok {
		return errf(KindTypeMismatch, "link %q target type %q not registered", name, dstType)
	}
	for _, sig := range g.linkTypes[name] {
		if sig.srcType == srcType && sig.dstType == dstType {
			return nil
		}
	}
	g.linkTypes[name] = append(g.linkTypes[name], linkSig{srcType: srcType, dstType: dstType})
	return nil
}

// CreateObject 创建一个具有类型的对象实例。
func (g *Graph) CreateObject(id, typ string, attrs map[string]int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.types[typ]; !ok {
		return errf(KindTypeMismatch, "object %q type %q not registered", id, typ)
	}
	if _, dup := g.objects[id]; dup {
		return errf(KindTypeMismatch, "object %q already exists", id)
	}
	am := make(map[string]int64, len(attrs))
	for k, v := range attrs {
		am[k] = v
	}
	g.objects[id] = &object{id: id, typ: typ, attrs: am}
	return nil
}

// ObjectExists 判断对象实例是否存在。
func (g *Graph) ObjectExists(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.objects[id]
	return ok
}

// GetAttr 读取对象实例的数值属性。
func (g *Graph) GetAttr(id, attr string) (int64, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	o, ok := g.objects[id]
	if !ok {
		return 0, false
	}
	v, ok := o.attrs[attr]
	return v, ok
}

// AddLink 增加一跳链接并增量维护相关视图，返回受影响的起点实例集合。
func (g *Graph) AddLink(link, src, dst string, opts LinkOptions) (map[string]struct{}, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkLinkEndpoints(link, src, dst); err != nil {
		return nil, err
	}
	old := g.edgeMul(link, src, dst)
	g.addEdge(link, src, dst)
	affected, err := g.applyLinkChange(link, src, dst, old, old+1, opts)
	if err != nil {
		// applyLinkChange 内部已回滚全部视图状态；这里撤销边变更。
		g.removeEdgeRaw(link, src, dst)
		return nil, err
	}
	g.logChange(sprintf("AddLink link=%s %s->%s", link, src, dst), affected, err)
	return affected, nil
}

// RemoveLink 删除一跳链接并增量维护相关视图，返回受影响的起点实例集合。
func (g *Graph) RemoveLink(link, src, dst string) (map[string]struct{}, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.checkLinkEndpoints(link, src, dst); err != nil {
		return nil, err
	}
	old := g.edgeMul(link, src, dst)
	if old == 0 {
		return map[string]struct{}{}, nil
	}
	g.removeEdgeRaw(link, src, dst)
	affected, err := g.applyLinkChange(link, src, dst, old, old-1, LinkOptions{})
	if err != nil {
		g.addEdge(link, src, dst)
		return nil, err
	}
	g.logChange(sprintf("RemoveLink link=%s %s->%s", link, src, dst), affected, err)
	return affected, nil
}

// RegisterView 校验并注册一个聚合视图，立即基于当前连通关系建立聚合状态。
func (g *Graph) RegisterView(spec ViewSpec) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, dup := g.byName[spec.Name]; dup {
		return errf(KindTypeMismatch, "view %q already registered", spec.Name)
	}
	if err := g.validateSpec(spec); err != nil {
		return err
	}
	vs := &viewState{spec: spec, cycleExcludable: staticallyCycleFree(spec.Path)}
	vs.snap = g.buildSnapshot(vs)
	g.views = append(g.views, vs)
	g.byName[spec.Name] = vs
	return nil
}

func (g *Graph) checkLinkEndpoints(link, src, dst string) error {
	sigs, ok := g.linkTypes[link]
	if !ok {
		return errf(KindTypeMismatch, "link type %q not declared", link)
	}
	so, sok := g.objects[src]
	do, dok := g.objects[dst]
	// 类型不匹配优先级高于实例不存在：先用已存在端点施加类型约束。
	okPair := false
	for _, sig := range sigs {
		srcOK := !sok || so.typ == sig.srcType
		dstOK := !dok || do.typ == sig.dstType
		if srcOK && dstOK {
			okPair = true
			break
		}
	}
	if !okPair {
		if sok && dok {
			return errf(KindTypeMismatch, "link %q does not allow %q(%s)->%q(%s)",
				link, src, so.typ, dst, do.typ)
		}
		if sok {
			return errf(KindTypeMismatch, "link %q does not allow source type %q", link, so.typ)
		}
		return errf(KindTypeMismatch, "link %q does not allow target type %q", link, do.typ)
	}
	if !sok {
		return errf(KindInstanceNotFound, "source object %q", src)
	}
	if !dok {
		return errf(KindInstanceNotFound, "target object %q", dst)
	}
	return nil
}

func (g *Graph) edgeMul(link, src, dst string) int64 {
	if m := g.out[link][src]; m != nil {
		return m[dst]
	}
	return 0
}

func (g *Graph) addEdge(link, src, dst string) {
	if g.out[link] == nil {
		g.out[link] = map[string]map[string]int64{}
	}
	if g.in[link] == nil {
		g.in[link] = map[string]map[string]int64{}
	}
	if g.out[link][src] == nil {
		g.out[link][src] = map[string]int64{}
	}
	if g.in[link][dst] == nil {
		g.in[link][dst] = map[string]int64{}
	}
	g.out[link][src][dst]++
	g.in[link][dst][src]++
}

func (g *Graph) removeEdgeRaw(link, src, dst string) {
	if g.edgeMul(link, src, dst) <= 1 {
		delete(g.out[link][src], dst)
		delete(g.in[link][dst], src)
	} else {
		g.out[link][src][dst]--
		g.in[link][dst][src]--
	}
}

func (g *Graph) logChange(input string, affected map[string]struct{}, err error) {
	if g.logf == nil {
		return
	}
	g.logf(sprintf("%s | affected=%v | err=%v", input, sortedKeys(affected), err))
}
