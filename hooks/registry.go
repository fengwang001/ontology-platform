package hooks

import (
	"fmt"
	"strings"
	"sync"

	"ontology/errs"
)

type entry struct {
	seq  uint64 // 全局注册序号，决定后置钩子的触发与聚合顺序
	name string
	fn   Func
}

// Registry 负责钩子注册：按对象类型分别保存前置 / 后置钩子，
// 各自保持注册顺序；同时维护后置钩子的全局注册顺序。
// 注册允许与执行并发（内部加锁），但通常在建模阶段完成。
type Registry struct {
	mu        sync.RWMutex
	nextSeq   uint64
	pre       map[string][]entry
	post      map[string][]entry
	postOrder []entryWithType // 后置钩子的全局注册顺序
}

type entryWithType struct {
	objectType string
	entry
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		pre:  make(map[string][]entry),
		post: make(map[string][]entry),
	}
}

// RegisterPre 为对象类型注册前置钩子，同类型内按注册顺序触发。
func (r *Registry) RegisterPre(objectType, name string, fn Func) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pre[objectType] = append(r.pre[objectType], entry{seq: r.nextSeq, name: name, fn: fn})
	r.nextSeq++
}

// RegisterPost 为对象类型注册后置钩子；
// 跨类型的触发顺序由全局注册顺序决定。
func (r *Registry) RegisterPost(objectType, name string, fn Func) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := entry{seq: r.nextSeq, name: name, fn: fn}
	r.post[objectType] = append(r.post[objectType], e)
	r.postOrder = append(r.postOrder, entryWithType{objectType: objectType, entry: e})
	r.nextSeq++
}

// Dispatcher 负责钩子的触发调度：
// 前置钩子逐次写入触发、首个失败即短路；
// 后置钩子只在最外层事务提交阶段触发一次，失败聚合一并报告。
type Dispatcher struct {
	reg *Registry
}

// NewDispatcher 基于注册表创建调度器。
func NewDispatcher(reg *Registry) *Dispatcher { return &Dispatcher{reg: reg} }

// FirePre 在指定类型的一次写入生效前，按注册顺序触发该类型的全部
// 前置钩子；任一钩子失败立即短路，返回归一化的 KindPreHook 错误。
func (d *Dispatcher) FirePre(ctx Context, rec *Recorder) error {
	d.reg.mu.RLock()
	entries := append([]entry(nil), d.reg.pre[ctx.ObjectType]...)
	d.reg.mu.RUnlock()

	for _, e := range entries {
		hookErr := e.fn(ctx)
		rec.records = append(rec.records, Record{
			Seq:        len(rec.records),
			TxID:       ctx.TxID,
			Kind:       Pre,
			Hook:       e.name,
			ObjectType: ctx.ObjectType,
			WriteID:    ctx.Write.ID,
			Depth:      ctx.Depth,
			ActionPath: strings.Join(ctx.ActionPath, " -> "),
			Err:        errText(hookErr),
		})
		if hookErr != nil {
			return &errs.Error{
				Kind:       errs.KindPreHook,
				ActionPath: append([]string(nil), ctx.ActionPath...),
				ObjectType: ctx.ObjectType,
				ObjectID:   ctx.Write.ID,
				HookName:   e.name,
				Message:    hookErr.Error(),
			}
		}
	}
	return nil
}

// FirePost 在最外层事务提交阶段触发一次：对本次事务实际写入过的
// 每个类型，按全局注册顺序触发其后置钩子。与前置钩子不同，
// 后置钩子失败不短路——收集全部失败结果，按注册顺序聚合成
// PostHookError 一并报告。
func (d *Dispatcher) FirePost(affected map[string]bool, ctx Context, rec *Recorder) error {
	d.reg.mu.RLock()
	ordered := append([]entryWithType(nil), d.reg.postOrder...)
	d.reg.mu.RUnlock()

	var failures []errs.HookFailure
	for _, e := range ordered {
		if !affected[e.objectType] {
			continue
		}
		hookCtx := ctx
		hookCtx.ObjectType = e.objectType
		hookErr := e.fn(hookCtx)
		rec.records = append(rec.records, Record{
			Seq:        len(rec.records),
			TxID:       ctx.TxID,
			Kind:       Post,
			Hook:       e.name,
			ObjectType: e.objectType,
			Depth:      ctx.Depth,
			ActionPath: strings.Join(ctx.ActionPath, " -> "),
			Err:        errText(hookErr),
		})
		if hookErr != nil {
			failures = append(failures, errs.HookFailure{
				HookName:   e.name,
				ObjectType: e.objectType,
				Message:    hookErr.Error(),
			})
		}
	}
	if len(failures) > 0 {
		return &errs.PostHookError{
			ActionPath: append([]string(nil), ctx.ActionPath...),
			Failures:   failures,
		}
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
