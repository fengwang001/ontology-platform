// Package api 组装 hook / schedule / snapshot，暴露唯一入口 Validate。
package api

import (
	"errors"
	"fmt"
	"strings"

	"ontology/hook"
	"ontology/schedule"
	"ontology/snapshot"
)

var (
	// ErrPreFailed pre 阶段至少一条钩子失败（变更未发生）。
	ErrPreFailed = errors.New("ontology: pre-hook validation failed")
	// ErrPostFailed post 阶段至少一条钩子失败（变更应回滚）。
	ErrPostFailed = errors.New("ontology: post-hook validation failed")
	// ErrHookInternal 钩子内部错误（panic），可与阶段哨兵同时 errors.Is。
	ErrHookInternal = errors.New("ontology: hook internal error")
	// ErrInvalidArgument Validate 参数不合法，不进入任何钩子。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
)

// Object 待变更对象：类型名 + 当前属性。
type Object struct {
	Type  string
	Attrs map[string]any
}

// Change 属性级 upsert 变更。
type Change struct {
	Set map[string]any
}

// Failure 单条钩子失败。
type Failure struct {
	Hook   string
	Phase  hook.Phase
	Reason string
	Err    error // panic 恢复时非空，包装 ErrHookInternal
}

// ValidationError 同阶段聚合的全部失败。
type ValidationError struct {
	Phase    hook.Phase
	Failures []Failure
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s 阶段校验失败（%d 条）:", e.Phase, len(e.Failures))
	for _, f := range e.Failures {
		fmt.Fprintf(&b, " [%s] %s;", f.Hook, f.Reason)
	}
	return b.String()
}

// Unwrap 同时暴露阶段哨兵与各失败携带的内部错误，供 errors.Is 区分。
func (e *ValidationError) Unwrap() []error {
	sentinel := ErrPreFailed
	if e.Phase == hook.Post {
		sentinel = ErrPostFailed
	}
	errs := []error{sentinel}
	for _, f := range e.Failures {
		if f.Err != nil {
			errs = append(errs, f.Err)
		}
	}
	return errs
}

// Validator 持有注册表的校验入口。
type Validator struct {
	registry *hook.Registry
}

// NewValidator 用给定注册表构造校验器。
func NewValidator(r *hook.Registry) *Validator { return &Validator{registry: r} }

// Validate 是唯一入口：参数校验 → pre 聚合 → 应用变更 → post 聚合。
// pre 任一失败则变更不发生、post 不运行；post 失败由调用方回滚。
func (v *Validator) Validate(obj Object, ch Change) error {
	if obj.Type == "" {
		return fmt.Errorf("%w: object type is empty", ErrInvalidArgument)
	}
	if len(ch.Set) == 0 {
		return fmt.Errorf("%w: change set is empty", ErrInvalidArgument)
	}

	plan := schedule.Plan(v.registry.Match(obj.Type))

	preSnap := snapshot.Freeze(obj.Type, obj.Attrs)
	if failures := runPhase(plan, hook.Pre, preSnap); len(failures) > 0 {
		return &ValidationError{Phase: hook.Pre, Failures: failures}
	}

	// pre 全过：应用变更，冻结变更后快照。
	newAttrs := make(map[string]any, len(obj.Attrs)+len(ch.Set))
	for k, val := range obj.Attrs {
		newAttrs[k] = val
	}
	for k, val := range ch.Set {
		newAttrs[k] = val
	}
	postSnap := snapshot.Freeze(obj.Type, newAttrs)
	if failures := runPhase(plan, hook.Post, postSnap); len(failures) > 0 {
		return &ValidationError{Phase: hook.Post, Failures: failures}
	}

	return nil
}

// runPhase 按编排顺序运行指定阶段的全部钩子，聚合所有失败（不首错即停）。
func runPhase(plan []hook.Hook, phase hook.Phase, snap snapshot.Snapshot) []Failure {
	var failures []Failure
	for _, h := range plan {
		if h.Phase != phase {
			continue
		}
		if f, failed := runOne(h, snap); failed {
			failures = append(failures, f)
		}
	}
	return failures
}

// runOne 运行单个钩子；panic 恢复为携带 ErrHookInternal 的失败。
func runOne(h hook.Hook, snap snapshot.Snapshot) (failure Failure, failed bool) {
	defer func() {
		if r := recover(); r != nil {
			failure = Failure{
				Hook:   h.Name,
				Phase:  h.Phase,
				Reason: fmt.Sprintf("panic: %v", r),
				Err:    fmt.Errorf("%w: hook %q: %v", ErrHookInternal, h.Name, r),
			}
			failed = true
		}
	}()
	pass, reason := h.Check(snap)
	if !pass {
		return Failure{Hook: h.Name, Phase: h.Phase, Reason: reason}, true
	}
	return Failure{}, false
}
