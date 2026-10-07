// Package boundary 负责比较两条时间轴区间在源格式与目标格式之间的边界
// （闭合/开放）语义差异，并判定这种差异是否会改变某个查询时点的归属结果。
package boundary

import "ontology/bitemporal"

// contains 是闭/开区间语义的标准归属判定：查询时点 t 是否属于
// 以 iv 的有限端点值、conv 的闭合约定所表达的区间。
//
// 无界方向不参与比较（任何有限查询点都严格满足对应不等式），且无界端点的
// 闭合位按约定为 Open、没有可观察语义。
func contains(iv bitemporal.Interval, conv bitemporal.BoundaryConvention, t int64) bool {
	if iv.Start != nil {
		if conv.StartClosed == bitemporal.Closed {
			if t < *iv.Start {
				return false
			}
		} else if t <= *iv.Start {
			return false
		}
	}
	if iv.End != nil {
		if conv.EndClosed == bitemporal.Closed {
			if t > *iv.End {
				return false
			}
		} else if t >= *iv.End {
			return false
		}
	}
	return true
}

// AxisDifference 描述单条时间轴上边界语义差异的判定结论。
type AxisDifference struct {
	Axis              bitemporal.Axis
	ChangesMembership bool
	WitnessPoint      int64
	HasWitness        bool
}

// Comparator 比较区间边界语义。零值即可直接使用，无状态、可并发调用。
type Comparator struct{}

// NewComparator 构造一个边界语义比较器。
func NewComparator() *Comparator {
	return &Comparator{}
}

// Compare 对给定区间（来自源记录）判断：若其在 src 与 dst 两种端点闭合约定下
// 表达，两种表达在同一查询时点上的归属判定是否存在差异。
//
// 只需要检查至多两个见证点——区间的有限起点与有限终点：闭/开两种约定之间的
// 归属差异只可能发生在端点时刻本身；区间内部与外部两种约定的结论恒相同。
// 因此结论是精确而非启发式的：ChangesMembership 为真当且仅当存在至少一个
// 查询时点使同一记录的归属判定在两种格式下相反，此时同时返回该见证点。
func (c Comparator) Compare(iv bitemporal.Interval, src, dst bitemporal.BoundaryConvention) AxisDifference {
	d := AxisDifference{Axis: ""}
	witness := []int64{}
	if iv.Start != nil {
		witness = append(witness, *iv.Start)
	}
	if iv.End != nil {
		witness = append(witness, *iv.End)
	}
	for _, t := range witness {
		if contains(iv, src, t) != contains(iv, dst, t) {
			d.ChangesMembership = true
			d.HasWitness = true
			d.WitnessPoint = t
			return d
		}
	}
	return d
}

// CompareAxis 是 Compare 的带轴标识包装，供编排层按轴收集差异。
func (c Comparator) CompareAxis(axis bitemporal.Axis, iv bitemporal.Interval, src, dst bitemporal.BoundaryConvention) AxisDifference {
	d := c.Compare(iv, src, dst)
	d.Axis = axis
	return d
}
