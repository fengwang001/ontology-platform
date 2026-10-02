// Package escape 实现按函数登记的逃逸分析摘要计算器。
//
// 每个函数在登记时做一次与语句次序无关、字段不敏感的流不敏感指向分析，
// 得到各分配点的逃逸分类与函数摘要；后登记的函数按已登记被调函数的
// 摘要分析；自递归函数对自身摘要求不动点。
package escape

import (
	"fmt"
	"sync"
)

// Kind 是语句种类。
type Kind int

const (
	New    Kind = iota // New(d)：在 d 处分配一个对象（一个分配点）
	Copy               // Copy(d, s)：d = s
	Store              // Store(d, s)：*d = s
	Load               // Load(d, s)：d = *s
	Ret                // Ret(s)：返回 s
	Global             // Global(s)：s 逃逸到全局
	Call               // Call(d, g, args)：d = g(args...)，d 为 -1 表示丢弃结果
)

// Stmt 是一条语句。D、S 为变量编号；G 为被调函数名；Args 为实参变量。
// 不同种类只使用部分字段：New 用 D；Copy/Store/Load 用 D、S；
// Ret/Global 用 S；Call 用 D、G、Args。
type Stmt struct {
	Kind Kind
	D    int
	S    int
	G    string
	Args []int
}

// Class 是分配点的逃逸分类。
type Class int

const (
	ClassStack  Class = iota // 栈上：不逃逸
	ClassReturn              // 返回逃逸
	ClassGlobal              // 全局逃逸
	ClassParam               // 参数逃逸
)

func (c Class) String() string {
	switch c {
	case ClassStack:
		return "stack"
	case ClassReturn:
		return "return"
	case ClassGlobal:
		return "global"
	case ClassParam:
		return "param"
	}
	return "unknown"
}

// Summary 是函数摘要。Ret、Glob 长度均为参数个数 k，E 为 k×k。
type Summary struct {
	Ret   []bool   // Ret[i]：参数 i 的对象可能沿返回值流出
	Glob  []bool   // Glob[i]：参数 i 的对象可能被标为全局
	E     [][]bool // E[i][j]：参数 j 的对象可能被直接存入参数 i 的对象
	Fresh bool     // Fresh：返回值可能是本函数新分配的对象或调用结果对象
}

// Site 是一个分配点及其分类。
type Site struct {
	ID    int // 全局分配点编号，从 1 起
	Class Class
}

// ErrorKind 区分拒绝/失败原因。
type ErrorKind int

const (
	ErrInvalidArgs      ErrorKind = iota // 参数非法
	ErrDuplicateName                     // 名字已登记
	ErrTooManyFunctions                  // 函数数超限
	ErrUnknownCallee                     // 被调函数未登记且不是自身
	ErrArgCountMismatch                  // 实参个数与被调函数参数个数不符
	ErrNotFound                          // 查询的名字未登记
)

// Error 是登记或查询失败的原因。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

const (
	maxNameLen   = 32
	maxParams    = 8
	maxVars      = 32
	maxStmts     = 64
	maxArgs      = 8
	maxFunctions = 256
)

// function 是已登记函数的内部表示。
type function struct {
	name    string
	k       int
	v       int
	stmts   []Stmt
	siteIDs []int // 每个 New 语句（按语句顺序）的全局分配点编号
	summary Summary
	classes []Class // 与 siteIDs 一一对应的分类
	rounds  int     // 实际分析轮数
}

// Registry 是函数登记表。所有方法可并发调用，
// 结果等价于某个串行顺序。
type Registry struct {
	mu           sync.Mutex
	funcs        map[string]*function
	nextSite     int // 下一个分配点编号，从 1 起
	analysisRuns int // 非导出计数器：实际执行的分析轮数
}

// NewRegistry 返回一个空的登记表。
func NewRegistry() *Registry {
	return &Registry{funcs: make(map[string]*function), nextSite: 1}
}

// Register 登记一个函数并在登记时完成逃逸分析。
// 被拒绝时不改变任何状态（包括分配点计数）。
func (r *Registry) Register(name string, k, v int, stmts []Stmt) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 第 1 阶段：参数非法（名字、k、V、语句数、变量编号、种类、
	// d 为 -1 用于非 Call、args 个数越界）。
	if err := validateShape(name, k, v, stmts); err != nil {
		return err
	}
	// 第 2 阶段：名字已登记。
	if _, ok := r.funcs[name]; ok {
		return errf(ErrDuplicateName, "function %q already registered", name)
	}
	// 第 3 阶段：函数数超限。
	if len(r.funcs) >= maxFunctions {
		return errf(ErrTooManyFunctions, "function count limit %d reached", maxFunctions)
	}
	// 第 4 阶段：按语句先后检查被调函数与实参个数。
	for i, st := range stmts {
		if st.Kind != Call {
			continue
		}
		ck := k
		if st.G != name {
			callee, ok := r.funcs[st.G]
			if !ok {
				return errf(ErrUnknownCallee, "stmt %d: callee %q is neither registered nor the function itself", i, st.G)
			}
			ck = callee.k
		}
		if len(st.Args) != ck {
			return errf(ErrArgCountMismatch, "stmt %d: call to %q takes %d args, want %d", i, st.G, len(st.Args), ck)
		}
	}

	// 校验全部通过后才开始改变状态：先分配全局分配点编号。
	f := &function{name: name, k: k, v: v, stmts: append([]Stmt(nil), stmts...)}
	for _, st := range stmts {
		if st.Kind == New {
			f.siteIDs = append(f.siteIDs, r.nextSite)
			r.nextSite++
		}
	}
	r.analyzeToFixpoint(f)
	r.funcs[name] = f
	return nil
}

// validateShape 执行第 1 阶段（参数非法）的全部检查。
func validateShape(name string, k, v int, stmts []Stmt) error {
	if len(name) == 0 || len(name) > maxNameLen {
		return errf(ErrInvalidArgs, "name length %d out of range [1, %d]", len(name), maxNameLen)
	}
	if k < 0 || k > maxParams {
		return errf(ErrInvalidArgs, "param count %d out of range [0, %d]", k, maxParams)
	}
	if v < k || v > maxVars {
		return errf(ErrInvalidArgs, "var count %d out of range [%d, %d]", v, k, maxVars)
	}
	if len(stmts) > maxStmts {
		return errf(ErrInvalidArgs, "stmt count %d exceeds %d", len(stmts), maxStmts)
	}
	inRange := func(x int) bool { return x >= 0 && x < v }
	for i, st := range stmts {
		switch st.Kind {
		case New:
			if !inRange(st.D) {
				return errf(ErrInvalidArgs, "stmt %d: New d=%d out of range", i, st.D)
			}
		case Copy, Store, Load:
			if !inRange(st.D) || !inRange(st.S) {
				return errf(ErrInvalidArgs, "stmt %d: d=%d s=%d out of range", i, st.D, st.S)
			}
		case Ret, Global:
			if !inRange(st.S) {
				return errf(ErrInvalidArgs, "stmt %d: s=%d out of range", i, st.S)
			}
		case Call:
			if st.D != -1 && !inRange(st.D) {
				return errf(ErrInvalidArgs, "stmt %d: Call d=%d out of range", i, st.D)
			}
			if len(st.Args) > maxArgs {
				return errf(ErrInvalidArgs, "stmt %d: %d call args exceeds %d", i, len(st.Args), maxArgs)
			}
			for j, a := range st.Args {
				if !inRange(a) {
					return errf(ErrInvalidArgs, "stmt %d: arg %d = %d out of range", i, j, a)
				}
			}
		default:
			return errf(ErrInvalidArgs, "stmt %d: invalid kind %d", i, int(st.Kind))
		}
	}
	return nil
}

// Summary 返回已登记函数的摘要与分析轮数。
func (r *Registry) Summary(name string) (Summary, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.funcs[name]
	if !ok {
		return Summary{}, 0, errf(ErrNotFound, "function %q not registered", name)
	}
	return f.summary, f.rounds, nil
}

// Sites 返回已登记函数的各分配点（编号升序）与分类。
func (r *Registry) Sites(name string) ([]Site, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.funcs[name]
	if !ok {
		return nil, errf(ErrNotFound, "function %q not registered", name)
	}
	sites := make([]Site, len(f.siteIDs))
	for i, id := range f.siteIDs {
		sites[i] = Site{ID: id, Class: f.classes[i]}
	}
	return sites, nil
}
