package ontology

import "math"

// Stats 记录一次归并的统计信息。
type Stats struct {
	// Comparisons 是堆上浮/下沉过程中元素比较的总次数。
	// 上界：N 为元素总数、k 为流条数（k >= 2）时，
	// 每个元素恰好入堆、出堆各一次，入堆至多 ceil(log2(k)) 次比较，
	// 出堆至多 2*ceil(log2(k)) 次比较，因此
	//     Comparisons <= 3 * N * ceil(log2(k))
	// 即与总长度 N 成线性关系；k <= 1 时 Comparisons 为 0。
	Comparisons int64
	// MaxHeapSize 是归并过程中堆内元素数的峰值，始终 <= 流条数。
	MaxHeapSize int
}

// mergeGroups 对 k 条已升序排好的流做一次 k 路归并，
// 按值从小到大把每个不同的值连同它在各流中的出现次数
// 交给 yield。counts 与 streams 一一对应，是复用的缓冲区，
// yield 不得保留其引用。
//
// 任意时刻堆内元素数不超过流的条数，额外内存与流长无关。
// 输入流不被修改。遇到 NaN 返回 NaNError，遇到严格递减
// 返回 OrderError。
func mergeGroups(streams [][]float64, yield func(value float64, counts []int)) (Stats, error) {
	var stats Stats
	heap := newMinHeap(len(streams), &stats.Comparisons)

	push := func(stream, index int) error {
		value := streams[stream][index]
		if math.IsNaN(value) {
			return NaNError{Stream: stream, Index: index}
		}
		heap.push(heapItem{value: value, stream: stream, index: index})
		if heap.len() > stats.MaxHeapSize {
			stats.MaxHeapSize = heap.len()
		}
		return nil
	}

	for stream := range streams {
		if len(streams[stream]) > 0 {
			if err := push(stream, 0); err != nil {
				return stats, err
			}
		}
	}

	counts := make([]int, len(streams))
	for heap.len() > 0 {
		value := heap.items[0].value
		for i := range counts {
			counts[i] = 0
		}
		// 弹出所有与组值相等的元素。+0.0 == -0.0 为真，
		// 故正负零归入同组；NaN 已在入堆前被拒绝。
		for heap.len() > 0 && heap.items[0].value == value {
			item := heap.pop()
			counts[item.stream]++
			next := item.index + 1
			if next < len(streams[item.stream]) {
				follow := streams[item.stream][next]
				if !math.IsNaN(follow) && follow < item.value {
					return stats, OrderError{
						Stream: item.stream,
						Index:  next,
						Prev:   item.value,
						Cur:    follow,
					}
				}
				if err := push(item.stream, next); err != nil {
					return stats, err
				}
			}
		}
		yield(value, counts)
	}
	return stats, nil
}
