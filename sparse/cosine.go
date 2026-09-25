package sparse

import "math"

// Cosine 计算两个稀疏向量的余弦相似度。
//
// 边界与保证：
//   - 任一向量模为 0（空向量或全零，含显式零）时余弦无定义，
//     返回 ErrZeroNorm，绝不返回 NaN 或 0；
//   - 两向量完全相同（长度相同、下标与值逐位相等）时精确返回 1，
//     而不是 1.0000000000000002：同一次归并里点积与平方和由
//     相同的项、同一个确定性 Kahan 求和器产生，Float64bits 相等，
//     此时直接返回字面上的 1.0，绕开 sqrt/除法的舍入；
//   - 其他情形计算 dot / sqrt(aa*bb)，再裁剪到 [-1, 1]
//     （仅当浮点结果越界时钳制，正常结果原样返回）；
//   - 含 Inf 元素或点积溢出为 Inf/NaN 时返回可判定错误。
func Cosine(a, b Vector) (float64, *DotResult, error) {
	r, aa, bb, err := merge(a, b)
	if err != nil {
		return 0, &r, err
	}
	if aa == 0 || bb == 0 {
		return 0, &r, ErrZeroNorm
	}

	if identical(a, b) {
		return 1.0, &r, nil
	}

	denom := math.Sqrt(aa * bb)
	c := r.Dot / denom
	if !finite(c) {
		return 0, &r, ErrNonFiniteResult
	}
	if c > 1 {
		c = 1
	} else if c < -1 {
		c = -1
	}
	return c, &r, nil
}

// identical 判断两向量是否完全相同（长度、下标、值逐位相等，
// 含符号位一致的 0）。不修改入参。
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
