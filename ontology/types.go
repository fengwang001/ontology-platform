package ontology

import (
	"errors"
	"fmt"
)

// Property 描述对象类型上的属性；TypeRef 指向属性值所引用的对象类型（可选）。
type Property struct {
	Name    string
	TypeRef string
}

// Link 描述从 SourceType 指向 TargetType 的链接，两侧类型名均属于引用。
type Link struct {
	Name       string
	SourceType string
	TargetType string
}

// Param 描述 Action 参数；TypeRef 为参数引用的对象类型（可选）。
type Param struct {
	Name    string
	TypeRef string
}

// Action 描述一个操作：其参数、输入集合以及副作用（创建/读取/更新/删除）都可能引用对象类型。
type Action struct {
	Name    string
	Params  []Param
	Inputs  []string
	Creates []string
	Reads   []string
	Updates []string
	Deletes []string
}

// ObjectType 是本体中的对象类型，RID 在重命名前后保持稳定，Name 为当前生效名称。
type ObjectType struct {
	RID        string
	Name       string
	Properties []Property
	Links      []Link
}

// pendingRename 记录一次已完成暂存但尚未提交（或未完成回滚）的重命名。
// 该结构会随图一起持久化，用于在重新加载时识别“中断的半改状态”。
type pendingRename struct {
	RID     string
	OldName string
	NewName string
}

// Graph 是对象类型、Action 与未决重命名记录组成的完整本体快照，重命名以不可变快照替换方式提交。
type Graph struct {
	Types   []*ObjectType
	Actions []*Action
	Pending *pendingRename
}

// Lookup 按当前名称查找对象类型。
func (g *Graph) Lookup(name string) *ObjectType {
	if g == nil {
		return nil
	}
	for _, t := range g.Types {
		if t != nil && t.Name == name {
			return t
		}
	}
	return nil
}

// Resolve 按名称解析对象类型：旧名已失效时必须返回 false，绝不回指到重命名后的类型。
func (g *Graph) Resolve(name string) (*ObjectType, bool) {
	t := g.Lookup(name)
	return t, t != nil
}

// VerificationError 描述图校验失败的原因，包含判定依据。
type VerificationError struct {
	Kind    string
	Message string
	Detail  string
}

func (e *VerificationError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Message, e.Detail)
}

// Is 支持 errors.Is(err, ErrXxx) 按 Kind 区分拒绝原因（Detail 不同也视为同一类原因）。
func (e *VerificationError) Is(target error) bool {
	var ve *VerificationError
	if errors.As(target, &ve) {
		return e.Kind == ve.Kind
	}
	return false
}

// 可区分的拒绝原因：
var (
	// ErrTypeNotFound 旧名不存在（含旧名已失效后再次被使用的情形由 ErrStaleName 单独表达）。
	ErrTypeNotFound = &VerificationError{Kind: "TypeNotFound", Message: "object type with old name does not exist"}
	// ErrNameConflict 新名与图中现有类型重名。
	ErrNameConflict = &VerificationError{Kind: "NameConflict", Message: "new name already exists"}
	// ErrDanglingReference 图中存在引用了不存在类型名的位置。
	ErrDanglingReference = &VerificationError{Kind: "DanglingReference", Message: "dangling type reference detected"}
	// ErrResidualOldName 重命名后仍有引用位置残留旧名。
	ErrResidualOldName = &VerificationError{Kind: "ResidualOldName", Message: "residual reference to old name after rename"}
	// ErrStaleName 旧名重命名后仍可解析到原类型（失效规则被破坏）。
	ErrStaleName = &VerificationError{Kind: "StaleName", Message: "old name still resolves after rename"}
	// ErrHalfApplied 图处于重命名中断产生的半改状态（暂存后既未提交也未回滚）。
	ErrHalfApplied = &VerificationError{Kind: "HalfAppliedRename", Message: "interrupted rename left graph in a half-applied state"}
)
