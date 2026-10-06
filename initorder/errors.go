package initorder

import "errors"

// 可区分的错误类别，调用方可用 errors.Is 判定。
var (
	// ErrInvalidArgument 表示登记参数非法（空变量列表、非法标识符、
	// 函数名为空白标识符、引用集合含空白标识符等）。
	ErrInvalidArgument = errors.New("initorder: invalid argument")
	// ErrDuplicateDeclaration 表示重复声明（与已接受的变量、函数、
	// 预声明标识符重名，或同一单元内两个非空白变量同名）。
	ErrDuplicateDeclaration = errors.New("initorder: duplicate declaration")
	// ErrUndeclaredReference 表示求解时发现未声明引用。
	ErrUndeclaredReference = errors.New("initorder: undeclared reference")
	// ErrInitializationCycle 表示求解时发现初始化环。
	ErrInitializationCycle = errors.New("initorder: initialization cycle")
)

// InvalidArgumentError 描述非法参数的具体原因。
type InvalidArgumentError struct {
	Reason string
}

func (e *InvalidArgumentError) Error() string {
	return ErrInvalidArgument.Error() + ": " + e.Reason
}

func (e *InvalidArgumentError) Is(target error) bool { return target == ErrInvalidArgument }

// DuplicateDeclarationError 描述重复声明的名字。
type DuplicateDeclarationError struct {
	Name string
}

func (e *DuplicateDeclarationError) Error() string {
	return ErrDuplicateDeclaration.Error() + ": " + e.Name
}

func (e *DuplicateDeclarationError) Is(target error) bool { return target == ErrDuplicateDeclaration }

// DeclKind 区分声明种类（变量单元或函数声明）。
type DeclKind int

const (
	// DeclKindVars 变量单元。
	DeclKindVars DeclKind = iota
	// DeclKindFunc 函数声明。
	DeclKindFunc
)

func (k DeclKind) String() string {
	if k == DeclKindFunc {
		return "func"
	}
	return "vars"
}

// UndeclaredReferenceError 报告登记次序最早的、含有未声明引用的声明，
// 以及其中字典序最小的未声明标识符。
type UndeclaredReferenceError struct {
	DeclIndex int      // 该声明在全部已接受声明中的登记次序（从 0 开始）
	Kind      DeclKind // 声明种类
	Name      string   // 函数声明的函数名；变量单元为空
	Ident     string   // 该声明中字典序最小的未声明标识符
}

func (e *UndeclaredReferenceError) Error() string {
	return ErrUndeclaredReference.Error() + ": " + e.Ident
}

func (e *UndeclaredReferenceError) Is(target error) bool { return target == ErrUndeclaredReference }

// InitializationCycleError 报告无法完成初始化的全部变量，
// 按源码次序排列，不含空白标识符。
type InitializationCycleError struct {
	Vars []string
}

func (e *InitializationCycleError) Error() string {
	return ErrInitializationCycle.Error()
}

func (e *InitializationCycleError) Is(target error) bool { return target == ErrInitializationCycle }
