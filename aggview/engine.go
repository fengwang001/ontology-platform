package aggview

import (
	"fmt"

	"ontology/ontology"
)

// Result 是某个起点实例的聚合结果；Absent 为 true 表示明确的"不存在"
// 状态（当前无法到达任何终点实例），而非任意默认数值。
type Result struct {
	Absent bool
	Max    float64
}

// Affected 记录一次变更影响到的起点集合及判定依据，供日志与测试检查。
type Affected struct {
	Sources []string
	Reason  string
}

// LogEntry 是一次被接受变更的结构化日志。
type LogEntry struct {
	Op       string
	Detail   string
	Affected []Affected
}

// view 是一个已注册聚合视图的全部运行时状态。
type view struct {
	path  *viewPath
	state *aggState
	open  bool // 声明后类型集合是否被结构性扩展过（影响环检查策略）
	fault bool // 测试注入：令下一次该视图的增量维护失败，验证整体回滚
}

// Engine 聚合视图子系统。所有变更在 store 的同一把写锁内串行完成，
// 因而并发的链接增删与属性写入对每个起点实例都等价于某个串行顺序。
type Engine struct {
	store *ontology.Store
	views map[string]*view
	log   []LogEntry
}

func NewEngine(store *ontology.Store) *Engine {
	return &Engine{store: store, views: map[string]*view{}, log: nil}
}

// DeclareView 声明一个聚合视图。
func (e *Engine) DeclareView(d PathDecl) *ontology.AggregateError {
	if err := validateDecl(d, e.store); err != nil {
		return err
	}
	e.store.Lock()
	defer e.store.Unlock()
	if _, dup := e.views[d.Name]; dup {
		return ontology.NewTypeMismatchError(fmt.Sprintf("view %q already declared", d.Name))
	}
	v := &view{path: newViewPath(d), state: newAggState()}
	e.views[d.Name] = v
	e.initViewLocked(v)
	return nil
}

// AddHopType 为某一跳的允许类型集合结构性新增成员。
func (e *Engine) AddHopType(viewName string, hopIndex int, typ string) *ontology.AggregateError {
	e.store.Lock()
	defer e.store.Unlock()
	v, ok := e.views[viewName]
	if !ok {
		return ontology.NewInstanceNotFoundError(fmt.Sprintf("view %q not found", viewName))
	}
	if hopIndex < 0 || hopIndex >= v.path.Len() {
		return ontology.NewTypeMismatchError(fmt.Sprintf("hop index %d out of range", hopIndex))
	}
	if !e.store.HasTypeLocked(typ) {
		return ontology.NewTypeMismatchError(fmt.Sprintf("type %q not registered", typ))
	}
	if v.path.allowsType(hopIndex, typ) {
		return nil // 幂等
	}
	v.path.hops[hopIndex].types[typ] = struct{}{}
	v.open = true
	// 结构扩展后既有连通关系仍然有效；只需为"新变为可达"的起点补建状态。
	affected := e.recomputeAllLocked(v)
	e.appendLog("add_hop_type",
		fmt.Sprintf("view=%s hop=%d type=%s", viewName, hopIndex, typ), affected)
	return nil
}

// Query 返回某起点实例当前的聚合结果；只读，不修改任何聚合状态。
func (e *Engine) Query(viewName, sourceID string) (Result, *ontology.AggregateError) {
	e.store.RLock()
	defer e.store.RUnlock()
	v, ok := e.views[viewName]
	if !ok {
		return Result{}, ontology.NewInstanceNotFoundError(fmt.Sprintf("view %q not found", viewName))
	}
	if t, ok := e.store.ObjectTypeOfLocked(sourceID); !ok {
		return Result{}, ontology.NewInstanceNotFoundError(fmt.Sprintf("source instance %q not found", sourceID))
	} else if t != v.path.source {
		return Result{}, ontology.NewTypeMismatchError(fmt.Sprintf("instance %q is not of source type %q", sourceID, v.path.source))
	}
	return v.state.get(sourceID), nil
}

// neighbors 实现 graph：按关系与允许类型集合展开一跳邻接并按终点去重。
func (e *Engine) neighbors(from, rel string, allowed map[string]struct{}) []string {
	seen := map[string]struct{}{}
	var res []string
	for _, l := range e.store.OutgoingLocked(from) {
		if l.Rel != rel {
			continue
		}
		if _, ok := allowed[e.typeOf(l.To)]; !ok {
			continue
		}
		if _, dup := seen[l.To]; dup {
			continue
		}
		seen[l.To] = struct{}{}
		res = append(res, l.To)
	}
	return res
}

// reverseNeighbors 实现 reverseGraph：求指向 to 且关系匹配的链接起点
// （去重）。层次类型过滤由 sourcesReaching 经 levelTypeOK 统一完成。
func (e *Engine) reverseNeighbors(to, rel string) []string {
	seen := map[string]struct{}{}
	var res []string
	for _, l := range e.store.IncomingLocked(to) {
		if l.Rel != rel {
			continue
		}
		if _, dup := seen[l.From]; dup {
			continue
		}
		seen[l.From] = struct{}{}
		res = append(res, l.From)
	}
	return res
}

// levelTypeOK 返回一个判定器：实例 id 是否能位于路径第 level 层
// （level 0 为起点类型，level j>0 为第 j-1 跳允许的终点类型集合）。
func (e *Engine) levelTypeOK(p *viewPath) func(level int, id string) bool {
	return func(level int, id string) bool {
		t, ok := e.store.ObjectTypeOfLocked(id)
		if !ok {
			return false
		}
		if level == 0 {
			return t == p.source
		}
		return p.allowsType(level-1, t)
	}
}

func (e *Engine) typeOf(id string) string {
	t, _ := e.store.ObjectTypeOfLocked(id)
	return t
}

func (e *Engine) appendLog(op, detail string, affected []Affected) {
	e.log = append(e.log, LogEntry{Op: op, Detail: detail, Affected: affected})
}

// Log 返回日志副本。
func (e *Engine) Log() []LogEntry {
	e.store.RLock()
	defer e.store.RUnlock()
	cp := make([]LogEntry, len(e.log))
	copy(cp, e.log)
	return cp
}

// InjectFault 令指定视图的下一次增量维护失败（仅用于测试回滚）。
func (e *Engine) InjectFault(viewName string) {
	e.store.Lock()
	defer e.store.Unlock()
	if v, ok := e.views[viewName]; ok {
		v.fault = true
	}
}
