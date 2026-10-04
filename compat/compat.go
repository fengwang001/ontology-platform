// Package compat 判定按读者模式 R 解读按写者模式 W 写出的数据是否可行。
package compat

import (
	"sync/atomic"

	"ontology/schema"
)

// Reason 是违规原因。
type Reason int

const (
	// MissingNoDefault：W 无同名字段且 R 侧字段无默认值（不论 required）。
	MissingNoDefault Reason = iota
	// TypeMismatch：W 有同名字段但类型不可提升（写出类型→读者类型）。
	TypeMismatch
	// OptionalToRequired：类型可提升但写出侧非 required 而读者 required 且无默认值。
	OptionalToRequired
)

func (r Reason) String() string {
	switch r {
	case MissingNoDefault:
		return "MissingNoDefault"
	case TypeMismatch:
		return "TypeMismatch"
	case OptionalToRequired:
		return "OptionalToRequired"
	}
	return "unknown"
}

// Violation 是 CanRead 发现的第一个违规（按 R 的字段次序）。
type Violation struct {
	Field  string
	Reason Reason
}

func (v Violation) String() string {
	return v.Field + ": " + v.Reason.String()
}

// compared 记录 CanRead 累计检查过的 R 侧字段数（复杂度审计用，非导出）。
var compared atomic.Int64

// Compared 返回 compared 的当前值。
func Compared() int64 {
	return compared.Load()
}

// ResetCompared 将 compared 清零（测试用）。
func ResetCompared() {
	compared.Store(0)
}

// Promotable 报告写出类型 from 是否可提升为读者类型 to。
// 仅同型、int32→int64、int32→float64、string→bytes，均不可反向。
func Promotable(from, to schema.Type) bool {
	if from == to {
		return true
	}
	switch from {
	case schema.Int32:
		return to == schema.Int64 || to == schema.Float64
	case schema.String:
		return to == schema.Bytes
	}
	return false
}

// CanRead 判定按 R 解读的读者能否读按 W 写出的数据。
// 按 R 的字段次序逐个检查，返回第一个违规；W 中多出的字段忽略；nil 表示可读。
func CanRead(R, W []schema.Field) *Violation {
	index := make(map[string]schema.Field, len(W))
	for _, f := range W {
		index[f.Name] = f
	}
	for _, rf := range R {
		compared.Add(1)
		wf, ok := index[rf.Name]
		if !ok {
			if !rf.HasDefault {
				return &Violation{Field: rf.Name, Reason: MissingNoDefault}
			}
			continue
		}
		if !Promotable(wf.Type, rf.Type) {
			return &Violation{Field: rf.Name, Reason: TypeMismatch}
		}
		if !wf.Required && rf.Required && !rf.HasDefault {
			return &Violation{Field: rf.Name, Reason: OptionalToRequired}
		}
	}
	return nil
}
