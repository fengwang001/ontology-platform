package alloc

// Split 把金额等分成 n 份。
// 等价于使用 n 个相同权重调用 Allocate：多余的单位分给索引最小的份。
func Split(amount int64, n int) ([]int64, error) {
	if n <= 0 {
		return nil, ErrInvalidParts
	}
	weights := make([]int64, n)
	for i := range weights {
		weights[i] = 1
	}
	return Allocate(amount, weights)
}
