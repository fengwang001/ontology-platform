// Package resolve 依据某次已提交的不可变快照解析读写目标。
package resolve

import (
	"errors"
	"sort"

	"ontology/indexreg"
)

// 可由 errors.Is 区分的哨兵错误。
var (
	// ErrNameNotFound 表示名字既非索引也非别名。
	ErrNameNotFound = errors.New("resolve: name not found")
	// ErrNoWriteIndex 表示别名存在但推不出写索引。alias 包复用此哨兵。
	ErrNoWriteIndex = errors.New("alias: no write index")
)

// MemberView 是解析时可见的成员视图（带 filter）。
type MemberView struct {
	Index  string
	Filter string
}

// Tracer 记录解析过程触碰的成员记录数。
// 证明写解析不随别名成员数增长：ResolveWrite 触碰记录数不超过 1。
type Tracer struct{ touched int }

// NewTracer 创建一个零计数的 Tracer。
func NewTracer() *Tracer { return &Tracer{} }

// Touch 记录触碰一条成员记录。
func (t *Tracer) Touch() { t.touched++ }

// Touched 返回累计触碰的成员记录数。
func (t *Tracer) Touched() int { return t.touched }

// View 是解析所依据的不可变状态视图，由状态持有者在锁内构造。
type View interface {
	// IndexState 返回索引的开闭状态。
	IndexState(name string) (indexreg.State, bool)
	// AliasWrite 返回别名推定的写索引；命中写成员时经 tracer 记一次触碰。
	AliasWrite(aliasName string, tracer *Tracer) (indexName string, ok bool)
	// AliasMembers 返回别名的全部成员（任意次序，解析器自行排序过滤）。
	AliasMembers(aliasName string) []MemberView
}

// Resolver 对 View 做读写解析；零值即可用。
type Resolver struct{}

// New 创建解析器。
func New() *Resolver { return &Resolver{} }

// Read 解析读目标：
//   - 名字是索引：返回仅含该索引自身的列表；已关闭报 indexreg.ErrIndexClosed。
//   - 名字是别名：返回未关闭成员（按索引名字节序，各带 filter）；
//     关闭成员静默跳过，全部关闭返回空列表而不报错。
//   - 都不是：返回 ErrNameNotFound。
func (r *Resolver) Read(v View, name string) ([]MemberView, error) {
	if st, isIndex := v.IndexState(name); isIndex {
		if st == indexreg.Closed {
			return nil, indexreg.ErrIndexClosed
		}
		return []MemberView{{Index: name, Filter: ""}}, nil
	}

	members := v.AliasMembers(name)
	if members == nil {
		// AliasMembers 对“非别名”返回 nil；现存但成员为空的别名不会出现
		//（成员全移除的别名随之消失）。
		return nil, ErrNameNotFound
	}

	open := make([]MemberView, 0, len(members))
	for _, mem := range members {
		st, ok := v.IndexState(mem.Index)
		if ok && st == indexreg.Open {
			open = append(open, mem)
		}
		// 成员索引不存在或已关闭：静默跳过。
	}
	sortMembers(open)
	return open, nil
}

// Write 解析写目标：
//   - 名字是索引：返回它自身；已关闭报 indexreg.ErrIndexClosed。
//   - 名字是别名：返回推定写索引；无写索引报 alias.ErrNoWriteIndex；
//     写索引已关闭报 indexreg.ErrIndexClosed，不回落到其他成员。
//   - 都不是：返回 ErrNameNotFound。
//
// 无论别名有多少成员，本方法至多触碰一条成员记录（tracer 计数 ≤ 1）。
func (r *Resolver) Write(v View, name string, tracer *Tracer) (string, error) {
	if st, isIndex := v.IndexState(name); isIndex {
		if st == indexreg.Closed {
			return "", indexreg.ErrIndexClosed
		}
		return name, nil
	}

	writeIndex, ok := v.AliasWrite(name, tracer)
	if !ok {
		// 区分“别名存在但无写索引”与“名字不存在”：存在的别名必有成员，
		// 借 AliasMembers 判定，返回切片非 nil 即别名存在。
		if v.AliasMembers(name) != nil {
			return "", ErrNoWriteIndex
		}
		return "", ErrNameNotFound
	}

	st, exists := v.IndexState(writeIndex)
	if !exists || st == indexreg.Closed {
		return "", indexreg.ErrIndexClosed
	}
	return writeIndex, nil
}

func sortMembers(members []MemberView) {
	sort.Slice(members, func(i, j int) bool { return members[i].Index < members[j].Index })
}
