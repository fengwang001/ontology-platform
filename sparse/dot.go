package sparse

// Dot 计算两个稀疏向量的点积。
//
// 只做一次双指针归并，时间复杂度 O(len(a)+len(b))，与下标跨度
// 无关——下标到十亿也只推进个位数步，绝不展开稠密数组。
//
// 归并中同时累加三个 Kahan 和：点积 dot 以及两向量的平方和
// aa、bb（余弦复用，保证“同一次归并”内数值口径一致）。
//
// 不修改 a、b 的任何内容（不在此排序、不写回），可安全并发调用；
// 所有计数器均为调用内的局部变量，互不串台。
func Dot(a, b Vector) (DotResult, error) {
	r, _, _, err := merge(a, b)
	return r, err
}

// merge 执行唯一一次双指针归并。
// 返回点积统计、两个平方和（用于余弦）、以及错误。
func merge(a, b Vector) (DotResult, float64, float64, error) {
	zeros, err := validateBoth(a, b)
	if err != nil {
		return DotResult{}, 0, 0, err
	}

	var dot, aa, bb kahanSum
	var steps int
	i, j := 0, 0

	for i < len(a) && j < len(b) {
		steps++
		switch {
		case a[i].Index < b[j].Index:
			aa.add(a[i].Value * a[i].Value)
			i++
		case a[i].Index > b[j].Index:
			bb.add(b[j].Value * b[j].Value)
			j++
		default: // 下标相等：贡献点积
			av, bv := a[i].Value, b[j].Value
			p := av * bv
			dot.add(p)
			aa.add(av * av)
			bb.add(bv * bv)
			i++
			j++
		}
	}
	// 归并尾部：剩余元素只计入各自平方和，不再推进点积比较。
	for ; i < len(a); i++ {
		aa.add(a[i].Value * a[i].Value)
	}
	for ; j < len(b); j++ {
		bb.add(b[j].Value * b[j].Value)
	}

	d := dot.value()
	sqA, sqB := aa.value(), bb.value()
	if !finite(d) || !finite(sqA) || !finite(sqB) {
		// 例如两个有限但相乘溢出的大数：不把 Inf/NaN 交给调用方。
		return DotResult{Steps: steps, ExplicitZeros: zeros}, sqA, sqB,
			ErrNonFiniteResult
	}

	return DotResult{
		Dot:           d,
		Steps:         steps,
		ExplicitZeros: zeros,
	}, sqA, sqB, nil
}
