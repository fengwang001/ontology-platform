package initorder

import (
	"sort"
	"sync"
)

// normalizeRefs 校验并规范化引用集合：每个元素必须是合法且非空白的
// 标识符；返回排序去重后的副本。非法时返回 ErrInvalidArgument。
func normalizeRefs(refs []string) ([]string, error) {
	norm := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, r := range refs {
		if !isValidIdent(r) {
			return nil, &InvalidArgumentError{Reason: "invalid identifier in refs: " + r}
		}
		if isBlankIdent(r) {
			return nil, &InvalidArgumentError{Reason: "blank identifier in refs"}
		}
		if !seen[r] {
			seen[r] = true
			norm = append(norm, r)
		}
	}
	sort.Strings(norm)
	return norm, nil
}

// Session 是包级变量初始化次序求解会话。
// 调用方按源码出现次序逐个登记变量初始化单元与函数声明，
// 之后可任意多次求解。会话可安全地被多个 goroutine 并发使用。
type Session struct {
	mu          sync.RWMutex
	predeclared map[string]bool
	varOwner    map[string]int // 非空白变量名 -> 所属单元下标
	funcIndex   map[string]int // 函数名 -> 函数声明下标
	units       []varUnit
	funcs       []funcDecl
	decls       []declRef // 全部已接受声明，按登记次序
}

// varUnit 是一个变量初始化单元：左侧变量列表与右侧引用集合。
type varUnit struct {
	vars []string // 按登记次序，可含空白标识符
	refs []string // 排序去重后的引用集合
}

// funcDecl 是一个函数声明：函数名与函数体直接提到的标识符集合。
type funcDecl struct {
	name string
	refs []string // 排序去重后的引用集合
}

// declRef 指向一条已接受声明，用于按登记次序扫描。
type declRef struct {
	kind  DeclKind
	index int // 在 units 或 funcs 中的下标
}

// NewSession 创建会话。predeclared 为预声明标识符集合，视为已就绪、
// 无需初始化；其中出现非法标识符返回 ErrInvalidArgument，
// 出现重复返回 ErrDuplicateDeclaration。
func NewSession(predeclared []string) (*Session, error) {
	s := &Session{
		predeclared: make(map[string]bool, len(predeclared)),
		varOwner:    make(map[string]int),
		funcIndex:   make(map[string]int),
	}
	for _, name := range predeclared {
		if !isValidIdent(name) || isBlankIdent(name) {
			return nil, &InvalidArgumentError{Reason: "invalid predeclared identifier: " + name}
		}
		if s.predeclared[name] {
			return nil, &DuplicateDeclarationError{Name: name}
		}
		s.predeclared[name] = true
	}
	return s, nil
}

// RegisterVars 登记一个变量初始化单元。vars 至少一个元素，可含空白
// 标识符；refs 为初始化表达式直接提到的标识符集合，不得含空白标识符。
// 参数非法优先于重复声明被报告；被拒绝的登记不改变会话状态。
func (s *Session) RegisterVars(vars []string, refs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerVarsLocked(vars, refs)
}

// RegisterFunc 登记一个函数声明。name 不得为空白标识符，refs 为函数体
// 直接提到的标识符集合，不得含空白标识符。函数名与变量名共用命名空间。
func (s *Session) RegisterFunc(name string, refs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerFuncLocked(name, refs)
}

func (s *Session) registerVarsLocked(vars []string, refs []string) error {
	// 第一优先级：参数非法。
	if len(vars) == 0 {
		return &InvalidArgumentError{Reason: "empty variable list"}
	}
	for _, v := range vars {
		if !isValidIdent(v) {
			return &InvalidArgumentError{Reason: "invalid variable identifier: " + v}
		}
	}
	normRefs, err := normalizeRefs(refs)
	if err != nil {
		return err
	}
	// 第二优先级：重复声明（含同一单元内两个非空白变量同名）。
	seen := make(map[string]bool, len(vars))
	for _, v := range vars {
		if isBlankIdent(v) {
			continue // 空白标识符不参与重名判定
		}
		if seen[v] {
			return &DuplicateDeclarationError{Name: v}
		}
		seen[v] = true
		if s.predeclared[v] {
			return &DuplicateDeclarationError{Name: v}
		}
		if _, ok := s.varOwner[v]; ok {
			return &DuplicateDeclarationError{Name: v}
		}
		if _, ok := s.funcIndex[v]; ok {
			return &DuplicateDeclarationError{Name: v}
		}
	}
	// 全部校验通过后才改变会话状态。
	index := len(s.units)
	unit := varUnit{vars: append([]string(nil), vars...), refs: normRefs}
	s.units = append(s.units, unit)
	for _, v := range vars {
		if !isBlankIdent(v) {
			s.varOwner[v] = index
		}
	}
	s.decls = append(s.decls, declRef{kind: DeclKindVars, index: index})
	return nil
}

func (s *Session) registerFuncLocked(name string, refs []string) error {
	// 第一优先级：参数非法。
	if !isValidIdent(name) {
		return &InvalidArgumentError{Reason: "invalid func name: " + name}
	}
	if isBlankIdent(name) {
		return &InvalidArgumentError{Reason: "func name is blank identifier"}
	}
	normRefs, err := normalizeRefs(refs)
	if err != nil {
		return err
	}
	// 第二优先级：重复声明（函数名与变量名共用命名空间）。
	if s.predeclared[name] {
		return &DuplicateDeclarationError{Name: name}
	}
	if _, ok := s.varOwner[name]; ok {
		return &DuplicateDeclarationError{Name: name}
	}
	if _, ok := s.funcIndex[name]; ok {
		return &DuplicateDeclarationError{Name: name}
	}
	// 全部校验通过后才改变会话状态。
	index := len(s.funcs)
	s.funcs = append(s.funcs, funcDecl{name: name, refs: normRefs})
	s.funcIndex[name] = index
	s.decls = append(s.decls, declRef{kind: DeclKindFunc, index: index})
	return nil
}

// UnitResult 给出求解结果中一个单元的初始化信息。
type UnitResult struct {
	Unit int      // 单元在全部变量单元中的登记次序（从 0 开始）
	Vars []string // 左侧变量列表（按登记次序，可含空白标识符）
	Deps []string // 传递依赖的变量集合，按名字升序，不含自身变量与空白标识符
}

// Solution 是唯一确定的初始化次序及每个单元的判定依据。
type Solution struct {
	Order []UnitResult // 按初始化完成次序排列
}

// Solve 求解当前会话的初始化次序。求解不改变会话状态，
// 并发的求解与登记等价于按某个全序串行执行。
func (s *Session) Solve() (*Solution, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sol, _, err := s.solveLocked()
	return sol, err
}

// solveStats 记录求解过程的工作量计数，用于验证开销特性。
type solveStats struct {
	FuncExpansions int // 函数体被展开的次数（每个函数一次，与引用者数量无关）
	UnitsCompleted int // 完成初始化的单元数
}
