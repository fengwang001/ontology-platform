package ontology

import "math"

// Dot 计算两个稀疏向量的点积，返回结果与本次计算的统计信息。
//
// 实现为一次双指针归并：两个输入都必须已按下标严格升序排列，
// 函数只读输入，绝不展开为稠密数组，也不修改传入的向量。
//
// 求和采用 Neumaier 补偿求和：归并顺序由升序下标唯一确定，
// 因此同一对向量无论元素原始写入顺序如何（重新排序后），
// 求和顺序一致，结果逐位相同；补偿项保证在项量级悬殊时
// 仍保持高精度（相对误差量级 1e-16）。
//
// 若输入含正负无穷或中间结果溢出导致结果为 Inf/NaN，
// 返回 *NonFiniteError 而不是把 NaN 交给调用方。
func Dot(a, b Vector) (float64, Stats, error) {
	var st Stats
	zerosA, err := validate(1, a)
	if err != nil {
		return 0, st, err
	}
	zerosB, err := validate(2, b)
	if err != nil {
		return 0, st, err
	}
	st.ZerosA, st.ZerosB = zerosA, zerosB

	sum, comp := neumaierDot(a, b, &st.Steps)
	result := sum + comp
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return 0, st, &NonFiniteError{Op: "dot", Value: result}
	}
	return result, st, nil
}

// neumaierDot 执行双指针归并，对匹配下标的乘积做 Neumaier 补偿求和。
// 每推进一次循环（任一指针移动）计一步，写入 *steps。
func neumaierDot(a, b Vector, steps *int) (sum, comp float64) {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		*steps++
		ai, bj := a[i].Index, b[j].Index
		switch {
		case ai < bj:
			i++
		case ai > bj:
			j++
		default:
			t := a[i].Value * b[j].Value
			// Neumaier 补偿求和：把本次加法丢失的低位记入 comp。
			next := sum + t
			if abs(sum) >= abs(t) {
				comp += (sum - next) + t
			} else {
				comp += (t - next) + sum
			}
			sum = next
			i++
			j++
		}
	}
	return sum, comp
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
