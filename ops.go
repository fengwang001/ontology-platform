package ontology

// Semantics 选择集合语义或多重集语义。
type Semantics int

const (
	// Set 集合语义：同一个值在结果里只出现一次。
	Set Semantics = iota
	// Multiset 多重集语义：结果中每个值的次数由各流次数决定。
	Multiset
)

// Union 计算各流的并集。
// 集合语义：在任意流中出现的值各取一次。
// 多重集语义：每个值取各流中出现次数的最大值。
// 零条流时返回空结果，无错误。
func Union(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	var result []float64
	stats, err := mergeGroups(streams, func(value float64, counts []int) {
		n := 1
		if sem == Multiset {
			n = 0
			for _, c := range counts {
				if c > n {
					n = c
				}
			}
		}
		result = appendN(result, value, n)
	})
	if err != nil {
		return nil, stats, err
	}
	return result, stats, nil
}

// Intersect 计算各流的交集。
// 集合语义：在所有流中都出现的值各取一次。
// 多重集语义：每个值取各流中出现次数的最小值。
// 零条流在数学上有歧义，返回 ErrNoStreams。
func Intersect(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	if len(streams) == 0 {
		return nil, Stats{}, ErrNoStreams
	}
	var result []float64
	stats, err := mergeGroups(streams, func(value float64, counts []int) {
		n := 1
		if sem == Multiset {
			n = counts[0]
		}
		for _, c := range counts {
			if c == 0 {
				return
			}
			if sem == Multiset && c < n {
				n = c
			}
		}
		result = appendN(result, value, n)
	})
	if err != nil {
		return nil, stats, err
	}
	return result, stats, nil
}

// Difference 计算差集：第一条流减去其余各流。
// 集合语义：在第一条流中出现、且不在任何其余流中出现的值各取一次。
// 多重集语义：每个值取第一条流的次数减去其余各流次数之和，不小于零。
// 零条流时没有"第一条流"，返回 ErrNoStreams。
func Difference(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	if len(streams) == 0 {
		return nil, Stats{}, ErrNoStreams
	}
	var result []float64
	stats, err := mergeGroups(streams, func(value float64, counts []int) {
		if counts[0] == 0 {
			return
		}
		n := 1
		if sem == Multiset {
			n = counts[0]
		}
		for _, c := range counts[1:] {
			if sem == Set {
				if c > 0 {
					return
				}
			} else {
				n -= c
			}
		}
		if n > 0 {
			result = appendN(result, value, n)
		}
	})
	if err != nil {
		return nil, stats, err
	}
	return result, stats, nil
}

func appendN(dst []float64, value float64, n int) []float64 {
	for ; n > 0; n-- {
		dst = append(dst, value)
	}
	return dst
}
