package coalesce

import "ontology/rangespec"

// Normalizer 执行归一化并记录排序阶段发生的区间比较次数。
// 归一化采用"自底向上归并排序 + 单次线性扫描合并"，没有两两比较，
// 比较次数上界为 n*ceil(log2(n))。
type Normalizer struct {
	compareCount int64
}

// NewNormalizer 创建一个比较计数器归零的 Normalizer。
func NewNormalizer() *Normalizer { return &Normalizer{} }

// CompareCount 返回自创建以来累计的区间比较次数（非导出状态，测试可读）。
func (n *Normalizer) CompareCount() int64 { return n.compareCount }

// Normalize 见包级 Normalize 的文档。
func (n *Normalizer) Normalize(specs []rangespec.Spec, size int64) ([]Interval, error) {
	if size < 0 {
		return nil, &UnsatisfiableError{TotalLength: size, Reason: "negative resource length"}
	}
	if len(specs) == 0 {
		return nil, &UnsatisfiableError{TotalLength: size, Reason: "empty range set"}
	}

	intervals := make([]Interval, 0, len(specs))
	for _, s := range specs {
		switch s.Kind {
		case rangespec.Suffix:
			// "-n" 表示末尾 n 字节；n==0 时末尾零字节，不可满足。
			if s.SuffixN == 0 {
				return nil, &UnsatisfiableError{TotalLength: size, Reason: "suffix of zero bytes (bytes=-0)"}
			}
			if s.SuffixN >= size {
				if size == 0 {
					return nil, &UnsatisfiableError{TotalLength: 0, Reason: "empty resource"}
				}
				intervals = append(intervals, Interval{Start: 0, End: size - 1})
			} else {
				intervals = append(intervals, Interval{Start: size - s.SuffixN, End: size - 1})
			}
		case rangespec.FromEnd:
			if s.Start >= size {
				return nil, &UnsatisfiableError{TotalLength: size, Reason: "range start is at or past end"}
			}
			intervals = append(intervals, Interval{Start: s.Start, End: size - 1})
		default: // FromTo
			if s.Start >= size {
				return nil, &UnsatisfiableError{TotalLength: size, Reason: "range start is at or past end"}
			}
			stop := s.End
			if stop >= size {
				stop = size - 1 // 终点越界裁剪到末尾
			}
			intervals = append(intervals, Interval{Start: s.Start, End: stop})
		}
	}

	n.mergeSort(intervals)
	merged := mergeAdjacent(intervals)
	return merged, nil
}

// mergeAdjacent 对已按起点排序的区间做一次线性扫描；
// 当 prev.End+1 >= cur.Start 时二者重叠或首尾相邻，予以合并。
func mergeAdjacent(in []Interval) []Interval {
	out := make([]Interval, 0, len(in))
	for _, iv := range in {
		if len(out) == 0 {
			out = append(out, iv)
			continue
		}
		last := &out[len(out)-1]
		if iv.Start <= last.End+1 {
			if iv.End > last.End {
				last.End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
