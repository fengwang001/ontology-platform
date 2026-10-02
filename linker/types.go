// Package linker 实现一个带循环依赖、暂时性死区（TDZ）与错误粘滞的
// 模块链接与求值器。
//
// 一次 Evaluate 是原子步骤：链接与求值在同一会话级互斥量内完成，
// 任何观察者都不会看到链接了一部分或求值到一半的状态。
package linker

// BindingKind 为本地导出的种类。
type BindingKind int

const (
	// KindFunction 函数导出：链接成功后即视为已初始化。
	KindFunction BindingKind = iota
	// KindLet let 导出：在体中被 Init 之前处于未初始化（TDZ）。
	KindLet
)

func (k BindingKind) String() string {
	switch k {
	case KindFunction:
		return "function"
	case KindLet:
		return "let"
	default:
		return "unknown"
	}
}

// ModuleStatus 为模块的生命周期状态。
type ModuleStatus int

const (
	StatusRegistered ModuleStatus = iota
	StatusLinked
	StatusEvaluating
	StatusEvaluated
	StatusErrored
)

func (s ModuleStatus) String() string {
	switch s {
	case StatusRegistered:
		return "registered"
	case StatusLinked:
		return "linked"
	case StatusEvaluating:
		return "evaluating"
	case StatusEvaluated:
		return "evaluated"
	case StatusErrored:
		return "errored"
	default:
		return "unknown"
	}
}

// ImportStmt 为一条导入语句（来源模块名, 绑定名列表）。
type ImportStmt struct {
	Source   string
	Bindings []string
}

// Export 为一个本地导出（绑定名, 种类）。
type Export struct {
	Name string
	Kind BindingKind
}

// NamedReExport 为一条具名转出（导出名, 来源模块名, 来源名）。
type NamedReExport struct {
	Name   string
	Source string
	From   string
}

// StarReExport 为一条星转出（来源模块名）。
type StarReExport struct {
	Source string
}

// ReExport 为转出列表中的一项：Name 为空表示星转出。
type ReExport struct {
	Name   string
	Source string
	From   string
}

// IsStar 报告该转出是否为星转出。
func (r ReExport) IsStar() bool { return r.Name == "" }

// StepKind 为体步骤的种类。
type StepKind int

const (
	StepInit StepKind = iota
	StepRead
	StepThrow
)

// Step 为体中的一条步骤。
//   - Init：Kind=StepInit，Name=要初始化的本模块 let 导出名
//   - Read：Kind=StepRead，Module=被读模块名，Name=绑定名
//   - Throw：Kind=StepThrow，Message=错误消息
type Step struct {
	Kind    StepKind
	Name    string
	Module  string
	Message string
}

// Init 构造一条 Init(name) 步骤。
func Init(name string) Step { return Step{Kind: StepInit, Name: name} }

// Read 构造一条 Read(module, name) 步骤。
func Read(module, name string) Step { return Step{Kind: StepRead, Module: module, Name: name} }

// Throw 构造一条 Throw(message) 步骤。
func Throw(message string) Step { return Step{Kind: StepThrow, Message: message} }

// ModuleDef 为 AddModule 的模块定义。
type ModuleDef struct {
	Name      string
	Imports   []ImportStmt
	Exports   []Export
	ReExports []ReExport
	Body      []Step
}

// StatusResult 为 Status 查询的结果。
type StatusResult struct {
	Status ModuleStatus
	// Lets 为本模块各 let 导出的初始化位（按导出声明次序）。
	Lets map[string]bool
	// Error 为出错状态下粘滞的错误值。
	Error string
}

// EvalResult 为一次 Evaluate 的结果。
type EvalResult struct {
	// Appended 为本次调用追加到求值完成次序的模块名（按追加次序）。
	Appended []string
	// OK 为根模块是否成功。
	OK bool
	// Error 为失败时粘滞在根模块上的错误值。
	Error string
}

// binding 为导出解析得到的绑定（定义模块, 本地名）。
type binding struct {
	module string
	name   string
}
