// Package version 定义快照版本号：单调递增、可比较，零值表示尚无快照。
package version

// V 是快照版本号。零值 V{} 表示「尚无快照」，任何已发布版本都大于它。
type V struct {
	n uint64
}

// Zero 返回零值版本号，表示尚无快照。
func Zero() V { return V{} }

// Next 返回紧接 v 的下一个版本号。
func Next(v V) V { return V{n: v.n + 1} }

// Less 报告 v 是否小于 other。
func (v V) Less(other V) bool { return v.n < other.n }

// Equal 报告两个版本号是否相同。
func (v V) Equal(other V) bool { return v.n == other.n }

// IsZero 报告版本号是否为零值。
func (v V) IsZero() bool { return v.n == 0 }

// Num 返回版本号的整数表示，仅用于打印与统计。
func (v V) Num() uint64 { return v.n }
