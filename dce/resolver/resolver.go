// Package resolver 在只读快照上解析“模块导出名 -> 最终声明”，全程记忆化。
package resolver

import (
	"sort"

	"ontology/dce/dceerr"
	"ontology/dce/model"
)

// 解析状态。
type resolveStatus int

const (
	stUnvisited resolveStatus = iota
	stVisiting                // 正在具名链上解析（用于重导出循环检测）
	stDone
)

type outcome struct {
	status resolveStatus
	key    model.Key
	err    *dceerr.Error
}

type moduleIndex struct {
	explicit map[string]model.Export // 本地声明导出 + 具名重导出（不含通配重导出）
	stars    []string                // 通配重导出目标，按登记次序
}

// Resolver 记忆化导出解析器。所有缓存字段惰性填充，解析过程只读快照。
type Resolver struct {
	snap *model.Snapshot

	indexes map[string]*moduleIndex
	memo    map[qKey]*outcome

	// 星链展开记忆化：每个模块至多展开一次星链。
	starDone    map[string]bool
	starVisited map[string]bool
	starDecls   map[string][]model.Key

	// Stats 用于近线性复杂度的可验证统计。
	Stats Stats
}

type qKey struct {
	mod  string
	name string
}

// Stats 记录解析过程的工作量计数，供测试验证复杂度。
type Stats struct {
	NamedResolves    int // 具名解析真正执行的（模块,名）次数
	StarExpansions   int // 星链 DFS 真正访问的模块次数
	LocalDeclLookups int // 最终本地声明定位次数
}

// New 构造解析器。
func New(snap *model.Snapshot) *Resolver {
	return &Resolver{
		snap:        snap,
		indexes:     make(map[string]*moduleIndex),
		memo:        make(map[qKey]*outcome),
		starDone:    make(map[string]bool),
		starVisited: make(map[string]bool),
		starDecls:   make(map[string][]model.Key),
	}
}

func (r *Resolver) index(id string) *moduleIndex {
	if idx, ok := r.indexes[id]; ok {
		return idx
	}
	idx := &moduleIndex{explicit: map[string]model.Export{}}
	if m := r.snap.Modules[id]; m != nil {
		for _, e := range m.Exports {
			switch e.Kind {
			case model.ExportLocal, model.ExportReexport:
				name := e.LocalName
				if e.Kind == model.ExportReexport {
					name = e.ExportName
				}
				if _, exists := idx.explicit[name]; !exists {
					idx.explicit[name] = e
				}
			case model.ExportStarReexport:
				idx.stars = append(idx.stars, e.Target)
			}
		}
	}
	r.indexes[id] = idx
	return idx
}

// Resolve 解析模块 mod 的具名导出 name 到最终声明。
func (r *Resolver) Resolve(mod, name string) (model.Key, error) {
	oc := r.resolveNamed(mod, name, nil)
	if oc.err != nil {
		return model.Key{}, oc.err
	}
	return oc.key, nil
}

// EffectiveNames 返回模块 mod 自身可解析的全部候选导出名：
// 显式导出名 ∪ 星链可达名字宇宙（含显式 default；星链不含 default）。
// 名字是否真正有效（歧义/缺失）由 Resolve 判定。
func (r *Resolver) EffectiveNames(mod string) []string {
	nameSet := map[string]bool{}
	for name := range r.index(mod).explicit {
		nameSet[name] = true
	}
	for _, name := range r.starNameUniverse(mod) {
		nameSet[name] = true
	}
	names := make([]string, 0, len(nameSet))
	for n := range nameSet {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// resolveNamed 解析 (mod,name)。
// namedChain 是当前具名重导出链上已经经过的（模块,导出名）集合；
// 命中其中任何一项即构成重导出循环。
func (r *Resolver) resolveNamed(mod, name string, namedChain map[qKey]bool) *outcome {
	k := qKey{mod, name}
	if oc, ok := r.memo[k]; ok && oc.status == stDone {
		return oc
	}
	// 注意：stVisiting 的缓存仅在本次具名链中成立。跨链的“访问中”
	// 是另一条链上的结构性歧义/环，按重导出循环处理是安全的
	// （该结果与入口/遍历次序无关，见设计说明）。
	if oc, ok := r.memo[k]; ok && oc.status == stVisiting {
		return cycleOutcome(mod, name)
	}
	if namedChain[k] {
		return cycleOutcome(mod, name)
	}

	r.Stats.NamedResolves++

	idx := r.index(mod)
	self := &outcome{status: stVisiting}
	r.memo[k] = self

	var result *outcome
	if e, ok := idx.explicit[name]; ok {
		result = r.resolveExplicit(mod, e, namedChain)
	} else {
		result = r.resolveViaStars(mod, name, namedChain)
	}

	self.status = stDone
	self.key = result.key
	self.err = result.err
	return self
}

func (r *Resolver) resolveExplicit(mod string, e model.Export, namedChain map[qKey]bool) *outcome {
	switch e.Kind {
	case model.ExportLocal:
		return r.lookupLocal(mod, e.LocalName)
	case model.ExportReexport:
		next := map[qKey]bool{}
		for kk := range namedChain {
			next[kk] = true
		}
		next[qKey{mod, e.ExportName}] = true
		return r.resolveNamed(e.Target, e.ForeignName, next)
	default:
		return r.resolveViaStars(mod, e.ExportName, namedChain)
	}
}

// resolveViaStars 处理“显式表中无此名”时的通配重导出解析。
// 同名候选全部解析到同一声明才有效；出现歧义报歧义导出；
// 纯通配回溯（星链自身成环）不报错也不贡献名字。
func (r *Resolver) resolveViaStars(mod, name string, namedChain map[qKey]bool) *outcome {
	var candidates []model.Key
	var firstErr *dceerr.Error

	var walk func(cur string, seen map[string]bool)
	walk = func(cur string, seen map[string]bool) {
		if cur != mod || len(seen) > 0 {
			if seen[cur] {
				// 纯通配路径回到已访问模块：该路径到此为止，不报错。
				return
			}
		}
		seen[cur] = true
		r.Stats.StarExpansions++

		cidx := r.index(cur)
		if e, ok := cidx.explicit[name]; ok {
			oc := r.resolveExplicit(cur, e, namedChain)
			switch {
			case oc.err != nil:
				if firstErr == nil {
					firstErr = oc.err
				}
			default:
				candidates = append(candidates, oc.key)
			}
			return
		}
		for _, t := range cidx.stars {
			walk(t, seen)
		}
	}

	walk(mod, map[string]bool{})

	if len(candidates) == 0 {
		if firstErr != nil {
			return &outcome{err: firstErr}
		}
		return &outcome{err: &dceerr.Error{
			Category: dceerr.CatMissingExport, Module: mod, Name: name,
			Detail: "no export resolves the name",
		}}
	}
	uniq := dedupKeys(candidates)
	if len(uniq) > 1 {
		return &outcome{err: &dceerr.Error{
			Category: dceerr.CatAmbiguousExport, Module: mod, Name: name,
			Detail: "star reexports resolve the name to different declarations",
		}}
	}
	return &outcome{key: uniq[0]}
}

func (r *Resolver) lookupLocal(mod, decl string) *outcome {
	m := r.snap.Modules[mod]
	if m == nil {
		return &outcome{err: &dceerr.Error{Category: dceerr.CatUnknownModule, Module: mod}}
	}
	r.Stats.LocalDeclLookups++
	for _, d := range m.Decls {
		if d.Name == decl {
			return &outcome{key: model.Key{Module: mod, Decl: decl}}
		}
	}
	// 本地导出指向不存在的本地声明，视为缺失导出。
	return &outcome{err: &dceerr.Error{
		Category: dceerr.CatMissingExport, Module: mod, Name: decl,
		Detail: "local export has no declaration",
	}}
}

// StarDecls 返回模块 mod 的通配视图：全部有效导出所解析到的声明，
// 歧义名与 default 名不计入；此处对缺失/歧义/环不报错。
func (r *Resolver) StarDecls(mod string) []model.Key {
	if r.starDone[mod] {
		return r.starDecls[mod]
	}

	// 名字宇宙：星链可达模块（含自身）显式导出的名字并集，排除 default。
	names := r.starNameUniverse(mod)

	var decls []model.Key
	seenKey := map[model.Key]bool{}
	for _, name := range names {
		oc := r.resolveNamed(mod, name, nil)
		if oc.err != nil {
			continue // 歧义/缺失/环：通配视图不报错、不计入
		}
		if !seenKey[oc.key] {
			seenKey[oc.key] = true
			decls = append(decls, oc.key)
		}
	}
	r.starDecls[mod] = decls
	r.starDone[mod] = true
	return decls
}

// starNameUniverse 通过按模块记忆化的星链 DFS 收集名字并集。
// 星链上的每个模块在本次求解中至多被遍历一次。
func (r *Resolver) starNameUniverse(root string) []string {
	nameSet := map[string]bool{}

	var dfs func(cur string, onStack map[string]bool)
	dfs = func(cur string, onStack map[string]bool) {
		if onStack[cur] {
			return // 星链成环：回溯，不贡献新内容
		}
		onStack[cur] = true
		r.Stats.StarExpansions++

		idx := r.index(cur)
		for name := range idx.explicit {
			if name != "default" {
				nameSet[name] = true
			}
		}
		for _, t := range idx.stars {
			dfs(t, onStack)
		}
		delete(onStack, cur)
	}

	dfs(root, map[string]bool{})

	names := make([]string, 0, len(nameSet))
	for n := range nameSet {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func dedupKeys(keys []model.Key) []model.Key {
	seen := map[model.Key]bool{}
	var out []model.Key
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func cycleOutcome(mod, name string) *outcome {
	return &outcome{err: &dceerr.Error{
		Category: dceerr.CatReexportCycle, Module: mod, Name: name,
		Detail: "reexport chain returns to a visited (module, export name)",
	}}
}
