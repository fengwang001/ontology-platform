// Package bitemporal 定义本体实例快照导出中双时态信息（有效时间轴与事务时间轴）
// 兼容性判定所使用的公共类型。
//
// 本包只包含无行为的值类型；判定逻辑分别位于：
//   - ontology/bitemporal/interval：记录自身时态区间的自洽性校验；
//   - ontology/bitemporal/boundary：两条时间轴边界（开闭）语义比较；
//   - ontology/bitemporal/registry：快照格式版本目录（记录哪些轴、采用何种边界约定）；
//   - ontology/bitemporal/judge：版本间信息留存规则判定、判定编排与迁移。
package bitemporal

// Axis 标识一条时间轴。
type Axis string

const (
	// ValidTime 有效时间轴（valid time）：业务事实在现实世界中成立的时间。
	ValidTime Axis = "valid"
	// TransactionTime 事务时间轴（transaction time）：事实被系统记录在案的时间。
	TransactionTime Axis = "transaction"
)

// Axes 返回两条时间轴的固定遍历顺序。判定报告（损失/不兼容/填充）均按此顺序给出，
// 保证多次调用与并发调用的输出稳定一致。
func Axes() []Axis {
	return []Axis{ValidTime, TransactionTime}
}

// EndKind 描述区间某一端点的闭合方式。
type EndKind bool

const (
	// Open 开放端点：端点时刻本身不属于区间，记为 ( 或 )。
	Open EndKind = false
	// Closed 闭合端点：端点时刻本身属于区间，记为 [ 或 ]。
	Closed EndKind = true
)

// Interval 是一条时间轴上的取值区间，端点时刻用同一时钟域的整数时间戳表示。
//
// 端点为 nil 表示该方向无界：Start == nil 表示负无穷，End == nil 表示正无穷。
// 无界端点的闭合方式没有可观察语义（没有任何查询时点等于无穷），约定为 Open。
type Interval struct {
	Start       *int64
	End         *int64
	StartClosed EndKind
	EndClosed   EndKind
}

// Record 是一条本体实例属性取值所携带的双时态信息。
//
// Valid / Transaction 为 nil 表示该记录本身不携带该轴信息（例如该记录抽取自
// 只记录事务时间轴的旧版快照）。Record 为纯数据值，判定过程绝不修改它。
type Record struct {
	ID          string
	Valid       *Interval
	Transaction *Interval
}

// BoundaryConvention 是某个格式版本对一条时间轴区间端点的固定记录约定。
type BoundaryConvention struct {
	StartClosed EndKind
	EndClosed   EndKind
}
