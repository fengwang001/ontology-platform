// Package policy 定义每个头部名的语义策略：是否列表型、重复时单值如何
// 取值、值比较是否大小写敏感。依赖 token 包。
package policy

import "ontology/token"

// Kind 是重复头部的单值解析策略。
type Kind int

const (
	First Kind = iota // 取第一个出现的值
	Last              // 取最后一个出现的值
	Merge             // 列表型：合并各出现（列表项语义拼接）
	Reject            // 出现多次即错误
)

// Policy 描述一个头部名的策略。
type Policy struct {
	List           bool // 列表型：值是逗号分隔元素集合
	Duplicate      Kind // 单值访问面对重复时的策略
	ValueSensitive bool // 值比较/合并去重时是否大小写敏感
}

// Registry 是按规范化名索引的策略表。零值即可用，未知名字回退 Default。
type Registry struct {
	def  Policy
	over map[string]Policy
}

// NewRegistry 以 def 作为未知名字的默认策略构造表。
func NewRegistry(def Policy) *Registry {
	return &Registry{def: def, over: map[string]Policy{}}
}

// Set 覆盖某名字（大小写不敏感）的策略。
func (r *Registry) Set(name string, p Policy) {
	r.over[token.LowerName(name)] = p
}

// Get 返回名字对应的策略；未注册返回默认策略。
func (r *Registry) Get(name string) Policy {
	if p, ok := r.over[token.LowerName(name)]; ok {
		return p
	}
	return r.def
}

// DefaultRegistry 覆盖常见标准头部；默认策略为"单值、取首个、值不敏感"。
func DefaultRegistry() *Registry {
	r := NewRegistry(Policy{Duplicate: First})
	r.Set("Set-Cookie", Policy{Duplicate: First}) // 严格重复保留，单值取首
	r.Set("Warning", Policy{List: true, Duplicate: Merge})
	r.Set("Accept", Policy{List: true, Duplicate: Merge})
	r.Set("Accept-Charset", Policy{List: true, Duplicate: Merge})
	r.Set("Accept-Encoding", Policy{List: true, Duplicate: Merge})
	r.Set("Accept-Language", Policy{List: true, Duplicate: Merge})
	r.Set("Allow", Policy{List: true, Duplicate: Merge})
	r.Set("Cache-Control", Policy{List: true, Duplicate: Merge})
	r.Set("Connection", Policy{List: true, Duplicate: Merge})
	r.Set("Content-Encoding", Policy{List: true, Duplicate: Merge})
	r.Set("Content-Language", Policy{List: true, Duplicate: Merge})
	r.Set("Via", Policy{List: true, Duplicate: Merge})
	r.Set("Forwarded", Policy{List: true, Duplicate: Merge})
	r.Set("If-Match", Policy{List: true, Duplicate: Merge})
	r.Set("If-None-Match", Policy{List: true, Duplicate: Merge})
	r.Set("Trailer", Policy{List: true, Duplicate: Merge})
	r.Set("Upgrade", Policy{List: true, Duplicate: Reject})
	r.Set("Content-Length", Policy{Duplicate: Reject, ValueSensitive: true})
	r.Set("Location", Policy{Duplicate: Last})
	r.Set("Host", Policy{Duplicate: Reject})
	return r
}
