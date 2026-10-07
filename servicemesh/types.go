// Package servicemesh 实现服务网格的流量路由配置发布与请求分流。
package servicemesh

import "time"

// PathKind 为路径条件类型。
type PathKind int

const (
	PathExact  PathKind = iota // 精确匹配
	PathPrefix                 // 段边界前缀匹配
)

// PathMatch 为路径条件：精确或段边界前缀。
type PathMatch struct {
	Kind PathKind
	Path string
}

// HeaderOp 为请求头条件类型。
type HeaderOp int

const (
	HeaderExact   HeaderOp = iota // 值精确相等
	HeaderPrefix                  // 值以给定串开头
	HeaderPresent                 // 仅要求存在
)

// HeaderMatch 为单个请求头条件。头名不区分大小写，值区分大小写。
type HeaderMatch struct {
	Name  string
	Op    HeaderOp
	Value string
}

// MatchItem 为一个匹配项；路径条件与所有头条件之间为且。
type MatchItem struct {
	Path    PathMatch
	Headers []HeaderMatch
}

// Target 为分流目标：子集名称与整数权重。
type Target struct {
	Subset string
	Weight int
}

// Policy 为策略；指针字段为 nil 表示该字段缺省。
type Policy struct {
	Timeout           *time.Duration
	PerAttemptTimeout *time.Duration
	MaxRetries        *int
}

// Rule 为一条有序路由规则。
type Rule struct {
	Matches  []MatchItem
	Targets  []Target
	Override *Policy
}

// ServiceConfig 为单个目标服务的一份完整路由配置。
type ServiceConfig struct {
	Rules     []Rule
	Fallbacks []Target // 可选兜底目标
	Default   Policy
}

// Endpoint 为子集中的一个端点。
type Endpoint struct {
	Name  string
	Ready bool
}

// Request 为一次分流请求。
type Request struct {
	Path         string              // 可含查询串
	HeaderValues map[string][]string // 多值头；缺省时合并 Headers
	Headers      map[string]string   // 单值头便捷字段
	Bucket       int                 // 调用方给定，0..9999
}

// RouteResult 为分流结果。
type RouteResult struct {
	Service  string
	Subset   string
	RuleIdx  int // 命中规则下标；兜底为 -1
	Policy   Policy
	Endpoint string
}
