// Package quota 实现多租户命名空间的资源配额准入控制器。
//
// 包内模块划分：
//   - types.go      核心数据类型与参数校验
//   - errors.go     可区分的错误类别及其优先级
//   - defaults.go   默认值补全（被记账的值 = 补全结果）
//   - scope.go      服务等级 / 期限属性与作用域匹配、配额配置校验
//   - controller.go 准入、原位调整、删除、配额 CRUD 与一致性自检
package quota

import (
	"fmt"
	"strings"
)

// ResourceName 是配额硬上限中的资源名，只允许三种形式：
// "pods"、"requests.X"、"limits.X"。
type ResourceName string

// ResourcePods 按 Pod 个数记账的资源。
const ResourcePods ResourceName = "pods"

const (
	requestsPrefix = "requests."
	limitsPrefix   = "limits."
)

// RequestsFor 返回资源 base 的请求量记账名。
func RequestsFor(base string) ResourceName { return ResourceName(requestsPrefix + base) }

// LimitsFor 返回资源 base 的上限量记账名。
func LimitsFor(base string) ResourceName { return ResourceName(limitsPrefix + base) }

// IsValid 报告资源名是否为三种合法形式之一。
func (r ResourceName) IsValid() bool {
	if r == ResourcePods {
		return true
	}
	_, ok := r.Base()
	return ok
}

// Base 对 requests.X / limits.X 返回基础资源名 X；pods 不返回。
func (r ResourceName) Base() (string, bool) {
	if s, ok := strings.CutPrefix(string(r), requestsPrefix); ok && s != "" {
		return s, true
	}
	if s, ok := strings.CutPrefix(string(r), limitsPrefix); ok && s != "" {
		return s, true
	}
	return "", false
}

// IsRequest 报告是否为 requests.X 形式。
func (r ResourceName) IsRequest() bool { return strings.HasPrefix(string(r), requestsPrefix) }

// PodSpec 描述一个 Pod 的资源声明与存活截止时长。
// Requests / Limits 的键为基础资源名 X，值非负；键存在即"已声明"。
type PodSpec struct {
	Requests map[string]int64
	Limits   map[string]int64
	// ActiveDeadlineSeconds 为 nil 表示未声明；大于零表示有期限。
	ActiveDeadlineSeconds *int64
}

// Clone 深拷贝，避免调用方后续修改影响内部状态。
func (p PodSpec) Clone() PodSpec {
	out := PodSpec{
		Requests: make(map[string]int64, len(p.Requests)),
		Limits:   make(map[string]int64, len(p.Limits)),
	}
	for k, v := range p.Requests {
		out.Requests[k] = v
	}
	for k, v := range p.Limits {
		out.Limits[k] = v
	}
	if p.ActiveDeadlineSeconds != nil {
		d := *p.ActiveDeadlineSeconds
		out.ActiveDeadlineSeconds = &d
	}
	return out
}

// validate 校验 PodSpec 参数合法性（非空资源名、非负数量、非负截止时长）。
func (p PodSpec) validate() *Error {
	for name, v := range p.Requests {
		if name == "" {
			return invalidArg("pod 请求量资源名不能为空")
		}
		if v < 0 {
			return invalidArg(fmt.Sprintf("pod 请求量 %q 为负数: %d", name, v))
		}
	}
	for name, v := range p.Limits {
		if name == "" {
			return invalidArg("pod 上限量资源名不能为空")
		}
		if v < 0 {
			return invalidArg(fmt.Sprintf("pod 上限量 %q 为负数: %d", name, v))
		}
	}
	if p.ActiveDeadlineSeconds != nil && *p.ActiveDeadlineSeconds < 0 {
		return invalidArg(fmt.Sprintf("存活截止时长为负数: %d", *p.ActiveDeadlineSeconds))
	}
	return nil
}

// ResourceDefault 是对某一资源的默认值规则；nil 表示该字段无默认。
type ResourceDefault struct {
	Request *int64
	Limit   *int64
}

// Defaults 是命名空间级别的默认值规则，键为基础资源名 X。
type Defaults map[string]ResourceDefault

// cloneDefaults 深拷贝默认值规则。
func cloneDefaults(d Defaults) Defaults {
	out := make(Defaults, len(d))
	for k, rd := range d {
		cp := ResourceDefault{}
		if rd.Request != nil {
			v := *rd.Request
			cp.Request = &v
		}
		if rd.Limit != nil {
			v := *rd.Limit
			cp.Limit = &v
		}
		out[k] = cp
	}
	return out
}

// validate 校验默认值规则参数合法性。
func (d Defaults) validate() *Error {
	for name, rd := range d {
		if name == "" {
			return invalidArg("默认值规则的资源名不能为空")
		}
		if rd.Request != nil && *rd.Request < 0 {
			return invalidArg(fmt.Sprintf("默认请求量 %q 为负数: %d", name, *rd.Request))
		}
		if rd.Limit != nil && *rd.Limit < 0 {
			return invalidArg(fmt.Sprintf("默认上限量 %q 为负数: %d", name, *rd.Limit))
		}
	}
	return nil
}

// Scope 是配额作用域。
type Scope string

const (
	ScopeBestEffort     Scope = "BestEffort"
	ScopeNotBestEffort  Scope = "NotBestEffort"
	ScopeTerminating    Scope = "Terminating"
	ScopeNotTerminating Scope = "NotTerminating"
)

// known 报告是否为合法的作用域字面量。
func (s Scope) known() bool {
	switch s {
	case ScopeBestEffort, ScopeNotBestEffort, ScopeTerminating, ScopeNotTerminating:
		return true
	}
	return false
}

// QuotaSpec 描述一份配额：唯一名称由调用方给出，这里包含作用域集合与硬上限映射。
type QuotaSpec struct {
	Scopes []Scope
	Hard   map[ResourceName]int64
}

// Clone 深拷贝。
func (q QuotaSpec) Clone() QuotaSpec {
	out := QuotaSpec{Hard: make(map[ResourceName]int64, len(q.Hard))}
	out.Scopes = append(out.Scopes, q.Scopes...)
	for k, v := range q.Hard {
		out.Hard[k] = v
	}
	return out
}
