package dce_test

import (
	"fmt"
	"sort"
	"strings"

	"ontology/dce/dceerr"
	"ontology/dce/model"
)

// naiveResult 是独立于产品代码、按题目规则直接迭代到不动点的参考结果。
type naiveResult struct {
	included map[string]bool
	kept     map[model.Key]dceReason
	errCat   dceerr.Category
}

type dceReason int

const (
	rEntry dceReason = iota
	rReferenced
	rSideEffect
)

func reasonString(r dceReason) string {
	return []string{"entry-export", "referenced", "side-effect"}[r]
}

type pair struct{ m, n string }

// naiveResolve 直接按规则递归：显式优先；否则沿星链收集同名候选。
func naiveResolve(mods map[string]*model.Module, mod, name string) (model.Key, *dceerr.Error) {
	visiting := map[pair]bool{}
	var rec func(m, n string) (model.Key, *dceerr.Error)
	rec = func(m, n string) (model.Key, *dceerr.Error) {
		cur := mods[m]
		if cur == nil {
			return model.Key{}, &dceerr.Error{Category: dceerr.CatUnknownModule, Module: m}
		}
		if visiting[pair{m, n}] {
			return model.Key{}, &dceerr.Error{Category: dceerr.CatReexportCycle, Module: m, Name: n}
		}
		visiting[pair{m, n}] = true
		defer delete(visiting, pair{m, n})

		for _, e := range cur.Exports {
			if e.Kind == model.ExportLocal && e.LocalName == n {
				for _, d := range cur.Decls {
					if d.Name == e.LocalName {
						return model.Key{Module: m, Decl: d.Name}, nil
					}
				}
				return model.Key{}, &dceerr.Error{Category: dceerr.CatMissingExport, Module: m, Name: n}
			}
			if e.Kind == model.ExportReexport && e.ExportName == n {
				return rec(e.Target, e.ForeignName)
			}
		}

		// 星链 DFS 收集同名候选；星链回到已访问模块则该路径终止。
		var cand []model.Key
		var firstErr *dceerr.Error
		seenMod := map[string]bool{}
		var starWalk func(m2 string)
		starWalk = func(m2 string) {
			if seenMod[m2] {
				return
			}
			seenMod[m2] = true
			c2 := mods[m2]
			for _, e := range c2.Exports {
				if e.Kind == model.ExportLocal && e.LocalName == n {
					if k, err := rec(m2, n); err != nil {
						if firstErr == nil {
							firstErr = err
						}
					} else {
						cand = append(cand, k)
					}
					return
				}
				if e.Kind == model.ExportReexport && e.ExportName == n {
					if k, err := rec(m2, n); err != nil {
						if firstErr == nil {
							firstErr = err
						}
					} else {
						cand = append(cand, k)
					}
					return
				}
			}
			for _, e := range c2.Exports {
				if e.Kind == model.ExportStarReexport {
					starWalk(e.Target)
				}
			}
		}
		starWalk(m)

		if len(cand) == 0 {
			if firstErr != nil {
				return model.Key{}, firstErr
			}
			return model.Key{}, &dceerr.Error{Category: dceerr.CatMissingExport, Module: m, Name: n}
		}
		u := map[model.Key]bool{}
		for _, k := range cand {
			u[k] = true
		}
		if len(u) > 1 {
			return model.Key{}, &dceerr.Error{Category: dceerr.CatAmbiguousExport, Module: m, Name: n}
		}
		return cand[0], nil
	}
	return rec(mod, name)
}

// naiveStarDecls 通配视图：星链可达模块显式名并集（不含 default），
// 逐个在根模块解析，成功且去重的计入，任何错误都跳过。
func naiveStarDecls(mods map[string]*model.Module, mod string) []model.Key {
	names := map[string]bool{}
	on := map[string]bool{}
	var dfs func(m string)
	dfs = func(m string) {
		if on[m] {
			return
		}
		on[m] = true
		for _, e := range mods[m].Exports {
			if (e.Kind == model.ExportLocal || e.Kind == model.ExportReexport) &&
				!(e.Kind == model.ExportLocal && e.LocalName == "default") {
				n := e.LocalName
				if e.Kind == model.ExportReexport {
					n = e.ExportName
				}
				if n != "default" {
					names[n] = true
				}
			}
		}
		for _, e := range mods[m].Exports {
			if e.Kind == model.ExportStarReexport {
				dfs(e.Target)
			}
		}
		delete(on, m)
	}
	dfs(mod)

	var out []model.Key
	seen := map[model.Key]bool{}
	for n := range names {
		if k, err := naiveResolve(mods, mod, n); err == nil && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].Decl < out[j].Decl
	})
	return out
}

func naiveEffectiveNames(mods map[string]*model.Module, mod string) []string {
	names := map[string]bool{}
	for _, e := range mods[mod].Exports {
		if e.Kind == model.ExportLocal {
			names[e.LocalName] = true
		} else if e.Kind == model.ExportReexport {
			names[e.ExportName] = true
		}
	}
	// 星链名字宇宙
	on := map[string]bool{}
	var dfs func(m string)
	dfs = func(m string) {
		if on[m] {
			return
		}
		on[m] = true
		for _, e := range mods[m].Exports {
			if e.Kind == model.ExportLocal {
				names[e.LocalName] = true
			} else if e.Kind == model.ExportReexport {
				names[e.ExportName] = true
			}
		}
		for _, e := range mods[m].Exports {
			if e.Kind == model.ExportStarReexport {
				dfs(e.Target)
			}
		}
		delete(on, m)
	}
	dfs(mod)
	var out []string
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// naiveSolve 反复迭代直到包含集合与保留集合不再变化。
func naiveSolve(mods map[string]*model.Module, entries []string) naiveResult {
	res := naiveResult{included: map[string]bool{}, kept: map[model.Key]dceReason{}}

	binding := func(mod, local string) (model.Import, model.ImportBinding, bool) {
		for _, imp := range mods[mod].Imports {
			for _, b := range imp.Bindings {
				if b.Local == local {
					return imp, b, true
				}
			}
		}
		return model.Import{}, model.ImportBinding{}, false
	}

	mark := func(k model.Key, r dceReason) {
		if old, ok := res.kept[k]; !ok || r < old {
			res.kept[k] = r
		}
		if m := mods[k.Module]; m != nil && !m.SideEffect.HasSideEffects() && r != rSideEffect {
			res.included[k.Module] = true
		}
	}

	processed := map[model.Key]bool{}

	// 反复整体扫描直到包含集合、保留集合及原因都不再变化（朴素不动点）。
	// 显式按“新出现的包含模块 / 新保留的声明”推进，避免漏扫既有模块中的新声明。
	snapshot := func() (map[string]bool, map[model.Key]dceReason) {
		ic := map[string]bool{}
		for k, v := range res.included {
			ic[k] = v
		}
		kp := map[model.Key]dceReason{}
		for k, v := range res.kept {
			kp[k] = v
		}
		return ic, kp
	}

	for {
		ic, kp := snapshot()
		for _, en := range entries {
			res.included[en] = true
		}

		ids := mapKeys(res.included)
		sort.Strings(ids)
		for _, id := range ids {
			m := mods[id]
			if inStrings(entries, id) {
				for _, n := range naiveEffectiveNames(mods, id) {
					if k, err := naiveResolve(mods, id, n); err == nil {
						mark(k, rEntry)
					}
				}
			}
			for _, imp := range m.Imports {
				t := mods[imp.Target]
				if t.SideEffect.HasSideEffects() {
					res.included[imp.Target] = true
					for _, d := range t.Decls {
						if d.SideEffect {
							mark(model.Key{Module: imp.Target, Decl: d.Name}, rSideEffect)
						}
					}
				}
				for _, b := range imp.Bindings {
					if b.Imported == "*" {
						for _, k := range naiveStarDecls(mods, imp.Target) {
							mark(k, rReferenced)
						}
						continue
					}
					if k, err := naiveResolve(mods, imp.Target, b.Imported); err == nil {
						mark(k, rReferenced)
					}
				}
			}
		}

		// 每个被保留声明只扫描一次引用（原因升级不改变其引用闭包）。
		var keys []model.Key
		for k := range res.kept {
			if !processed[k] {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Module != keys[j].Module {
				return keys[i].Module < keys[j].Module
			}
			return keys[i].Decl < keys[j].Decl
		})
		for _, k := range keys {
			processed[k] = true
			m := mods[k.Module]
			var d *model.Declaration
			for i := range m.Decls {
				if m.Decls[i].Name == k.Decl {
					d = &m.Decls[i]
				}
			}
			if d == nil {
				continue
			}
			for _, ref := range d.Refs {
				isDecl := false
				for _, d2 := range m.Decls {
					if d2.Name == ref {
						isDecl = true
					}
				}
				if isDecl {
					mark(model.Key{Module: k.Module, Decl: ref}, rReferenced)
				} else if imp, b, ok := binding(k.Module, ref); ok {
					if b.Imported == "*" {
						for _, k2 := range naiveStarDecls(mods, imp.Target) {
							mark(k2, rReferenced)
						}
					} else if k2, err := naiveResolve(mods, imp.Target, b.Imported); err == nil {
						mark(k2, rReferenced)
					}
				}
			}
		}

		// 终止判定：重算前后状态一致。
		sameInc := len(ic) == len(res.included)
		sameKept := len(kp) == len(res.kept)
		reasonSame := true
		for k, v := range kp {
			if res.kept[k] != v {
				reasonSame = false
			}
		}
		if sameInc && sameKept && reasonSame {
			break
		}
	}

	// 绑定解析检查：模块标识升序、登记次序第一个错误。
	ids := mapKeys(res.included)
	sort.Strings(ids)
	for _, id := range ids {
		for _, imp := range mods[id].Imports {
			for _, b := range imp.Bindings {
				if b.Imported == "*" {
					continue
				}
				if _, err := naiveResolve(mods, imp.Target, b.Imported); err != nil {
					res.errCat = err.Category
					return res
				}
			}
		}
	}
	return res
}

func mapKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func inStrings(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func dumpGraph(mods map[string]*model.Module, entries []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "entries=%v\n", entries)
	ids := make([]string, 0, len(mods))
	for id := range mods {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := mods[id]
		fmt.Fprintf(&b, "module %s se=%v\n", id, m.SideEffect)
		for _, d := range m.Decls {
			fmt.Fprintf(&b, "  decl %s side=%v refs=%v\n", d.Name, d.SideEffect, d.Refs)
		}
		for _, imp := range m.Imports {
			fmt.Fprintf(&b, "  import %s bindings=%v\n", imp.Target, imp.Bindings)
		}
		for _, e := range m.Exports {
			fmt.Fprintf(&b, "  export %+v\n", e)
		}
	}
	return b.String()
}
