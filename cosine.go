package ontology

import "math"

// Cosine 计算两个稀疏向量的余弦相似度。
//
// 边界约定：
//   - 任一向量模为零（空向量或全零向量）时返回 *ZeroNormError；
//   - 两向量逐元素完全相同（下标与值的比特位均相同）时精确返回 1；
//   - 其余结果裁剪到 [-1, 1]；
//   - 出现 Inf/NaN（如输入含无穷）时返回 *NonFiniteError。
//
// 模的平方和同样用 Neumaier 补偿求和累计，统计的步数为
// 点积归并与两次模扫描的步数之和。
func Cosine(a, b Vector) (float64, Stats, error) {
	dot, st, err := Dot(a, b)
	if err != nil {
		return 0, st, err
	}

	normA2, stepsA := normSquared(a)
	normB2, stepsB := normSquared(b)
	st.Steps += stepsA + stepsB

	if math.IsInf(normA2, 0) || math.IsNaN(normA2) ||
		math.IsInf(normB2, 0) || math.IsNaN(normB2) {
		return 0, st, &NonFiniteError{Op: "cosine", Value: math.NaN()}
	}

	zeroA, zeroB := normA2 == 0, normB2 == 0
	switch {
	case zeroA && zeroB:
		return 0, st, &ZeroNormError{Which: 3}
	case zeroA:
		return 0, st, &ZeroNormError{Which: 1}
	case zeroB:
		return 0, st, &ZeroNormError{Which: 2}
	}

	// 完全相同的向量余弦精确为 1：直接特判，
	// 避免 sqrt 往返引入 1.0000000000000002 之类的误差。
	if identical(a, b) {
		return 1, st, nil
	}

	// 分开除以避免 normA2*normB2 中间溢出。
	cos := (dot / math.Sqrt(normA2)) / math.Sqrt(normB2)
	if math.IsInf(cos, 0) || math.IsNaN(cos) {
		return 0, st, &NonFiniteError{Op: "cosine", Value: cos}
	}
	return clamp(cos, -1, 1), st, nil
}

// normSquared 用 Neumaier 补偿求和计算向量模的平方，并返回扫描步数。
func normSquared(v Vector) (float64, int) {
	var sum, comp float64
	for _, e := range v {
		t := e.Value * e.Value
		next := sum + t
		if abs(sum) >= abs(t) {
			comp += (sum - next) + t
		} else {
			comp += (t - next) + sum
		}
		sum = next
	}
	return sum + comp, len(v)
}

// identical 判断两个向量是否逐元素完全相同（下标与值的比特位均相同）。
func identical(a, b Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Index != b[i].Index ||
			math.Float64bits(a[i].Value) != math.Float64bits(b[i].Value) {
			return false
		}
	}
	return true
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
