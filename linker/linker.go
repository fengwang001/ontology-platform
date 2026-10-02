package linker

import (
	"errors"
	"fmt"
	"sync"
)

// 拒绝原因。它们都是"拒绝"：发生时不改变任何状态。
var (
	ErrInvalidArg = errors.New("invalid argument")
	ErrNameExists = errors.New("name already registered")
	ErrTooMany    = errors.New("too many modules")
	ErrNotFound   = errors.New("module not found")
)

// LinkError 为链接阶段的拒绝（"链接错误"或"链接歧义"）。
// 解析结果为"未找到"时 Ambiguous 为假（链接错误）；
// 为"歧义"时 Ambiguous 为真（链接歧义）。
type LinkError struct {
	Ambiguous bool
	Module    string // 正在做导入/具名转出解析的本模块
	Source    string // 来源模块名
	Name      string // 绑定名
}

func (e *LinkError) Error() string {
	if e.Ambiguous {
		return fmt.Sprintf("link ambiguity: (%s, %s, %s)", e.Module, e.Source, e.Name)
	}
	return fmt.Sprintf("link error: (%s, %s, %s)", e.Module, e.Source, e.Name)
}

// AsLinkError 提取 LinkError。
func AsLinkError(err error) (*LinkError, bool) {
	var le *LinkError
	if errors.As(err, &le) {
		return le, true
	}
	return nil, false
}

const (
	maxNameLen    = 32
	maxMessageLen = 64
	maxModules    = 100
	maxImports    = 20
	maxExports    = 20
	maxReExports  = 20
	maxSteps      = 50
	maxBindings   = 10
)

// module 为会话内部的模块记录。
type module struct {
	def         ModuleDef
	status      ModuleStatus
	err         string
	initialized map[string]bool // let 导出初始化位

	// importBinding[导入名] = 链接时解析得到的定义绑定。
	importBinding map[string]binding
	// importSource[导入名] = 该名字从哪条导入语句的来源导入。
	importSource map[string]string

	// 非导出计数器：模块体执行次数（至多一次）。
	bodyRuns int

	// deps 为缓存的依赖列表（导入来源按语句次序，再接转出来源按列表次序，保留重复）。
	deps []string
}

// Session 为一个模块链接与求值会话。所有方法可并发调用。
type Session struct {
	mu      sync.Mutex
	modules map[string]*module
	order   []string

	// 非导出计数器：最近一次顶层 ResolveExport 内各 (模块,名字) 对的展开次数。
	resolutionExpansions map[[2]string]int

	// 非导出计数器：最近一次 Evaluate 的链接遍历访问模块数。
	lastLinkVisitCount int
	// 非导出计数器：最近一次 Evaluate 第二步的 Eval 访问模块数。
	lastEvalVisitCount int
}

// NewSession 创建空会话。
func NewSession() *Session {
	return &Session{modules: map[string]*module{}}
}

func validName(s string) bool {
	return len(s) >= 1 && len(s) <= maxNameLen
}

// AddModule 登记一个模块。
func (s *Session) AddModule(def ModuleDef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addModule(def)
}

func (s *Session) addModule(def ModuleDef) error {
	deps, importedNames, localExports, err := validateDef(def)
	if err != nil {
		return err
	}

	// 2) 名字已登记。
	if _, exists := s.modules[def.Name]; exists {
		return ErrNameExists
	}
	// 3) 模块数超限。
	if len(s.modules) >= maxModules {
		return ErrTooMany
	}

	m := &module{
		def:           copyDef(def),
		status:        StatusRegistered,
		initialized:   map[string]bool{},
		importBinding: map[string]binding{},
		importSource:  importedNames,
		deps:          deps,
	}
	for name, kind := range localExports {
		if kind == KindLet {
			m.initialized[name] = false
		}
	}
	s.modules[def.Name] = m
	return nil
}

func copyDef(def ModuleDef) ModuleDef {
	out := def
	out.Imports = append([]ImportStmt(nil), def.Imports...)
	for i := range out.Imports {
		out.Imports[i].Bindings = append([]string(nil), def.Imports[i].Bindings...)
	}
	out.Exports = append([]Export(nil), def.Exports...)
	out.ReExports = append([]ReExport(nil), def.ReExports...)
	out.Body = append([]Step(nil), def.Body...)
	return out
}

// Evaluate 原子地链接并求值根模块的可达子图。
func (s *Session) Evaluate(rootName string) (EvalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evaluate(rootName)
}

// resolver 为导出解析器。解析集合在每次顶层 ResolveExport 开始时重置，
// 在该次调用内共享、只增不减。
type resolver struct {
	s          *Session
	set        map[[2]string]bool
	expansions map[[2]string]int
}

// resolveExport 实现 ResolveExport(m,n)。
// found=false 且 ambiguous=false 为未找到；ambiguous=true 为歧义。
func (r *resolver) resolveExport(mName, n string) (b binding, found, ambiguous bool) {
	key := [2]string{mName, n}
	if r.set[key] {
		return binding{}, false, false
	}
	r.set[key] = true
	r.expansions[key]++

	m := r.s.modules[mName]
	for _, e := range m.def.Exports {
		if e.Name == n {
			return binding{module: mName, name: n}, true, false
		}
	}
	for _, re := range m.def.ReExports {
		if !re.IsStar() && re.Name == n {
			return r.resolveExport(re.Source, re.From)
		}
	}
	var star binding
	haveStar := false
	for _, re := range m.def.ReExports {
		if !re.IsStar() {
			continue
		}
		cb, cfound, cambig := r.resolveExport(re.Source, n)
		if cambig {
			return binding{}, false, true
		}
		if !cfound {
			continue
		}
		if haveStar && cb != star {
			return binding{}, false, true
		}
		star, haveStar = cb, true
	}
	if haveStar {
		return star, true, false
	}
	return binding{}, false, false
}

func (r *resolver) reset(s *Session) {
	r.set = map[[2]string]bool{}
	r.expansions = map[[2]string]int{}
	s.resolutionExpansions = r.expansions
}

func (s *Session) evaluate(rootName string) (EvalResult, error) {
	if !validName(rootName) {
		return EvalResult{}, ErrInvalidArg
	}
	root, ok := s.modules[rootName]
	if !ok {
		return EvalResult{}, ErrNotFound
	}

	// 已求值或已出错：直接返回，不做任何遍历。
	if root.status == StatusEvaluated {
		s.lastLinkVisitCount = 1
		s.lastEvalVisitCount = 1
		return EvalResult{Appended: []string{}, OK: true}, nil
	}
	if root.status == StatusErrored {
		s.lastLinkVisitCount = 1
		s.lastEvalVisitCount = 1
		return EvalResult{Appended: []string{}, OK: false, Error: root.err}, nil
	}

	// ---- 第一步：链接（先序深度优先遍历）----
	visited := map[string]bool{}
	var linkedOrder []string // 本次新链接的模块（先序）
	stack := []string{rootName}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur] {
			continue
		}
		cm, exists := s.modules[cur]
		if !exists {
			s.lastLinkVisitCount = len(linkedOrder) + 1
			return EvalResult{}, ErrNotFound
		}
		visited[cur] = true
		if cm.status != StatusRegistered {
			continue // 已链接及之后状态不再进入
		}
		// 只进入状态为已登记的模块。
		linkedOrder = append(linkedOrder, cur)
		for i := len(cm.deps) - 1; i >= 0; i-- {
			if !visited[cm.deps[i]] {
				stack = append(stack, cm.deps[i])
			}
		}
	}

	// 导出解析：先导入语句（语句次序、名字次序），再具名转出（列表次序）。
	r := &resolver{s: s}
	parseBindings := func(mName, source, name string) error {
		r.reset(s)
		bd, found, ambig := r.resolveExport(source, name)
		if !found {
			return &LinkError{Ambiguous: ambig, Module: mName, Source: source, Name: name}
		}
		s.modules[mName].importBinding[name] = bd
		return nil
	}
	for _, mName := range linkedOrder {
		m := s.modules[mName]
		for _, st := range m.def.Imports {
			for _, b := range st.Bindings {
				if err := parseBindings(mName, st.Source, b); err != nil {
					return EvalResult{}, err
				}
			}
		}
		for _, re := range m.def.ReExports {
			if re.IsStar() {
				continue
			}
			r.reset(s)
			_, found, ambig := r.resolveExport(re.Source, re.From)
			if !found {
				return EvalResult{}, &LinkError{Ambiguous: ambig, Module: mName, Source: re.Source, Name: re.From}
			}
		}
	}

	// 全部通过后提交：置已链接；全部函数导出位置真。
	for _, mName := range linkedOrder {
		m := s.modules[mName]
		m.status = StatusLinked
		for _, e := range m.def.Exports {
			if e.Kind == KindFunction {
				m.initialized[e.Name] = true
			}
		}
	}
	s.lastLinkVisitCount = len(linkedOrder)

	// ---- 第二步：深度优先后序求值 ----
	ev := &evaluator{s: s}
	startLen := len(s.order)
	ok2, errVal := ev.eval(rootName)
	s.lastEvalVisitCount = ev.visits

	res := EvalResult{Appended: append([]string(nil), s.order[startLen:]...), OK: ok2}
	if !ok2 {
		res.Error = errVal
	}
	return res, nil
}

type evaluator struct {
	s      *Session
	visits int
}

func (ev *evaluator) eval(mName string) (bool, string) {
	m := ev.s.modules[mName]
	ev.visits++
	switch m.status {
	case StatusEvaluated:
		return true, ""
	case StatusErrored:
		return false, m.err
	case StatusEvaluating:
		return true, "" // 循环：跳过
	}

	m.status = StatusEvaluating
	for _, dep := range m.deps {
		if ok, e := ev.eval(dep); !ok {
			m.status = StatusErrored
			m.err = e
			return false, e
		}
	}

	m.bodyRuns++
	if e, thrown := ev.runBody(m); thrown {
		m.status = StatusErrored
		m.err = e
		return false, e
	}

	m.status = StatusEvaluated
	ev.s.order = append(ev.s.order, m.def.Name)
	return true, ""
}

func (ev *evaluator) runBody(m *module) (string, bool) {
	for _, step := range m.def.Body {
		switch step.Kind {
		case StepInit:
			m.initialized[step.Name] = true
		case StepRead:
			var b binding
			if step.Module == m.def.Name {
				b = binding{module: m.def.Name, name: step.Name}
			} else {
				b = m.importBinding[step.Name]
			}
			d := ev.s.modules[b.module]
			if d.exportKind(b.name) == KindLet && !d.initialized[b.name] {
				return "TDZ " + b.module + "." + b.name, true
			}
		case StepThrow:
			return step.Message, true
		}
	}
	return "", false
}

func (m *module) exportKind(name string) BindingKind {
	for _, e := range m.def.Exports {
		if e.Name == name {
			return e.Kind
		}
	}
	return KindLet // 链接通过后被读到的名字必然存在
}

// Status 返回模块状态与各 let 导出的初始化位。
func (s *Session) Status(name string) (StatusResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.modules[name]
	if !ok {
		return StatusResult{}, ErrNotFound
	}
	lets := map[string]bool{}
	for _, e := range m.def.Exports {
		if e.Kind == KindLet {
			lets[e.Name] = m.initialized[e.Name]
		}
	}
	return StatusResult{Status: m.status, Lets: lets, Error: m.err}, nil
}

// Order 返回求值完成次序的副本。
func (s *Session) Order() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}
