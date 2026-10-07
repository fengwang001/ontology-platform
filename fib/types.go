// Package fib 实现容量受限的转发表管理器。
//
// 控制面维护全量路由(前缀到下一跳的映射),数据面维护一份聚合后的
// 转发表。聚合后的数据面在全部 2^32 个地址上与控制面的最长前缀匹配
// 结果逐地址一致(包括"无路由"与"黑洞"的区分),且条目数在满足该
// 约束的所有数据面中最少。任何更新生效后若最少条目数超过容量上限,
// 则整体拒绝,控制面、数据面与条目数均不发生变化。
package fib

import "errors"

// 错误按固定优先级判定:参数非法 > 撤销目标不存在 > 容量不足。
var (
	// ErrInvalidArgument 参数非法(前缀低位非零、长度越界、下一跳为空等)。
	ErrInvalidArgument = errors.New("fib: 参数非法")
	// ErrRouteNotFound 撤销的前缀不存在(批量内按生效顺序取第一个)。
	ErrRouteNotFound = errors.New("fib: 撤销目标不存在")
	// ErrCapacityExceeded 更新生效后的最少条目数超过容量上限,或
	// 新容量上限小于当前最少条目数。
	ErrCapacityExceeded = errors.New("fib: 容量不足")
)

// Nexthop 是路由的下一跳:非空字符串,或特殊的黑洞值。
// 黑洞是有效下一跳:匹配到的地址被丢弃,但它不同于无路由。
type Nexthop struct {
	// Blackhole 为 true 表示黑洞下一跳,此时 Value 必须为空。
	Blackhole bool
	// Value 普通下一跳字符串,非空;Blackhole 为 true 时忽略且必须为空。
	Value string
}

// NH 构造普通字符串下一跳,s 必须非空(否则在使用时按参数非法处理)。
func NH(s string) Nexthop { return Nexthop{Value: s} }

// Blackhole 是黑洞下一跳:有效,但匹配到的地址被丢弃。
var Blackhole = Nexthop{Blackhole: true}

// valid 校验下一跳参数是否合法。
func (n Nexthop) valid() bool {
	if n.Blackhole {
		return n.Value == ""
	}
	return n.Value != ""
}

// Prefix 表示一个地址前缀:Addr 为 32 位地址,Len 为前缀长度(0..32),
// 长度之外的低位必须全为零。
type Prefix struct {
	Addr uint32
	Len  int
}

// Valid 校验前缀参数是否合法。
func (p Prefix) Valid() bool {
	if p.Len < 0 || p.Len > 32 {
		return false
	}
	// Len == 0 时掩码为全 1,等价于要求 Addr == 0。
	return p.Addr&((uint32(1)<<(32-p.Len))-1) == 0
}

// Contains 判断 addr 是否被该前缀覆盖。调用方需保证前缀合法。
func (p Prefix) Contains(addr uint32) bool {
	if p.Len == 0 {
		return true
	}
	return addr>>(32-p.Len) == p.Addr>>(32-p.Len)
}

// ResultKind 区分一次地址查询的三种结果。
type ResultKind int

const (
	// NoRoute 无路由:没有任何前缀覆盖该地址,或命中了无路由条目。
	NoRoute ResultKind = iota
	// BlackholeRoute 命中黑洞:地址被丢弃,但不同于无路由。
	BlackholeRoute
	// NexthopRoute 命中普通下一跳。
	NexthopRoute
)

// Result 是一次地址查询在某个面上的结果。
type Result struct {
	Kind    ResultKind
	Nexthop string // 仅当 Kind == NexthopRoute 时有效
}

// color 是内部使用的"地址着色":无路由、黑洞或某个具体下一跳。
// 控制面把全部 2^32 个地址各染上一种颜色,数据面要用最少的前缀条目
// 复现同一着色。
type color struct {
	kind ResultKind
	nh   string
}

var noRouteColor = color{kind: NoRoute}

func nexthopColor(n Nexthop) color {
	if n.Blackhole {
		return color{kind: BlackholeRoute}
	}
	return color{kind: NexthopRoute, nh: n.Value}
}

func (c color) result() Result {
	return Result{Kind: c.kind, Nexthop: c.nh}
}

// colorLess 为候选颜色提供确定性的全序,保证数据面内容可复现。
func colorLess(a, b color) bool {
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	return a.nh < b.nh
}

// Entry 是一条数据面条目。Result 可以是无路由(无路由条目,占用条目数)。
type Entry struct {
	Prefix Prefix
	Result Result
}

// Op 是批量更新中的一步操作。
type Op struct {
	// Delete 为 true 表示撤销该前缀,否则为写入(已存在则覆盖)。
	Delete bool
	Prefix Prefix
	// Nexthop 写入时的下一跳;Delete 为 true 时忽略。
	Nexthop Nexthop
}

// PutOp 构造一步写入操作。
func PutOp(p Prefix, nh Nexthop) Op { return Op{Prefix: p, Nexthop: nh} }

// DeleteOp 构造一步撤销操作。
func DeleteOp(p Prefix) Op { return Op{Delete: true, Prefix: p} }

// Stats 是更新路径上的工作量计数,用于以可验证的方式证明
// 单次更新开销与无关路由总数无关。
type Stats struct {
	// Recomputes 触发 DP 状态重算的 trie 节点数。
	Recomputes int64
	// GammaUpdates 因祖先路由变化而更新继承色的节点数。
	GammaUpdates int64
}
