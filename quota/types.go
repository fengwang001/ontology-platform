// Package quota 实现多租户命名空间的资源配额准入控制器。
//
// 包内职责划分：
//   - types.go      对外数据模型（Pod、Defaults、Quota、Scope）
//   - resource.go   资源名（pods / requests.X / limits.X）的解析与校验
//   - errors.go     六类错误的可区分表示与优先级
//   - defaults.go   准入前的默认值补全
//   - scope.go      Pod 服务等级/期限分类与配额作用域匹配
//   - controller.go 控制器本体：准入、原位调整、配额管理、自检、并发
package quota

// Pod 是准入的对象。数量均为非负整数。
type Pod struct {
	Name string
	Spec PodSpec
}

// PodSpec 声明各资源的请求量与上限量；未在 map 中出现的资源视为缺省。
type PodSpec struct {
	// Requests / Limits 以裸资源名（如 "cpu"）为键。
	Requests map[string]int64
	Limits   map[string]int64
	// DeadlineSeconds 为存活截止时长；nil 或 <= 0 表示无期限。
	DeadlineSeconds *int64
}

// DefaultRule 给出某资源的默认请求量与默认上限量，二者均可缺省。
type DefaultRule struct {
	Request *int64
	Limit   *int64
}

// Defaults 是命名空间级别的一份默认值规则，以裸资源名为键。
type Defaults struct {
	Rules map[string]DefaultRule
}

// Scope 是配额作用域。
type Scope string

const (
	ScopeBestEffort     Scope = "BestEffort"
	ScopeNotBestEffort  Scope = "NotBestEffort"
	ScopeTerminating    Scope = "Terminating"
	ScopeNotTerminating Scope = "NotTerminating"
)

// Quota 是一条配额：唯一名称、作用域集合与硬上限映射。
type Quota struct {
	Name   string
	Scopes []Scope
	// Hard 的键只能是 pods、requests.X、limits.X 三种形式。
	Hard map[ResourceName]int64
}
