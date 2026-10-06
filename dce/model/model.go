// Package model 定义死代码裁剪分析器使用的输入数据模型。
package model

// SideEffect 表示模块级副作用声明。
type SideEffect int

const (
	// SideEffectDefault 缺省：视为“有”。
	SideEffectDefault SideEffect = iota
	// SideEffectYes 显式声明模块级副作用“有”。
	SideEffectYes
	// SideEffectNo 显式声明模块级副作用“无”。
	SideEffectNo
)

// HasSideEffects 返回该模块是否在被引入时执行模块级副作用。
func (s SideEffect) HasSideEffects() bool {
	return s != SideEffectNo
}

// Declaration 是模块顶层声明。
type Declaration struct {
	Name       string
	SideEffect bool     // 该声明执行时是否带有副作用
	Refs       []string // 引用的标识符：本模块声明或本模块导入绑定的本地名
}

// ImportBinding 是导入语句中的一个绑定：本地名 Local 绑定到目标模块的导出名 Imported。
// Imported == "*" 表示通配（命名空间）导入。
type ImportBinding struct {
	Local    string
	Imported string
}

// Import 是一条导入语句。Target 为目标模块标识；Bindings 为空表示纯副作用导入。
type Import struct {
	Target   string
	Bindings []ImportBinding
}

// ExportKind 区分三种导出来源。
type ExportKind int

const (
	// ExportLocal 本地声明导出：导出名对应一个本地声明。
	ExportLocal ExportKind = iota
	// ExportReexport 具名重导出：来自目标模块的某个导出名，可改名。
	ExportReexport
	// ExportStarReexport 通配重导出：转发目标模块的全部导出（不含 default）。
	ExportStarReexport
)

// Export 是一条导出项。
//   - ExportLocal:  使用 LocalName（导出/声明同名）
//   - ExportReexport: 使用 ExportName（本模块导出名）、Target、ForeignName（目标模块导出名）
//   - ExportStarReexport: 仅使用 Target
type Export struct {
	Kind        ExportKind
	LocalName   string
	ExportName  string
	ForeignName string
	Target      string
}

// Module 是一个登记模块。
type Module struct {
	ID         string
	SideEffect SideEffect
	Decls      []Declaration
	Imports    []Import
	Exports    []Export
}

// Key 唯一定位一个声明。
type Key struct {
	Module string
	Decl   string
}

// Snapshot 是某次求解所基于的只读模块快照。
type Snapshot struct {
	Modules map[string]*Module
	// Order 保留登记次序，保证结果与串行化顺序无关的可复现性。
	Order []string
}

// Clone 深拷贝一个模块，保证登记被拒绝或快照被外部修改时互不影响。
func (m *Module) Clone() *Module {
	cp := &Module{ID: m.ID, SideEffect: m.SideEffect}
	cp.Decls = make([]Declaration, len(m.Decls))
	for i, d := range m.Decls {
		cp.Decls[i] = Declaration{Name: d.Name, SideEffect: d.SideEffect, Refs: append([]string(nil), d.Refs...)}
	}
	cp.Imports = make([]Import, len(m.Imports))
	for i, imp := range m.Imports {
		bindings := make([]ImportBinding, len(imp.Bindings))
		copy(bindings, imp.Bindings)
		cp.Imports[i] = Import{Target: imp.Target, Bindings: bindings}
	}
	cp.Exports = append([]Export(nil), m.Exports...)
	return cp
}
