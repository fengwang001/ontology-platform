// Package caps 定义协议特性表、端点声明与统一错误类型。
package caps

// Feature 编号范围 0..31。
const (
	NumFeatures = 32
	MaxVersion  = 1_000_000_000
	Unbounded   = MaxVersion + 1 // maxV 取此值表示无上限
)

// Spec 描述单个特性：f 在版本 v 可用当且仅当 MinV <= v < MaxV；
// Role 为使用该特性所需的最低角色（0..2）。
type Spec struct {
	MinV int
	MaxV int
	Role int
}

// Hello 是端点声明：版本区间 [Lo,Hi]、支持位集 Sup、必需位集 Req。
type Hello struct {
	Lo  int
	Hi  int
	Sup uint32
	Req uint32
}

// Code 标识拒绝原因。
type Code int

const (
	CodeInvalid Code = iota
	CodeForbidden
	CodeNoSession
	CodeNoVersion
	CodeMissing
	CodeDenied
	CodeWindow
	CodeNotEnabled
)

// Error 携带拒绝码与相关特性编号（无特性时为 -1）。
type Error struct {
	Code    Code
	Feature int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch e.Code {
	case CodeInvalid:
		return "caps: invalid argument"
	case CodeForbidden:
		return "caps: server role must be 2"
	case CodeNoSession:
		return "caps: session not found or closed"
	case CodeNoVersion:
		return "caps: no common version range"
	case CodeMissing:
		return "caps: required feature not supported by both ends"
	case CodeDenied:
		return "caps: required feature denied by client role"
	case CodeWindow:
		return "caps: required feature has no common effective window"
	case CodeNotEnabled:
		return "caps: feature is not enabled in session"
	default:
		return "caps: unknown error"
	}
}

// Is 支持 errors.Is(err, ErrXxx) 按码比较。
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	t, ok := target.(*Error)
	return ok && e.Code == t.Code
}

func errCode(c Code) *Error        { return &Error{Code: c, Feature: -1} }
func errFeat(c Code, f int) *Error { return &Error{Code: c, Feature: f} }

// 无特性编号的哨兵错误，供 errors.Is 使用。
var (
	ErrInvalid    = errCode(CodeInvalid)
	ErrForbidden  = errCode(CodeForbidden)
	ErrNoSession  = errCode(CodeNoSession)
	ErrNoVersion  = errCode(CodeNoVersion)
	ErrWindow     = errCode(CodeWindow)
	ErrNotEnabled = errCode(CodeNotEnabled)
)

// ErrFeature 构造携带特性编号的 Missing/Denied/Window 错误。
func ErrFeature(c Code, f int) *Error { return errFeat(c, f) }

// Table 是 32 个特性的静态规格表。
type Table struct {
	specs [NumFeatures]Spec
}

// NewTable 以 32 个特性规格构造特性表；任一规格非法返回 CodeInvalid。
// 拒绝不影响调用方已持有的任何表。
func NewTable(specs [NumFeatures]Spec) (*Table, *Error) {
	for i := range specs {
		if !ValidSpec(specs[i]) {
			return nil, errFeat(CodeInvalid, i)
		}
	}
	return &Table{specs: specs}, nil
}

// ValidSpec 校验构造参数。
func ValidSpec(s Spec) bool {
	return s.MinV >= 1 && s.MinV <= MaxVersion &&
		s.MaxV > s.MinV && s.MaxV <= Unbounded &&
		s.Role >= 0 && s.Role <= 2
}

// ValidHello 校验端点声明的内部一致性。
func ValidHello(h Hello) bool {
	return h.Lo >= 1 && h.Lo <= MaxVersion &&
		h.Hi >= 1 && h.Hi <= MaxVersion &&
		h.Lo <= h.Hi &&
		h.Req&^h.Sup == 0
}

// ValidRole 校验角色取值 0..2。
func ValidRole(r int) bool { return r >= 0 && r <= 2 }

// SpecAt 返回特性 f 的规格。
func (t *Table) SpecAt(f int) Spec { return t.specs[f] }

// AvailableAt 判断特性 f 在版本 v 是否可用。
func (t *Table) AvailableAt(f, v int) bool {
	if f < 0 || f >= NumFeatures {
		return false
	}
	s := t.specs[f]
	return s.MinV <= v && v < s.MaxV
}
