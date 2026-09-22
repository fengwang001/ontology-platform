package ontology

import "math"

// validate 检查向量下标严格升序且值不为 NaN，并统计显式零元素个数。
// 不修改输入向量。
func validate(which int, v Vector) (zeros int, err error) {
	for i, e := range v {
		if math.IsNaN(e.Value) {
			return 0, &NaNError{Which: which, Position: i, Index: e.Index}
		}
		if i > 0 && e.Index <= v[i-1].Index {
			return 0, &OrderError{Which: which, Position: i, Prev: v[i-1].Index, Cur: e.Index}
		}
		if e.Value == 0 {
			zeros++
		}
	}
	return zeros, nil
}
