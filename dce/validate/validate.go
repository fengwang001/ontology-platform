// Package validate 负责构造期检查（非法参数、重复模块、未知模块、未定义引用）。
package validate

import (
	"sort"

	"ontology/dce/dceerr"
	"ontology/dce/model"
)

// CheckModule 对单个待登记模块做类别 1（非法参数）检查：
// 空标识、同一模块内声明名重复、同一模块内导入本地名重复、同一模块内导出名重复。
// 登记次序内命中第一处即报告。
func CheckModule(m *model.Module) error {
	if m.ID == "" {
		return &dceerr.Error{Category: dceerr.CatInvalidArgument, Module: m.ID, Detail: "empty module id"}
	}

	declNames := make(map[string]bool, len(m.Decls))
	for _, d := range m.Decls {
		if declNames[d.Name] {
			return &dceerr.Error{Category: dceerr.CatInvalidArgument, Module: m.ID, Name: d.Name, Detail: "duplicate declaration name"}
		}
		declNames[d.Name] = true
	}

	localNames := make(map[string]bool)
	for _, imp := range m.Imports {
		for _, b := range imp.Bindings {
			if b.Local == "" {
				return &dceerr.Error{
					Category: dceerr.CatInvalidArgument, Module: m.ID, Name: b.Local,
					Detail: "empty import local name",
				}
			}
			if localNames[b.Local] {
				return &dceerr.Error{
					Category: dceerr.CatInvalidArgument, Module: m.ID, Name: b.Local,
					Detail: "duplicate import local name",
				}
			}
			localNames[b.Local] = true
		}
	}

	exportNames := make(map[string]bool)
	for _, e := range m.Exports {
		var name string
		switch e.Kind {
		case model.ExportLocal:
			name = e.LocalName
		case model.ExportReexport:
			name = e.ExportName
		case model.ExportStarReexport:
			continue // 通配重导出不占导出名
		}
		if exportNames[name] {
			return &dceerr.Error{
				Category: dceerr.CatInvalidArgument, Module: m.ID, Name: name,
				Detail: "duplicate export name",
			}
		}
		exportNames[name] = true
	}
	return nil
}

// orderedModule 按模块标识升序遍历所需的辅助结构。
type orderedModule struct {
	id string
	m  *model.Module
}

// CheckSnapshot 做需要完整模块图的构造期检查，类别优先级固定为
// 重复模块(2) -> 未知模块(3) -> 未定义引用(4)。
// 同一类别下按模块标识升序、再按登记次序（导入先于重导出；声明按声明次序）报告第一处。
func CheckSnapshot(snap *model.Snapshot, known map[string]bool) error {
	if known == nil {
		known = make(map[string]bool, len(snap.Modules))
		for id := range snap.Modules {
			known[id] = true
		}
	}

	mods := make([]orderedModule, 0, len(snap.Modules))
	for id, m := range snap.Modules {
		mods = append(mods, orderedModule{id, m})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].id < mods[j].id })

	// 类别 3：未知模块（导入与重导出目标）。
	for _, om := range mods {
		for _, imp := range om.m.Imports {
			if !known[imp.Target] {
				return &dceerr.Error{
					Category: dceerr.CatUnknownModule, Module: om.id, Target: imp.Target,
					Detail: "import target does not exist",
				}
			}
		}
		for _, e := range om.m.Exports {
			if e.Kind == model.ExportReexport || e.Kind == model.ExportStarReexport {
				if !known[e.Target] {
					return &dceerr.Error{
						Category: dceerr.CatUnknownModule, Module: om.id, Target: e.Target,
						Detail: "reexport target does not exist",
					}
				}
			}
		}
	}

	// 类别 4：未定义引用。声明引用必须解析到本模块声明或导入本地绑定。
	for _, om := range mods {
		localBindings := make(map[string]bool)
		for _, imp := range om.m.Imports {
			for _, b := range imp.Bindings {
				localBindings[b.Local] = true
			}
		}
		for _, d := range om.m.Decls {
			for _, ref := range d.Refs {
				if !declExists(om.m, ref) && !localBindings[ref] {
					return &dceerr.Error{
						Category: dceerr.CatUndefinedReference, Module: om.id, Name: ref,
						Detail: "reference is neither a local declaration nor an import binding",
					}
				}
			}
		}
	}
	return nil
}

func declExists(m *model.Module, name string) bool {
	for _, d := range m.Decls {
		if d.Name == name {
			return true
		}
	}
	return false
}
