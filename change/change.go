// Package change 定义变更记录、操作类型与全序仲裁键。
//
// 仲裁规则：按三元组 (Ts, Origin, Seq) 字典序比较，严格大者胜；
// 删除（Del）以带三元组的墓碑形式参与比较。
package change

import "fmt"

// MaxNow 是写时间戳的上界（含）。
const MaxNow = int64(1_000_000_000_000)

// MaxKeyBytes 是键的最大字节长度。
const MaxKeyBytes = 32

// Op 是变更操作类型。
type Op int

const (
	// Put 写入一个 int64 值。
	Put Op = iota
	// Del 删除键（以墓碑形式保留三元组）。
	Del
)

// Valid 报告 op 是否为合法操作。
func (op Op) Valid() bool { return op == Put || op == Del }

func (op Op) String() string {
	switch op {
	case Put:
		return "Put"
	case Del:
		return "Del"
	default:
		return fmt.Sprintf("Op(%d)", int(op))
	}
}

// ValidKey 报告键是否合法：非空且不超过 32 字节。
func ValidKey(key string) bool {
	return len(key) >= 1 && len(key) <= MaxKeyBytes
}

// ValidNow 报告时间戳是否在 [0, 1e12] 内。
func ValidNow(now int64) bool {
	return now >= 0 && now <= MaxNow
}

// Triple 是全序仲裁键 (Ts, Origin, Seq)，按字典序比较。
type Triple struct {
	Ts     int64
	Origin int
	Seq    int64
}

// Less 报告 a 是否严格小于 b。
func (a Triple) Less(b Triple) bool {
	if a.Ts != b.Ts {
		return a.Ts < b.Ts
	}
	if a.Origin != b.Origin {
		return a.Origin < b.Origin
	}
	return a.Seq < b.Seq
}

func (a Triple) String() string {
	return fmt.Sprintf("(ts=%d,origin=%d,seq=%d)", a.Ts, a.Origin, a.Seq)
}

// Change 是一条变更记录。
type Change struct {
	Origin int    // 来源站点
	Seq    int64  // 来源站点上的写序号（从 1 开始）
	Ts     int64  // 写方给定的时间戳
	Key    string // 键（1..32 字节）
	Op     Op     // Put 或 Del
	Val    int64  // Put 的值；Del 时无意义
}

// Triple 返回该变更的仲裁三元组。
func (c Change) Triple() Triple {
	return Triple{Ts: c.Ts, Origin: c.Origin, Seq: c.Seq}
}

func (c Change) String() string {
	if c.Op == Del {
		return fmt.Sprintf("{origin=%d seq=%d ts=%d key=%q Del}", c.Origin, c.Seq, c.Ts, c.Key)
	}
	return fmt.Sprintf("{origin=%d seq=%d ts=%d key=%q Put(%d)}", c.Origin, c.Seq, c.Ts, c.Key, c.Val)
}
