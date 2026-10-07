package hypcheck

import (
	"fmt"
	"sort"
)

// frozenState 是前置阶段演算开始时冻结的只读对象快照。
// 后置钩子只允许读取它；意图集合是独立结构，绝不回写快照。
type frozenState struct {
	vals map[string]Value
}

func (fs *frozenState) get(key string) (Value, bool) {
	v, ok := fs.vals[key]
	return v, ok
}

func specPhase(k SpecKind) Phase {
	switch k {
	case PreDenyParamEqual, PreDenyStateEqual:
		return PhasePre
	case PostDenyStateEqual, PostDenyIntendedSet:
		return PhasePost
	default:
		return ""
	}
}

// validateParams 按当时生效的结构约束校验参数：
// 缺必填、类型不符、出现未声明字段都构成 E4；错误描述按字段名排序。
func validateParams(s Schema, params Params) []string {
	var errs []string
	declared := map[string]Field{}
	for _, f := range s.Fields {
		declared[f.Name] = f
	}
	for _, f := range s.Fields {
		v, ok := params[f.Name]
		if !ok {
			if f.Required {
				errs = append(errs, fmt.Sprintf("missing required field %q", f.Name))
			}
			continue
		}
		if v.Kind != ValueKind(f.Type) {
			errs = append(errs, fmt.Sprintf("field %q has type %s, want %s", f.Name, v.Kind, f.Type))
		}
	}
	var extra []string
	for k := range params {
		if _, ok := declared[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		errs = append(errs, fmt.Sprintf("undeclared field %q", k))
	}
	sort.Strings(errs)
	return errs
}

// renderEffects 依据当时类型定义把参数 / 常量展开为状态改变意图（不落地）。
func renderEffects(tv TypeVersion, params Params) []Effect {
	out := make([]Effect, 0, len(tv.Effects))
	for _, d := range tv.Effects {
		v := d.Const
		if d.Param != "" {
			v = params[d.Param]
		}
		out = append(out, Effect{Op: "set", Key: d.Key, Value: v})
	}
	return out
}

func denyMessage(spec HookSpec) string {
	switch spec.Kind {
	case PreDenyParamEqual:
		return fmt.Sprintf("param %q equals forbidden value", spec.Param)
	case PreDenyStateEqual, PostDenyStateEqual:
		return fmt.Sprintf("state %q equals forbidden value", spec.Key)
	case PostDenyIntendedSet:
		return fmt.Sprintf("intended write to %q is forbidden", spec.Key)
	default:
		return "hook denied"
	}
}

func hookFires(spec HookSpec, fs *frozenState, params Params, intended []Effect) bool {
	switch spec.Kind {
	case PreDenyParamEqual:
		v, ok := params[spec.Param]
		return ok && valueEqual(v, spec.Value)
	case PreDenyStateEqual, PostDenyStateEqual:
		v, ok := fs.get(spec.Key)
		return ok && valueEqual(v, spec.Value)
	case PostDenyIntendedSet:
		for _, ef := range intended {
			if ef.Key == spec.Key && valueEqual(ef.Value, spec.Value) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func valueEqual(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindInt:
		return a.Int == b.Int
	case KindStr:
		return a.Str == b.Str
	case KindBool:
		return a.Bool == b.Bool
	default:
		return false
	}
}

// runPre 演算前置阶段：权限闸门最先，随后前置钩子按 hookID 升序。
// collectAll=false 遇首个失败即短路；true 聚合本阶段全部失败。
// 规定：权限失败与钩子失败同属前置阶段，可在 collectAll 下一并聚合。
func runPre(hooks []ResolvedHook, trace *PermTrace, fs *frozenState, params Params, collectAll bool) []PhaseFailure {
	var fails []PhaseFailure
	if trace == nil || !trace.Allowed {
		fails = append(fails, PhaseFailure{
			Phase:   PhasePre,
			Code:    FailurePermissionDenied,
			Message: "caller has no effective permission at the requested time",
		})
		if !collectAll {
			return fails
		}
	}
	for _, h := range hooks {
		if h.Phase != PhasePre {
			continue
		}
		if hookFires(h.Spec, fs, params, nil) {
			fails = append(fails, PhaseFailure{
				Phase:   PhasePre,
				Code:    FailureHookDenied,
				HookID:  h.HookID,
				Message: denyMessage(h.Spec),
			})
			if !collectAll {
				return fails
			}
		}
	}
	return fails
}

// runPost 演算后置阶段；仅在前置阶段无失败时被调用。
// 聚合规则规定：前置阶段失败时后置阶段绝不演算，故两类失败永不同时出现。
// 它读取 frozenState（不修改）与意图集合；collectAll 聚合本阶段全部失败。
func runPost(hooks []ResolvedHook, fs *frozenState, params Params, intended []Effect, collectAll bool) []PhaseFailure {
	var fails []PhaseFailure
	for _, h := range hooks {
		if h.Phase != PhasePost {
			continue
		}
		if hookFires(h.Spec, fs, params, intended) {
			fails = append(fails, PhaseFailure{
				Phase:   PhasePost,
				Code:    FailureHookDenied,
				HookID:  h.HookID,
				Message: denyMessage(h.Spec),
			})
			if !collectAll {
				return fails
			}
		}
	}
	return fails
}
