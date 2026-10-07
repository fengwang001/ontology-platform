package compensation

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Object 是对象图中的一个对象实例。
type Object struct {
	id      string
	mu      sync.RWMutex
	props   map[string]any
	clock   uint64
	version uint64
}

// Link 是对象图中的一条链接实例。
type Link struct {
	id      string
	from    string
	to      string
	typ     string
	version uint64
	alive   bool
}

// taintState 记录一个对象实例的污染事实。
type taintState struct {
	earliestStep int
	entry        uint64
}

// guard 是争用控制的最小单元：对象实例的单个属性或单条链接实例或钩子槽位。
type guard struct{ mu sync.Mutex }

// Graph 是对象实例与链接实例组成的对象图。
type Graph struct {
	mu      sync.RWMutex
	objects map[string]*Object
	links   map[string]*Link

	guardsMu sync.Mutex
	guards   map[string]*guard

	taintMu sync.RWMutex
	taints  map[string]taintState

	// entryCounter 是全局单调的逆操作登记编号源，
	// 跨所有并发补偿流程唯一、不重复。
	entryCounter atomic.Uint64
}

// NewGraph 创建空对象图。
func NewGraph() *Graph {
	return &Graph{
		objects: map[string]*Object{},
		links:   map[string]*Link{},
		guards:  map[string]*guard{},
		taints:  map[string]taintState{},
	}
}

// nextEntry 分配一个全局唯一的逆操作登记编号。
func (g *Graph) nextEntry() uint64 { return g.entryCounter.Add(1) }

// AddObject 向对象图加入一个对象实例（幂等）。
func (g *Graph) AddObject(id string, props map[string]any) *Object {
	g.mu.Lock()
	defer g.mu.Unlock()
	if o, ok := g.objects[id]; ok {
		return o
	}
	snapshot := make(map[string]any, len(props))
	for k, v := range props {
		snapshot[k] = v
	}
	o := &Object{id: id, props: snapshot}
	g.objects[id] = o
	return o
}

func (g *Graph) object(id string) (*Object, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	o, ok := g.objects[id]
	return o, ok
}

// ObjectVersion 返回对象实例当前版本号（测试用）。
func (g *Graph) ObjectVersion(id string) (uint64, bool) {
	o, ok := g.object(id)
	if !ok {
		return 0, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.version, true
}

// ObjectClock 返回对象实例当前时钟戳（测试用）。
func (g *Graph) ObjectClock(id string) (uint64, bool) {
	o, ok := g.object(id)
	if !ok {
		return 0, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.clock, true
}

// Prop 读取单个属性值。
func (g *Graph) Prop(id, key string) (any, bool) {
	o, ok := g.object(id)
	if !ok {
		return nil, false
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	v, exists := o.props[key]
	return v, exists
}

// LinkAlive 查询链接实例是否存在且有效。
func (g *Graph) LinkAlive(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	l, ok := g.links[id]
	if !ok {
		return false
	}
	return l.alive
}

// guardKeys 返回子操作涉及的全部守卫键（属性粒度）。
func guardKeys(op SubOp) []string {
	switch op.Kind {
	case OpSetProperties:
		keys := make([]string, 0, len(op.Sets))
		for k := range op.Sets {
			keys = append(keys, "prop:"+op.ObjectID+"|"+k)
		}
		sort.Strings(keys)
		return keys
	case OpCreateLink, OpDeleteLink:
		return []string{"link:" + op.LinkID}
	case OpHook:
		return []string{"hook:" + op.ObjectID}
	default:
		return nil
	}
}

// opObjectIDs 返回子操作涉及的全部对象实例（链接类取两端实例）。
func opObjectIDs(op SubOp) []string {
	switch op.Kind {
	case OpSetProperties, OpHook:
		return []string{op.ObjectID}
	case OpCreateLink, OpDeleteLink:
		switch {
		case op.From != "" && op.To != "" && op.From != op.To:
			return []string{op.From, op.To}
		case op.From != "":
			return []string{op.From}
		default:
			return []string{op.To}
		}
	default:
		return nil
	}
}

// acquireGuards 在执行任何子操作之前一次性按全局排序获取全部守卫。
// 任一守卫已被其他动作持有（争用）则立即拒绝：已获取的守卫全部释放，
// 返回 ok=false。整个过程不触碰对象图的任何业务状态。
func (g *Graph) acquireGuards(ops []SubOp) ([]string, bool) {
	set := map[string]struct{}{}
	for _, op := range ops {
		for _, k := range guardKeys(op) {
			set[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	held := make([]string, 0, len(keys))
	for _, k := range keys {
		g.guardsMu.Lock()
		gd, ok := g.guards[k]
		if !ok {
			gd = &guard{}
			g.guards[k] = gd
		}
		g.guardsMu.Unlock()

		if !gd.mu.TryLock() {
			g.releaseGuards(held)
			return nil, false
		}
		held = append(held, k)
	}
	return keys, true
}

// releaseGuards 释放此前获取的守卫。
func (g *Graph) releaseGuards(keys []string) {
	for _, k := range keys {
		g.guardsMu.Lock()
		gd, ok := g.guards[k]
		g.guardsMu.Unlock()
		if ok {
			gd.mu.Unlock()
		}
	}
}

// Tainted 查询对象实例是否处于污染态；返回首次污染补偿中的最早失败子操作编号。
func (g *Graph) Tainted(id string) (TaintInfo, bool) {
	g.taintMu.RLock()
	defer g.taintMu.RUnlock()
	s, ok := g.taints[id]
	if !ok {
		return TaintInfo{}, false
	}
	return TaintInfo{ObjectID: id, EarliestStep: s.earliestStep, Entry: s.entry}, true
}

// firstTainted 返回候选实例集合中第一个处于污染态的实例（按 ID 排序，保证确定性）。
func (g *Graph) firstTainted(ids []string) (TaintInfo, bool) {
	uniq := map[string]struct{}{}
	for _, id := range ids {
		if id != "" {
			uniq[id] = struct{}{}
		}
	}
	sorted := make([]string, 0, len(uniq))
	for id := range uniq {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		if info, ok := g.Tainted(id); ok {
			return info, true
		}
	}
	return TaintInfo{}, false
}

// commitTaints 依据一次补偿的失败登记写入污染标记。
// 已经污染的实例永不覆盖：保留的永远是其第一次被污染的那次补偿里最早失败的子操作编号。
func (g *Graph) commitTaints(failed []CompRecord) []TaintInfo {
	earliest := map[string]CompRecord{}
	for _, rec := range failed {
		if rec.Skipped {
			continue // 跳过环节不是任何对象的补偿失败，不产生污染
		}
		for _, id := range rec.ObjectIDs {
			prev, ok := earliest[id]
			if !ok || rec.Index < prev.Index {
				earliest[id] = rec
			}
		}
	}

	g.taintMu.Lock()
	defer g.taintMu.Unlock()
	var added []TaintInfo
	ids := make([]string, 0, len(earliest))
	for id := range earliest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, exists := g.taints[id]; exists {
			continue
		}
		rec := earliest[id]
		st := taintState{earliestStep: rec.Index, entry: rec.Entry}
		g.taints[id] = st
		added = append(added, TaintInfo{ObjectID: id, EarliestStep: rec.Index, Entry: rec.Entry})
	}
	return added
}
