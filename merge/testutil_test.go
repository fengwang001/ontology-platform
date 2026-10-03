package merge

import "ontology/schema"

func mkHist(name string, b []int64, c []uint64, sum int64) schema.Hist {
	return schema.Hist{Name: name, Bounds: b, Counts: c, Sum: sum}
}

// naiveProject 按题目规则朴素投影：每桶并入"不小于其闭上界的最小公共边界"桶。
func naiveProject(bounds []int64, counts []uint64, common []int64) []uint64 {
	out := make([]uint64, len(common)+1)
	for i, cnt := range counts {
		target := len(common)
		if i < len(bounds) {
			for k, cb := range common {
				if cb >= bounds[i] {
					target = k
					break
				}
			}
		}
		out[target] += cnt
	}
	return out
}

func equalHist(a, b schema.Hist) bool {
	if a.Name != b.Name || a.Sum != b.Sum || len(a.Bounds) != len(b.Bounds) ||
		len(a.Counts) != len(b.Counts) {
		return false
	}
	for i := range a.Bounds {
		if a.Bounds[i] != b.Bounds[i] {
			return false
		}
	}
	for i := range a.Counts {
		if a.Counts[i] != b.Counts[i] {
			return false
		}
	}
	return true
}
