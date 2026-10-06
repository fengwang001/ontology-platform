// Package analyzer 在快照上求保留规则的最小不动点，并产出保留结果。
package analyzer

import (
	"sort"

	"ontology/dce/dceerr"
	"ontology/dce/model"
	"ontology/dce/resolver"
)

// Reason 是保留原因类别。
type Reason int

const (
	ReasonEntry      Reason = iota + 1 // 入口导出
	ReasonReferenced                   // 被引用
	ReasonSideEffect                   // 副作用
)

func (r Reason) String() string {
	switch r {
	case ReasonEntry:
		return "entry-export"
	case ReasonReferenced:
		return "referenced"
	case ReasonSideEffect:
		return "side-effect"
	default:
		return "unknown"
	}
}

// KeptDecl 是一个保留声明及其（优先级最高的）保留原因。
type KeptDecl struct {
	Key    model.Key
	Reason Reason
}

// Stats 是裁剪分析自身的工作量计数。
type Stats struct {
	DeclProcessed  int // 被真正处理的（模块,声明）数量（每个至多一次）
	ModulesScanned int // 被扫描导入的模块数量（每个被引入模块恰一次）
}

// Result 是求解结果。
type Result struct {
	Kept     []KeptDecl // 按（模块、声明）升序
	Included []string   // 被引入模块，按标识升序

	Stats         Stats
	ResolverStats resolver.Stats
}

// Option 配置一次求解。
type Option func(*config)

type config struct {
	logger Logger
}

// WithLogger 注入日志器。
func WithLogger(l Logger) Option {
	return func(c *config) { c.logger = l }
}

type engine struct {
	snap *model.Snapshot
	res  *resolver.Resolver
	log  Logger

	included map[string]bool
	keep     map[model.Key]Reason

	declQueue []model.Key
	declEnq   map[model.Key]bool // 每个（模块,声明）至多入队一次
	modQueue  []string
	modSeen   map[string]bool // 每个被引入模块的导入只扫描一次

	stats Stats
}

// Analyze 对快照与入口集合求保留规则的最小不动点。
// 前置条件：构造期检查已通过；entries 中所有模块均存在（未知入口由调用方先报类别 3）。
func Analyze(snap *model.Snapshot, entries []string, opts ...Option) (*Result, error) {
	cfg := config{logger: NopLogger{}}
	for _, o := range opts {
		o(&cfg)
	}
	cfg.logger.LogEntry(append([]string(nil), entries...))

	e := &engine{
		snap:     snap,
		res:      resolver.New(snap),
		log:      cfg.logger,
		included: map[string]bool{},
		keep:     map[model.Key]Reason{},
		declEnq:  map[model.Key]bool{},
		modSeen:  map[string]bool{},
	}

	for _, en := range dedupSorted(entries) {
		e.includeModule(en, "entry")
		e.markEntryExports(en)
	}
	e.fixpoint()

	if err := e.checkBindings(); err != nil {
		return nil, err
	}
	return e.result(), nil
}

func (e *engine) includeModule(id, why string) {
	if e.included[id] {
		return
	}
	e.included[id] = true
	e.log.LogModuleIncluded(id, why)
	if !e.modSeen[id] {
		e.modSeen[id] = true
		e.modQueue = append(e.modQueue, id)
	}
}

// markKeep 记录保留声明及原因；原因优先级 入口导出 < 被引用 < 副作用（数值越小越优先）。
func (e *engine) markKeep(k model.Key, reason Reason, because string) {
	if old, ok := e.keep[k]; !ok || reason < old {
		e.keep[k] = reason
		e.log.LogDeclKept(k.Module, k.Decl, reason, because)
	}
	// “无副作用”模块只有在声明因入口/被引用而保留时才被引入。
	if m := e.snap.Modules[k.Module]; m != nil &&
		!m.SideEffect.HasSideEffects() && reason != ReasonSideEffect {
		e.includeModule(k.Module, "referenced decl in side-effect-free module")
	}
	if !e.declEnq[k] {
		e.declEnq[k] = true
		e.declQueue = append(e.declQueue, k)
	}
}

func (e *engine) markEntryExports(mod string) {
	for _, name := range e.res.EffectiveNames(mod) {
		k, err := e.res.Resolve(mod, name)
		if err != nil {
			continue // 入口只保留“有效导出”；歧义/缺失/环名不是有效导出
		}
		e.markKeep(k, ReasonEntry, "entry export "+name)
	}
}

func (e *engine) fixpoint() {
	for len(e.declQueue) > 0 || len(e.modQueue) > 0 {
		for len(e.modQueue) > 0 {
			id := e.modQueue[0]
			e.modQueue = e.modQueue[1:]
			e.scanModuleImports(id)
		}
		for len(e.declQueue) > 0 {
			k := e.declQueue[0]
			e.declQueue = e.declQueue[1:]
			e.stats.DeclProcessed++
			e.processDecl(k)
		}
	}
}

// scanModuleImports 处理被引入模块的全部导入语句（含纯副作用导入）。
func (e *engine) scanModuleImports(id string) {
	e.stats.ModulesScanned++
	m := e.snap.Modules[id]
	if m == nil {
		return
	}
	for _, imp := range m.Imports {
		target := e.snap.Modules[imp.Target]
		if target == nil {
			continue // 未知模块属于构造期错误，先于解析检查
		}
		if target.SideEffect.HasSideEffects() {
			e.includeModule(imp.Target, "imported module with side effects")
			for _, d := range target.Decls {
				if d.SideEffect {
					e.markKeep(model.Key{Module: imp.Target, Decl: d.Name},
						ReasonSideEffect, "side-effectful decl in side-effectful imported module")
				}
			}
		}
		// 引用型绑定即便目标无副作用也会驱动保留与引入。
		for _, b := range imp.Bindings {
			e.applyBinding(imp.Target, b)
		}
	}
}

// applyBinding 按导入绑定在目标模块解析并驱动保留；无法解析不产生引用。
func (e *engine) applyBinding(target string, b model.ImportBinding) {
	if b.Imported == "*" {
		for _, k := range e.res.StarDecls(target) {
			e.markKeep(k, ReasonReferenced, "namespace import "+b.Local)
		}
		return
	}
	k, err := e.res.Resolve(target, b.Imported)
	if err != nil {
		return // 无法解析：不产生引用；错误在 checkBindings 统一报告
	}
	e.markKeep(k, ReasonReferenced, "import binding "+b.Local+" from "+target)
}

func (e *engine) processDecl(k model.Key) {
	m := e.snap.Modules[k.Module]
	if m == nil {
		return
	}
	var decl *model.Declaration
	for i := range m.Decls {
		if m.Decls[i].Name == k.Decl {
			decl = &m.Decls[i]
			break
		}
	}
	if decl == nil {
		return
	}
	for _, ref := range decl.Refs {
		if e.localDecl(k.Module, ref) {
			e.markKeep(model.Key{Module: k.Module, Decl: ref},
				ReasonReferenced, "local ref from "+k.Decl)
			continue
		}
		if imp, b, ok := e.findBinding(k.Module, ref); ok {
			e.applyBinding(imp.Target, b)
		}
	}
}

func (e *engine) localDecl(mod, name string) bool {
	m := e.snap.Modules[mod]
	if m == nil {
		return false
	}
	for _, d := range m.Decls {
		if d.Name == name {
			return true
		}
	}
	return false
}

func (e *engine) findBinding(mod, local string) (model.Import, model.ImportBinding, bool) {
	m := e.snap.Modules[mod]
	if m == nil {
		return model.Import{}, model.ImportBinding{}, false
	}
	for _, imp := range m.Imports {
		for _, b := range imp.Bindings {
			if b.Local == local {
				return imp, b, true
			}
		}
	}
	return model.Import{}, model.ImportBinding{}, false
}

// checkBindings 对全部被引入模块的全部导入绑定做解析检查。
// 按（模块标识升序，绑定登记次序）报告第一处；通配绑定不检查。
func (e *engine) checkBindings() *dceerr.Error {
	ids := make([]string, 0, len(e.included))
	for id := range e.included {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := e.snap.Modules[id]
		if m == nil {
			continue
		}
		for _, imp := range m.Imports {
			for _, b := range imp.Bindings {
				if b.Imported == "*" {
					continue
				}
				if _, err := e.res.Resolve(imp.Target, b.Imported); err != nil {
					if de, ok := dceerr.As(err); ok {
						e.log.LogBindError(id, imp.Target, b.Imported, err)
						return de
					}
				}
			}
		}
	}
	return nil
}

func (e *engine) result() *Result {
	res := &Result{Stats: e.stats, ResolverStats: e.res.Stats}
	for k, reason := range e.keep {
		res.Kept = append(res.Kept, KeptDecl{Key: k, Reason: reason})
	}
	sort.Slice(res.Kept, func(i, j int) bool {
		if res.Kept[i].Key.Module != res.Kept[j].Key.Module {
			return res.Kept[i].Key.Module < res.Kept[j].Key.Module
		}
		return res.Kept[i].Key.Decl < res.Kept[j].Key.Decl
	})
	for id := range e.included {
		res.Included = append(res.Included, id)
	}
	sort.Strings(res.Included)
	e.log.LogResult(res.Kept, res.Included)
	return res
}

func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
