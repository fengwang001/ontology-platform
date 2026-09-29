package metrics

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// series 是一条普通序列或溢出序列的内部可变状态。
type series struct {
	labels Labels

	// counter 累计值。
	value int64

	// histogram 各桶累计计数，长度为 len(buckets)+1，末桶为 +Inf。
	bucketCounts []uint64
	count        uint64
	sum          float64

	// lastUpdate 是该序列最后一次被接受上报的时间。
	lastUpdate time.Time
}

// metric 是单个指标的注册定义与全部序列状态。
type metric struct {
	typ     MetricType
	labels  map[string]struct{}
	buckets []float64

	// active 为占用名额的普通序列，按创建先后排列；key 见 labelKey。
	active map[string]*series
	order  []string

	// overflow 是该指标唯一的溢出序列，不占名额、永不回收；
	// 在第一次需要折叠（新序列满额或回收并入）时惰性创建。
	overflow *series

	// accepted 为已接受上报的总量：
	// 计数器为全部增量之和；直方图为观测条数。
	accepted int64
}

// labelKey 按允许标签名的规范顺序（排序后）拼接标签集合，
// 使顺序不同但集合相同的标签得到同一个键。
// 调用前调用方必须保证标签键集合与允许集合完全一致。
func labelKey(labels Labels, allowed map[string]struct{}) string {
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(strconv.Quote(name))
		b.WriteByte('=')
		b.WriteString(strconv.Quote(labels[name]))
		b.WriteByte('\x00')
	}
	return b.String()
}

// bucketIndex 返回观测值落入的桶下标。
// 观测值恰等于某上界时落入该上界对应的桶（<= 语义）；
// 大于全部上界的值落入末尾的 +Inf 桶。
func bucketIndex(buckets []float64, value float64) int {
	idx := sort.SearchFloat64s(buckets, value)
	if idx < len(buckets) && buckets[idx] == value {
		return idx
	}
	return idx
}

// newSeries 创建一条序列，直方图序列的桶数组在此分配。
func newSeries(m *metric, labels Labels, now time.Time) *series {
	s := &series{labels: labels, lastUpdate: now}
	if m.typ == Histogram {
		s.bucketCounts = make([]uint64, len(m.buckets)+1)
	}
	return s
}

// snapshotSeries 导出单序列快照；直方图桶计数导出为累计计数
// （cumulative，末桶等于该序列观测总数），内部仍保存各桶独立计数。
func snapshotSeries(m *metric, s *series) SeriesSnapshot {
	snap := SeriesSnapshot{
		Labels: cloneLabels(s.labels),
		Value:  s.value,
		Count:  s.count,
		Sum:    s.sum,
	}
	if m.typ == Histogram {
		snap.BucketCounts = append([]uint64(nil), s.bucketCounts...)
		var cumulative uint64
		for i, c := range snap.BucketCounts {
			cumulative += c
			snap.BucketCounts[i] = cumulative
		}
	}
	return snap
}

// foldInto 把 src 的全部累计值并入 dst，用于溢出折叠与回收并入。
func foldInto(dst, src *series) {
	dst.value += src.value
	if src.bucketCounts != nil {
		if dst.bucketCounts == nil {
			dst.bucketCounts = make([]uint64, len(src.bucketCounts))
		}
		for i, c := range src.bucketCounts {
			dst.bucketCounts[i] += c
		}
		dst.count += src.count
		dst.sum += src.sum
	}
}

// validateBuckets 校验桶上界必须为有限数且严格递增。
func validateBuckets(buckets []float64) bool {
	for i, bound := range buckets {
		if math.IsNaN(bound) || math.IsInf(bound, 0) {
			return false
		}
		if i > 0 && !(bound > buckets[i-1]) {
			return false
		}
	}
	return true
}

// labelsEqualSet 判断给出的标签键集合是否与允许集合完全一致，
// 返回第一个缺失的标签名（若有）。
func checkLabels(labels Labels, allowed map[string]struct{}) (missing string, extra string, ok bool) {
	for name := range allowed {
		if _, present := labels[name]; !present {
			return name, "", false
		}
	}
	for name := range labels {
		if _, permitted := allowed[name]; !permitted {
			return "", name, false
		}
	}
	return "", "", true
}
