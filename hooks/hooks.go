// Package hooks 负责校验钩子的注册与触发调度：
// 前置钩子按注册顺序在每次写入生效前触发，
// 后置钩子在最外层动作全部写入应用后、提交前按全局注册顺序触发一次，
// 多个后置钩子失败按注册顺序聚合一并报告。
package hooks

import (
	"fmt"

	"ontology/spec"
)

// Kind 区分前置 / 后置校验钩子。
type Kind int

const (
	// Pre 前置钩子：在该类型任一实例被写入之前触发，看到写入生效前的状态。
	Pre Kind = iota
	// Post 后置钩子：最外层动作全部写入应用之后、事务提交之前触发一次。
	Post
)

func (k Kind) String() string {
	if k == Pre {
		return "pre"
	}
	return "post"
}

// StateView 是钩子可见的只读状态视图。
// 前置钩子拿到的是写入生效前的状态，后置钩子拿到的是
// 本次动作（含全部嵌套调用）结束时的最终状态。
// 视图只在钩子调用期间有效，钩子不得在返回后继续持有。
type StateView interface {
	// Get 返回指定实例的字段副本；不存在时 ok=false。
	Get(objectType, id string) (fields map[string]any, ok bool)
	// List 返回指定类型全部实例的 ID 列表（确定性升序）。
	List(objectType string) []string
	// Count 返回指定类型的实例数。
	Count(objectType string) int
}

// Context 是一次钩子触发的上下文。
type Context struct {
	Kind       Kind        // 前置 / 后置
	ObjectType string      // 钩子注册的对象类型
	Write      *spec.Write // 前置钩子：即将生效的写入；后置钩子为 nil
	State      StateView   // 只读状态视图
	TxID       uint64      // 所属最外层事务 ID
	Depth      int         // 触发时的嵌套深度（最外层为 0）
	ActionPath []string    // 动作调用路径（外层在前）
}

// Func 是校验钩子函数，返回非 nil error 表示校验失败。
type Func func(ctx Context) error

// Record 是一条钩子触发记录，用于审计与确定性对照。
type Record struct {
	Seq        int    // 事务内触发序号
	TxID       uint64 // 所属最外层事务 ID
	Kind       Kind   // 前置 / 后置
	Hook       string // 钩子名
	ObjectType string // 钩子注册的对象类型
	WriteID    string // 前置钩子：被写入的实例 ID
	Depth      int    // 触发时的嵌套深度
	ActionPath string // 动作调用路径，"a -> b" 形式
	Err        string // 钩子返回的错误文本，通过为空串
	TxOutcome  string // 所属事务最终结局："committed" / "rolled-back"
}

func (r Record) String() string {
	w := r.WriteID
	if w == "" {
		w = "-"
	}
	err := r.Err
	if err == "" {
		err = "ok"
	}
	return fmt.Sprintf("tx=%d seq=%d %s hook=%s type=%s write=%s depth=%d path=%s result=%s tx=%s",
		r.TxID, r.Seq, r.Kind, r.Hook, r.ObjectType, w, r.Depth, r.ActionPath, err, r.TxOutcome)
}

// Recorder 收集一个事务内的钩子触发记录。
type Recorder struct {
	records []Record
}

// NewRecorder 创建一个空记录器。
func NewRecorder() *Recorder { return &Recorder{} }

// Records 返回已收集的记录。
func (r *Recorder) Records() []Record { return r.records }

// Flush 给全部记录标注事务结局并返回，供并入全局钩子日志。
func (r *Recorder) Flush(outcome string) []Record {
	out := make([]Record, len(r.records))
	copy(out, r.records)
	for i := range out {
		out[i].TxOutcome = outcome
	}
	return out
}
