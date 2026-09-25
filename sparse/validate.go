package sparse

import "math"

// validate 检查单个向量：
//   - 下标必须严格升序；相等或递减都返回 ErrNotSorted；
//   - 值为 NaN 返回 ErrNaN；值为正负无穷返回 ErrInf；
//   - 显式零元素合法，仅统计其个数。
//
// vecID 为 VectorA(1) 或 VectorB(2)，Pos 从 0 起。
// 检查顺序保证：先比较相邻下标（位置 i>=1 时比较 i-1 与 i），
// 再检查当前位置的值，因此每个错误都精确定位到出错元素。
func validate(v Vector, vecID int) (explicitZeros int, err error) {
	for i, e := range v {
		if i > 0 && e.Index <= v[i-1].Index {
			return explicitZeros, &ValidationError{
				Err:    ErrNotSorted,
				Vector: vecID,
				Pos:    i,
				Index:  e.Index,
			}
		}
		switch {
		case math.IsNaN(e.Value):
			return explicitZeros, &ValidationError{
				Err:    ErrNaN,
				Vector: vecID,
				Pos:    i,
				Index:  e.Index,
			}
		case math.IsInf(e.Value, 0):
			return explicitZeros, &ValidationError{
				Err:    ErrInf,
				Vector: vecID,
				Pos:    i,
				Index:  e.Index,
			}
		case e.Value == 0:
			explicitZeros++
		}
	}
	return explicitZeros, nil
}

// validateBoth 依次校验两个向量，返回两者显式零元素总数。
func validateBoth(a, b Vector) (int, error) {
	za, err := validate(a, VectorA)
	if err != nil {
		return 0, err
	}
	zb, err := validate(b, VectorB)
	if err != nil {
		return 0, err
	}
	return za + zb, nil
}
