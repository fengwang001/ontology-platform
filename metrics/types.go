package metrics

// MetricType 标识指标类型。
type MetricType int

const (
	// Counter 是单调非负累加计数器。
	Counter MetricType = iota + 1
	// Histogram 是带升序桶上界的直方图。
	Histogram
)

// RejectReason 是注册或上报被整体拒绝的分类原因。
type RejectReason string

const (
	ReasonMetricNotRegistered RejectReason = "metric_not_registered"
	ReasonDefinitionMismatch  RejectReason = "definition_mismatch"
	ReasonLabelMissing        RejectReason = "label_missing"
	ReasonLabelNotAllowed     RejectReason = "label_not_allowed"
	ReasonNegativeDelta       RejectReason = "negative_delta"
	ReasonNaNObservation      RejectReason = "nan_observation"
	ReasonBucketsNotSorted    RejectReason = "buckets_not_strictly_increasing"
)

// Definition 是指标的注册定义。
//
// Labels 为允许的标签名集合，与给出顺序无关。Buckets 为直方图桶上界，
// 必须严格递增，末尾隐含正无穷桶；计数器忽略 Buckets。
type Definition struct {
	Type    MetricType
	Labels  []string
	Buckets []float64
}

// Labels 是一次上报携带的标签集合（名->值）。
type Labels map[string]string

// SeriesSnapshot 是单个序列的一致快照。
type SeriesSnapshot struct {
	Labels Labels

	// Counter 序列的累计值。
	Value int64

	// Histogram 序列各桶的累计计数；长度为 len(Buckets)+1，末桶为正无穷桶。
	BucketCounts []uint64
	// 落入直方图的观测总数，等于 BucketCounts 末桶的累计值。
	Count uint64
	// 全部观测值之和。
	Sum float64
}

// MetricSnapshot 是单个指标在某一时刻的一致快照。
type MetricSnapshot struct {
	Name          string
	Type          MetricType
	Buckets       []float64
	Series        []SeriesSnapshot
	Overflow      *SeriesSnapshot
	AcceptedTotal int64
}

// RejectCounters 按原因汇总被整体拒绝的操作次数。
type RejectCounters map[RejectReason]int64

// Snapshot 是一次导出的全量一致快照。
type Snapshot struct {
	Metrics  []MetricSnapshot
	Rejected RejectCounters
}
