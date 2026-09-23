package stats

import "sync/atomic"

// DefaultSelectivity 是列统计缺失时回退的写死默认选择率。
const DefaultSelectivity = 0.1

// rowDataReads 计数器：任何读取行数据的路径都必须经由此计数器。
// 基数估计只使用聚合统计，规划全程该计数器保持为 0。
var rowDataReads atomic.Int64

// RowDataReads 返回进程内累计的行数据访问次数。
func RowDataReads() int64 { return rowDataReads.Load() }

// ReadRows 是唯一的行数据访问入口（规划器从不调用它）。
func ReadRows(n int) { rowDataReads.Add(int64(n)) }

// EqJoinSelectivity 返回等值连接谓词 a = b 的选择率：1/max(ndvA, ndvB)。
// 任一 NDV 为 0（未知）时整体回退到 DefaultSelectivity。
func EqJoinSelectivity(ndvA, ndvB uint64) float64 {
	if ndvA == 0 || ndvB == 0 {
		return DefaultSelectivity
	}
	m := ndvA
	if ndvB > m {
		m = ndvB
	}
	return 1.0 / float64(m)
}
