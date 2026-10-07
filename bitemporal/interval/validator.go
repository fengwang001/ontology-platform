// Package interval 负责记录自身时态区间的自洽性校验。
//
// 它只回答“记录携带的每一条时间轴区间本身是否成立”，与格式版本无关：
// 起点晚于终点的区间在任何版本下都不应参与后续兼容性判定。
package interval

import (
	"fmt"

	"ontology/bitemporal"
)

// AxisError 描述单条时间轴上的自洽性问题。
type AxisError struct {
	Axis    bitemporal.Axis
	Message string
}

// Validator 校验记录自身时态区间的自洽性。零值即可直接使用，且不持有任何
// 可变状态，可被任意数量的 goroutine 并发调用。
type Validator struct{}

// NewValidator 构造一个自洽性校验器。
func NewValidator() *Validator {
	return &Validator{}
}

// Validate 返回记录中每条已携带时间轴区间的自洽性问题；空切片表示全部自洽。
// 该方法不读取也不修改任何外部状态，也不会修改入参记录。
//
// 当前版本只拒绝题面明确规定的一种不自洽情形：有限起点晚于有限终点，例如 [10,5)。
// 边界点相同的两个开放端点（例如 (3,3)）表示空集，是数学上合法的区间，不在此列；
// 无界端点天然不存在“晚于”关系。为保证错误清单在多次调用间稳定，结果按
// bitemporal.Axes() 的固定轴顺序返回。
func (Validator) Validate(rec bitemporal.Record) []AxisError {
	errs := make([]AxisError, 0, 2)
	for _, axis := range bitemporal.Axes() {
		iv := axisInterval(rec, axis)
		if iv == nil {
			continue
		}
		if iv.Start != nil && iv.End != nil && *iv.Start > *iv.End {
			errs = append(errs, AxisError{
				Axis: axis,
				Message: fmt.Sprintf(
					"%s 时间区间起点 %d 晚于终点 %d，区间在任何格式版本下都不自洽",
					axisName(axis), *iv.Start, *iv.End),
			})
		}
	}
	return errs
}

func axisInterval(rec bitemporal.Record, axis bitemporal.Axis) *bitemporal.Interval {
	switch axis {
	case bitemporal.ValidTime:
		return rec.Valid
	case bitemporal.TransactionTime:
		return rec.Transaction
	default:
		return nil
	}
}

func axisName(axis bitemporal.Axis) string {
	switch axis {
	case bitemporal.ValidTime:
		return "有效"
	case bitemporal.TransactionTime:
		return "事务"
	default:
		return string(axis)
	}
}
