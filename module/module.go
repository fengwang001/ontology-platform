// Package module 实现带循环依赖、暂时性死区与错误粘滞的模块链接与求值器。
//
// 会话（Session）登记不可变模块，Evaluate 分两步原子完成：
// 先沿依赖列表深度优先先序链接根模块的可达子图（导出解析、函数导出初始化），
// 再按深度优先后序求值（循环跳过、TDZ 检查、错误粘滞）。
// 所有公开方法持有同一把互斥锁，一次 Evaluate 对外表现为一个原子步骤。
package module

import (
	"fmt"
	"sync"
)

// ExportKind 导出种类。
type ExportKind int

const (
	// Func 函数导出：链接时即已初始化。
	Func ExportKind = iota
	// Let 导出：在体内被 Init 之前未初始化。
	Let
)

// State 模块状态。
type State int

const (
	// StateRegistered 已登记。
	StateRegistered State = iota
	// StateLinked 已链接。
	StateLinked
	// StateEvaluating 求值中。
	StateEvaluating
	// StateEvaluated 已求值。
	StateEvaluated
	// StateError 出错（带错误值）。
	StateError
)

// String 返回状态的可读名字。
func (s State) String() string {
	switch s {
	case StateRegistered:
		return "已登记"
	case StateLinked:
		return "已链接"
	case StateEvaluating:
		return "求值中"
	case StateEvaluated:
		return "已求值"
	case StateError:
		return "出错"
	}
	return "未知"
}

// 各类上限。
const (
	maxNameBytes      = 32
	maxMsgBytes       = 64
	maxModules        = 100
	maxImports        = 20
	maxNamesPerImport = 10
	maxExports        = 20
	maxReexports      = 20
	maxSteps          = 50
)

// Import 一条导入语句：来源模块名 + 绑定名列表。
type Import struct {
	Source string
	Names  []string
}

// Export 一个本地导出。
type Export struct {
	Name string
	Kind ExportKind
}

// Reexport 一项转出。Star 为真表示星转出（仅 Source 有效），
// 否则为具名转出（Name, Source, SourceName）。
type Reexport struct {
	Star       bool
	Name       string // 具名转出：本模块的导出名
	Source     string // 来源模块名
	SourceName string // 具名转出：来源名
}

// NamedReexport 构造具名转出（导出名, 来源模块名, 来源名）。
func NamedReexport(name, source, sourceName string) Reexport {
	return Reexport{Name: name, Source: source, SourceName: sourceName}
}

// StarReexport 构造星转出（来源模块名）。
func StarReexport(source string) Reexport {
	return Reexport{Star: true, Source: source}
}

// StepKind 体步骤种类。
type StepKind int

const (
	// StepInit 把本模块某个 let 导出的初始化位置真。
	StepInit StepKind = iota
	// StepRead 读取某模块的绑定（可能触发 TDZ）。
	StepRead
	// StepThrow 抛出错误值。
	StepThrow
)

// Step 体中的一步。
type Step struct {
	Kind    StepKind
	Name    string // Init 的名字 / Read 的名字
	Module  string // Read 的模块名
	Message string // Throw 的消息
}

// Init 构造 Init(名) 步骤。
func Init(name string) Step { return Step{Kind: StepInit, Name: name} }

// Read 构造 Read(模块名, 名) 步骤。
func Read(mod, name string) Step { return Step{Kind: StepRead, Module: mod, Name: name} }

// Throw 构造 Throw(消息) 步骤。
func Throw(msg string) Step { return Step{Kind: StepThrow, Message: msg} }

// RejectReason 拒绝原因，可按值区分。
type RejectReason int

const (
	// RejectInvalidArgument 参数非法（格式与上限违规、名字空串等）。
	RejectInvalidArgument RejectReason = iota
	// RejectNameRegistered 名字已登记。
	RejectNameRegistered
	// RejectTooManyModules 模块数超限。
	RejectTooManyModules
	// RejectModuleNotFound 模块不存在（根或遍历到的来源未登记）。
	RejectModuleNotFound
	// RejectLinkError 链接错误（导出解析未找到）。
	RejectLinkError
	// RejectLinkAmbiguous 链接歧义（导出解析歧义）。
	RejectLinkAmbiguous
)

// String 返回拒绝原因的可读名字。
func (r RejectReason) String() string {
	switch r {
	case RejectInvalidArgument:
		return "参数非法"
	case RejectNameRegistered:
		return "名字已登记"
	case RejectTooManyModules:
		return "模块数超限"
	case RejectModuleNotFound:
		return "模块不存在"
	case RejectLinkError:
		return "链接错误"
	case RejectLinkAmbiguous:
		return "链接歧义"
	}
	return "未知"
}

// Reject 一次被拒绝的操作。被拒绝的操作不改变任何状态。
type Reject struct {
	Reason RejectReason
	Module string // 链接失败所属模块（Evaluate 链接阶段）
	Source string // 来源模块名
	Name   string // 相关名字（未登记模块名 / 解析名 / 重复名）
	Detail string // 参数非法的具体说明
}

// Error 实现 error 接口。
func (r *Reject) Error() string {
	switch r.Reason {
	case RejectLinkError, RejectLinkAmbiguous:
		return r.Reason.String() + ": (" + r.Module + ", " + r.Source + ", " + r.Name + ")"
	case RejectModuleNotFound, RejectNameRegistered:
		return r.Reason.String() + ": " + r.Name
	case RejectInvalidArgument:
		return r.Reason.String() + ": " + r.Detail
	}
	return r.Reason.String()
}

// module 已登记模块的内部表示。登记后规格不再改变。
type module struct {
	name     string
	imports  []Import
	exports  []Export
	reexport []Reexport
	body     []Step

	deps           []string            // 依赖列表：导入来源按语句次序 + 转出来源按列表次序
	exportSet      map[string]Export   // 本地导出名 -> 导出
	letSet         map[string]bool     // let 导出名集合
	namedReexports map[string]Reexport // 具名转出导出名 -> 转出项
	starSources    []string            // 星转出来源按列表次序

	state    State
	errValue string
	inited   map[string]bool // 导出名 -> 已初始化位（函数导出链接时置真，let 导出由 Init 置真）

	readBind map[[2]string][2]string // 链接时缓存：（来源, 名字）->（定义模块, 本地名）
	bodyRuns int                     // 体执行次数（非导出计数器，不变量：至多 1）
}

// Session 一个模块会话：模块表 + 求值完成次序。
// 所有公开方法可并发调用，一次 Evaluate 是一个原子步骤。
type Session struct {
	mu      sync.Mutex
	modules map[string]*module
	order   []string

	// 非导出计数器（供测试验证不变量）。
	lastLinkVisited       int // 上一次 Evaluate 链接遍历访问的模块数
	lastResolveExpansions int // 上一次顶层 ResolveExport 的展开次数
	lastResolveSetSize    int // 上一次顶层 ResolveExport 解析集合的最终大小
}

// NewSession 创建空会话。
func NewSession() *Session {
	return &Session{modules: make(map[string]*module)}
}

func validName(s string) bool { return len(s) >= 1 && len(s) <= maxNameBytes }

func invalidf(format string, args ...interface{}) *Reject {
	return &Reject{Reason: RejectInvalidArgument, Detail: fmt.Sprintf(format, args...)}
}

// AddModule 登记一个模块。模块登记后不可改变；来源模块可以尚未登记。
// 拒绝顺序：参数非法 -> 名字已登记 -> 模块数超限。被拒绝时不改变任何状态。
func (s *Session) AddModule(name string, imports []Import, exports []Export, reexports []Reexport, body []Step) *Reject {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rej := validateSpec(name, imports, exports, reexports, body); rej != nil {
		return rej
	}
	if _, ok := s.modules[name]; ok {
		return &Reject{Reason: RejectNameRegistered, Name: name}
	}
	if len(s.modules) >= maxModules {
		return &Reject{Reason: RejectTooManyModules, Name: name}
	}

	m := &module{
		name:           name,
		imports:        append([]Import(nil), imports...),
		exports:        append([]Export(nil), exports...),
		reexport:       append([]Reexport(nil), reexports...),
		body:           append([]Step(nil), body...),
		exportSet:      make(map[string]Export),
		letSet:         make(map[string]bool),
		namedReexports: make(map[string]Reexport),
		state:          StateRegistered,
		inited:         make(map[string]bool),
		readBind:       make(map[[2]string][2]string),
	}
	for _, imp := range m.imports {
		m.deps = append(m.deps, imp.Source)
	}
	for _, re := range m.reexport {
		m.deps = append(m.deps, re.Source)
		if !re.Star {
			m.namedReexports[re.Name] = re
		} else {
			m.starSources = append(m.starSources, re.Source)
		}
	}
	for _, ex := range m.exports {
		m.exportSet[ex.Name] = ex
		if ex.Kind == Let {
			m.letSet[ex.Name] = true
		}
	}
	s.modules[name] = m
	return nil
}

// validateSpec 校验 AddModule 的全部格式与上限约束。
func validateSpec(name string, imports []Import, exports []Export, reexports []Reexport, body []Step) *Reject {
	if !validName(name) {
		return invalidf("模块名 %q 为空或超过 %d 字节", name, maxNameBytes)
	}
	if len(imports) > maxImports {
		return invalidf("导入语句数 %d 超过上限 %d", len(imports), maxImports)
	}
	if len(exports) > maxExports {
		return invalidf("本地导出数 %d 超过上限 %d", len(exports), maxExports)
	}
	if len(reexports) > maxReexports {
		return invalidf("转出项数 %d 超过上限 %d", len(reexports), maxReexports)
	}
	if len(body) > maxSteps {
		return invalidf("体步骤数 %d 超过上限 %d", len(body), maxSteps)
	}

	localExports := make(map[string]bool)
	for _, ex := range exports {
		if !validName(ex.Name) {
			return invalidf("导出名 %q 为空或超过 %d 字节", ex.Name, maxNameBytes)
		}
		if ex.Kind != Func && ex.Kind != Let {
			return invalidf("导出 %q 的种类非法", ex.Name)
		}
		if localExports[ex.Name] {
			return invalidf("本地导出名 %q 重复", ex.Name)
		}
		localExports[ex.Name] = true
	}

	namedReexports := make(map[string]bool)
	for _, re := range reexports {
		if !validName(re.Source) {
			return invalidf("转出来源 %q 为空或超过 %d 字节", re.Source, maxNameBytes)
		}
		if re.Source == name {
			return invalidf("转出来源不能是本模块 %q", name)
		}
		if re.Star {
			continue
		}
		if !validName(re.Name) || !validName(re.SourceName) {
			return invalidf("具名转出名 %q 或来源名 %q 为空或超长", re.Name, re.SourceName)
		}
		if localExports[re.Name] {
			return invalidf("具名转出名 %q 与本地导出名重名", re.Name)
		}
		if namedReexports[re.Name] {
			return invalidf("具名转出名 %q 重复", re.Name)
		}
		namedReexports[re.Name] = true
	}

	imported := make(map[string]bool)
	importedFrom := make(map[string]map[string]bool) // 来源 -> 从该来源导入的名字
	for _, imp := range imports {
		if !validName(imp.Source) {
			return invalidf("导入来源 %q 为空或超过 %d 字节", imp.Source, maxNameBytes)
		}
		if imp.Source == name {
			return invalidf("导入来源不能是本模块 %q", name)
		}
		if len(imp.Names) < 1 || len(imp.Names) > maxNamesPerImport {
			return invalidf("导入语句（来源 %q）名字数 %d 不在 1..%d", imp.Source, len(imp.Names), maxNamesPerImport)
		}
		if importedFrom[imp.Source] == nil {
			importedFrom[imp.Source] = make(map[string]bool)
		}
		for _, n := range imp.Names {
			if !validName(n) {
				return invalidf("导入名 %q 为空或超过 %d 字节", n, maxNameBytes)
			}
			if imported[n] {
				return invalidf("导入名 %q 重复", n)
			}
			if localExports[n] || namedReexports[n] {
				return invalidf("导入名 %q 与本模块导出名重名", n)
			}
			imported[n] = true
			importedFrom[imp.Source][n] = true
		}
	}

	for _, st := range body {
		switch st.Kind {
		case StepInit:
			ex, ok := localExportOf(exports, st.Name)
			if !ok || ex.Kind != Let {
				return invalidf("Init 的名字 %q 不是本模块的 let 导出", st.Name)
			}
		case StepRead:
			if st.Module == name {
				if !localExports[st.Name] {
					return invalidf("Read 的名字 %q 不是本模块的本地导出", st.Name)
				}
			} else if src := importedFrom[st.Module]; src == nil || !src[st.Name] {
				return invalidf("Read 的名字 %q 不是从模块 %q 导入的名字", st.Name, st.Module)
			}
		case StepThrow:
			if len(st.Message) < 1 || len(st.Message) > maxMsgBytes {
				return invalidf("Throw 消息长度 %d 不在 1..%d", len(st.Message), maxMsgBytes)
			}
		default:
			return invalidf("体步骤种类非法")
		}
	}
	return nil
}

func localExportOf(exports []Export, name string) (Export, bool) {
	for _, ex := range exports {
		if ex.Name == name {
			return ex, true
		}
	}
	return Export{}, false
}

// EvalResult 一次 Evaluate 的返回：本次追加到求值完成次序的模块名列表与根的结果。
type EvalResult struct {
	Appended []string // 本次调用追加到次序的模块名（按追加顺序）
	OK       bool     // 根的结果：成功为 true
	ErrValue string   // 根的结果：失败时的错误值
}

// Evaluate 原子地链接根模块的可达子图并按深度优先后序求值。
// 拒绝顺序：参数非法 -> 模块不存在 -> 链接错误 / 链接歧义（先序第一个失败者）。
// 被拒绝时不改变任何状态；求值阶段的错误是结果而不是拒绝，其修改全部保留。
func (s *Session) Evaluate(root string) (EvalResult, *Reject) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validName(root) {
		return EvalResult{}, invalidf("根模块名 %q 为空或超过 %d 字节", root, maxNameBytes)
	}
	rootMod, ok := s.modules[root]
	if !ok {
		return EvalResult{}, &Reject{Reason: RejectModuleNotFound, Name: root}
	}
	// 根已求值或已出错：直接返回，不做任何遍历。
	if rootMod.state == StateEvaluated {
		s.lastLinkVisited = 1
		return EvalResult{OK: true}, nil
	}
	if rootMod.state == StateError {
		s.lastLinkVisited = 1
		return EvalResult{OK: false, ErrValue: rootMod.errValue}, nil
	}

	// 第一步：链接。沿依赖列表深度优先先序遍历，只进入已登记的模块。
	var preorder []*module
	visited := make(map[string]bool)
	var rej *Reject
	var walk func(m *module)
	walk = func(m *module) {
		if visited[m.name] {
			return
		}
		visited[m.name] = true
		if m.state != StateRegistered {
			return
		}
		preorder = append(preorder, m)
		for _, dep := range m.deps {
			d, ok := s.modules[dep]
			if !ok {
				rej = &Reject{Reason: RejectModuleNotFound, Name: dep}
				return
			}
			walk(d)
			if rej != nil {
				return
			}
		}
	}
	walk(rootMod)
	s.lastLinkVisited = len(preorder)
	if rej != nil {
		return EvalResult{}, rej
	}

	// 按先序对访问到的模块做导出解析：先导入（语句次序、语句内名字次序），
	// 再各具名转出（列表次序）。全部通过前不提交任何解析缓存。
	type commit struct {
		m    *module
		key  [2]string
		bind [2]string
	}
	var commits []commit
	fail := func(m *module, source, name string, r resolveResult) *Reject {
		if r == resolveAmbiguous {
			return &Reject{Reason: RejectLinkAmbiguous, Module: m.name, Source: source, Name: name}
		}
		return &Reject{Reason: RejectLinkError, Module: m.name, Source: source, Name: name}
	}
	for _, m := range preorder {
		for _, imp := range m.imports {
			for _, n := range imp.Names {
				bind, r := s.resolveExport(imp.Source, n)
				if r != resolveFound {
					return EvalResult{}, fail(m, imp.Source, n, r)
				}
				commits = append(commits, commit{m, [2]string{imp.Source, n}, bind})
			}
		}
		for _, re := range m.reexport {
			if re.Star {
				continue
			}
			if _, r := s.resolveExport(re.Source, re.SourceName); r != resolveFound {
				return EvalResult{}, fail(m, re.Source, re.SourceName, r)
			}
		}
	}

	// 全部通过：置已链接、函数导出位置真、提交解析缓存。
	for _, m := range preorder {
		m.state = StateLinked
		for _, ex := range m.exports {
			if ex.Kind == Func {
				m.inited[ex.Name] = true
			}
		}
	}
	for _, c := range commits {
		c.m.readBind[c.key] = c.bind
	}

	// 第二步：求值。
	before := len(s.order)
	errValue, ok := s.eval(rootMod)
	return EvalResult{
		Appended: append([]string(nil), s.order[before:]...),
		OK:       ok,
		ErrValue: errValue,
	}, nil
}

// eval 深度优先后序求值。返回（错误值, 是否成功）。
func (s *Session) eval(m *module) (string, bool) {
	switch m.state {
	case StateEvaluated:
		return "", true
	case StateError:
		return m.errValue, false
	case StateEvaluating:
		return "", true // 循环：跳过
	}
	m.state = StateEvaluating
	for _, depName := range m.deps {
		d := s.modules[depName]
		if errValue, ok := s.eval(d); !ok {
			m.state = StateError
			m.errValue = errValue
			return errValue, false
		}
	}
	m.bodyRuns++
	for _, st := range m.body {
		switch st.Kind {
		case StepInit:
			m.inited[st.Name] = true
		case StepRead:
			d, localName := m, st.Name
			if st.Module != m.name {
				bind := m.readBind[[2]string{st.Module, st.Name}]
				d, localName = s.modules[bind[0]], bind[1]
			}
			if d.letSet[localName] && !d.inited[localName] {
				errValue := "TDZ " + d.name + "." + localName
				m.state = StateError
				m.errValue = errValue
				return errValue, false
			}
		case StepThrow:
			m.state = StateError
			m.errValue = st.Message
			return st.Message, false
		}
	}
	m.state = StateEvaluated
	s.order = append(s.order, m.name)
	return "", true
}

// resolveResult 导出解析结果。
type resolveResult int

const (
	resolveNotFound  resolveResult = iota // 未找到
	resolveFound                          // 找到绑定
	resolveAmbiguous                      // 歧义
)

// resolveCtx 一次顶层 ResolveExport 调用的解析集合，整个调用内共享、只增不减。
type resolveCtx struct {
	seen       map[[2]string]bool
	expansions int
}

// resolveExport 顶层导出解析：持有本调用共享的解析集合。
func (s *Session) resolveExport(m, n string) ([2]string, resolveResult) {
	ctx := &resolveCtx{seen: make(map[[2]string]bool)}
	bind, r := s.resolve(ctx, m, n)
	s.lastResolveExpansions = ctx.expansions
	s.lastResolveSetSize = len(ctx.seen)
	return bind, r
}

// resolve 实现 ResolveExport(m, n)：本地导出与具名转出优先于星转出。
func (s *Session) resolve(ctx *resolveCtx, m, n string) ([2]string, resolveResult) {
	key := [2]string{m, n}
	if ctx.seen[key] {
		return [2]string{}, resolveNotFound
	}
	ctx.seen[key] = true
	ctx.expansions++
	mod := s.modules[m]
	if mod == nil {
		return [2]string{}, resolveNotFound // 不会发生：遍历已保证来源已登记
	}
	if _, ok := mod.exportSet[n]; ok {
		return [2]string{m, n}, resolveFound
	}
	if re, ok := mod.namedReexports[n]; ok {
		return s.resolve(ctx, re.Source, re.SourceName)
	}
	var star [2]string
	hasStar := false
	for _, src := range mod.starSources {
		bind, r := s.resolve(ctx, src, n)
		if r == resolveAmbiguous {
			return [2]string{}, resolveAmbiguous
		}
		if r == resolveFound {
			if hasStar && star != bind {
				return [2]string{}, resolveAmbiguous
			}
			star = bind
			hasStar = true
		}
	}
	if hasStar {
		return star, resolveFound
	}
	return [2]string{}, resolveNotFound
}

// ModuleStatus 模块状态快照。
type ModuleStatus struct {
	State    State
	ErrValue string          // State 为出错时的错误值
	LetInit  map[string]bool // 各 let 导出的初始化位
}

// Status 返回模块状态与各 let 导出的初始化位；名字未登记报不存在。
func (s *Session) Status(name string) (ModuleStatus, *Reject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.modules[name]
	if !ok {
		return ModuleStatus{}, &Reject{Reason: RejectModuleNotFound, Name: name}
	}
	st := ModuleStatus{State: m.state, ErrValue: m.errValue, LetInit: make(map[string]bool)}
	for _, ex := range m.exports {
		if ex.Kind == Let {
			st.LetInit[ex.Name] = m.inited[ex.Name]
		}
	}
	return st, nil
}

// Order 返回求值完成次序（副本）。
func (s *Session) Order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}
