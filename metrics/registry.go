package metrics

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"sort"
	"sync"
	"time"
)

// Clock 注入当前时间，便于确定性地测试空闲回收。
type Clock func() time.Time

// Logger 记录输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// Registry 是带序列数上限的指标注册与聚合器。
type Registry struct {
	mu sync.RWMutex

	maxSeries int
	idleTTL   time.Duration
	clock     Clock
	logger    Logger

	metrics  map[string]*metric
	rejected map[RejectReason]int64
}

// Option 配置 Registry。
type Option func(*Registry)

// WithClock 注入时钟。
func WithClock(c Clock) Option {
	return func(r *Registry) { r.clock = c }
}

// WithLogger 注入日志输出。
func WithLogger(l Logger) Option {
	return func(r *Registry) { r.logger = l }
}

// New 创建 Registry；maxSeries 为每个指标的普通序列名额 N（N 必须为正），
// idleTTL 为普通序列的空闲回收时长 T。
func New(maxSeries int, idleTTL time.Duration, opts ...Option) (*Registry, error) {
	if maxSeries <= 0 {
		return nil, fmt.Errorf("metrics: maxSeries must be positive, got %d", maxSeries)
	}
	if idleTTL < 0 {
		return nil, fmt.Errorf("metrics: idleTTL must be non-negative, got %s", idleTTL)
	}
	r := &Registry{
		maxSeries: maxSeries,
		idleTTL:   idleTTL,
		clock:     time.Now,
		logger:    log.New(io.Discard, "metrics ", log.LstdFlags),
		metrics:   make(map[string]*metric),
		rejected:  make(map[RejectReason]int64),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Register 声明指标定义；同名同定义重复注册为幂等。
func (r *Registry) Register(name string, def Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	if def.Type != Counter && def.Type != Histogram {
		return r.rejectLocked(now, "register", name, nil, 0,
			fmt.Errorf("metrics: unknown metric type %d", def.Type))
	}
	if def.Type == Histogram && !validateBuckets(def.Buckets) {
		return r.rejectLocked(now, "register", name, nil, 0,
			r.reasonErr(ReasonBucketsNotSorted, "histogram buckets must be finite and strictly increasing"))
	}

	allowed := make(map[string]struct{}, len(def.Labels))
	for _, label := range def.Labels {
		allowed[label] = struct{}{}
	}

	if existing, ok := r.metrics[name]; ok {
		if sameDefinition(existing, def.Type, allowed, def.Buckets) {
			r.logger.Printf("register input name=%q def=%+v output=idempotent reason=already_registered_same_definition at=%s",
				name, def, now.Format(time.RFC3339Nano))
			return nil
		}
		return r.rejectLocked(now, "register", name, nil, 0,
			r.reasonErr(ReasonDefinitionMismatch, "metric already registered with a different definition"))
	}

	m := &metric{
		typ:    def.Type,
		labels: allowed,
		active: make(map[string]*series),
	}
	if def.Type == Histogram {
		m.buckets = append([]float64(nil), def.Buckets...)
	}
	r.metrics[name] = m
	r.logger.Printf("register input name=%q def=%+v output=accepted reason=new_metric at=%s",
		name, def, now.Format(time.RFC3339Nano))
	return nil
}

// AddCounter 向计数器序列累加非负增量。
func (r *Registry) AddCounter(name string, labels Labels, delta int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	m, err := r.validateReportLocked("add_counter", name, labels, now)
	if err != nil {
		return err
	}
	if m.typ != Counter {
		return r.rejectLocked(now, "add_counter", name, labels, 0,
			r.reasonErr(ReasonDefinitionMismatch, "metric is not a counter"))
	}
	if delta < 0 {
		return r.rejectLocked(now, "add_counter", name, labels, delta,
			r.reasonErr(ReasonNegativeDelta, "counter delta must be non-negative"))
	}

	key := labelKey(labels, m.labels)
	target, overflow := r.resolveSeriesLocked(m, labels, key, now)
	target.value += delta
	target.lastUpdate = now
	m.accepted += delta
	r.logger.Printf("add_counter input name=%q labels=%v delta=%d output=accepted target=%s value=%d at=%s",
		name, labels, delta, targetName(overflow), target.value, now.Format(time.RFC3339Nano))
	return nil
}

// Observe 向直方图序列记录一个观测值。
func (r *Registry) Observe(name string, labels Labels, value float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	m, err := r.validateReportLocked("observe", name, labels, now)
	if err != nil {
		return err
	}
	if m.typ != Histogram {
		return r.rejectLocked(now, "observe", name, labels, value,
			r.reasonErr(ReasonDefinitionMismatch, "metric is not a histogram"))
	}
	if math.IsNaN(value) {
		return r.rejectLocked(now, "observe", name, labels, value,
			r.reasonErr(ReasonNaNObservation, "histogram observation must not be NaN"))
	}

	key := labelKey(labels, m.labels)
	target, overflow := r.resolveSeriesLocked(m, labels, key, now)
	idx := bucketIndex(m.buckets, value)
	target.bucketCounts[idx]++
	target.count++
	target.sum += value
	target.lastUpdate = now
	m.accepted++
	r.logger.Printf("observe input name=%q labels=%v value=%g output=accepted target=%s bucket=%d count=%d at=%s",
		name, labels, value, targetName(overflow), idx, target.count, now.Format(time.RFC3339Nano))
	return nil
}

// ReclaimExpired 回收所有空闲时长超过 T 的普通序列，返回被回收的序列数。
// 判定规则：now - lastUpdate > T 才回收（恰为 T 时保留）。
// 被回收序列的全部累计值并入溢出序列并释放名额；溢出序列永不回收。
func (r *Registry) ReclaimExpired() (reclaimed int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	names := make([]string, 0, len(r.metrics))
	for name := range r.metrics {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		m := r.metrics[name]
		for _, key := range append([]string(nil), m.order...) {
			s := m.active[key]
			idle := now.Sub(s.lastUpdate)
			if idle <= r.idleTTL {
				r.logger.Printf("reclaim input name=%q key=%q idle=%s output=kept reason=within_ttl at=%s",
					name, key, idle, now.Format(time.RFC3339Nano))
				continue
			}
			if m.overflow == nil {
				m.overflow = newSeries(m, Labels{"__overflow__": "true"}, s.lastUpdate)
			}
			foldInto(m.overflow, s)
			m.overflow.lastUpdate = now
			delete(m.active, key)
			m.order = removeKey(m.order, key)
			reclaimed++
			r.logger.Printf("reclaim input name=%q key=%q idle=%s output=reclaimed reason=idle_over_ttl folded_into=overflow value=%d count=%d at=%s",
				name, key, idle, s.value, s.count, now.Format(time.RFC3339Nano))
		}
	}
	return reclaimed
}

// Export 导出全量一致快照。
func (r *Registry) Export() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock()
	snap := Snapshot{
		Metrics:  make([]MetricSnapshot, 0, len(r.metrics)),
		Rejected: make(RejectCounters, len(r.rejected)),
	}
	for reason, count := range r.rejected {
		snap.Rejected[reason] = count
	}

	names := make([]string, 0, len(r.metrics))
	for name := range r.metrics {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		m := r.metrics[name]
		ms := MetricSnapshot{
			Name:          name,
			Type:          m.typ,
			Buckets:       append([]float64(nil), m.buckets...),
			Series:        make([]SeriesSnapshot, 0, len(m.order)),
			AcceptedTotal: m.accepted,
		}
		for _, key := range m.order {
			ms.Series = append(ms.Series, snapshotSeries(m, m.active[key]))
		}
		if m.overflow != nil {
			o := snapshotSeries(m, m.overflow)
			o.Labels = nil
			ms.Overflow = &o
		}
		snap.Metrics = append(snap.Metrics, ms)
	}
	r.logger.Printf("export output metrics=%d rejected=%v at=%s",
		len(snap.Metrics), snap.Rejected, now.Format(time.RFC3339Nano))
	return snap
}

// RejectedCount 返回某一拒绝原因的累计次数。
func (r *Registry) RejectedCount(reason RejectReason) int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rejected[reason]
}

// RejectError 携带结构化的拒绝原因，便于调用方分类处理。
type RejectError struct {
	Reason  RejectReason
	Message string
}

func (e *RejectError) Error() string { return string(e.Reason) + ": " + e.Message }

func (r *Registry) reasonErr(reason RejectReason, msg string) *RejectError {
	return &RejectError{Reason: reason, Message: msg}
}

// rejectLocked 按原因计数、打印判定依据并返回错误；拒绝前不得修改任何状态。
func (r *Registry) rejectLocked(now time.Time, op, name string, labels Labels, input any, err error) error {
	reason := RejectReason("invalid")
	var rej *RejectError
	if errors.As(err, &rej) {
		reason = rej.Reason
		r.rejected[reason]++
	}
	r.logger.Printf("%s input name=%q labels=%v value=%v output=rejected reason=%s detail=%q at=%s",
		op, name, labels, input, reason, err.Error(), now.Format(time.RFC3339Nano))
	return err
}

// validateReportLocked 执行所有上报共享的校验：指标存在、定义类型匹配在外层处理、
// 标签集合必须与允许集合完全一致。
func (r *Registry) validateReportLocked(op, name string, labels Labels, now time.Time) (*metric, error) {
	m, ok := r.metrics[name]
	if !ok {
		return nil, r.rejectLocked(now, op, name, labels, nil,
			r.reasonErr(ReasonMetricNotRegistered, "metric is not registered"))
	}
	missing, extra, ok := checkLabels(labels, m.labels)
	if !ok {
		if missing != "" {
			return nil, r.rejectLocked(now, op, name, labels, nil,
				r.reasonErr(ReasonLabelMissing, "missing required label: "+missing))
		}
		return nil, r.rejectLocked(now, op, name, labels, nil,
			r.reasonErr(ReasonLabelNotAllowed, "label not in allowed set: "+extra))
	}
	return m, nil
}

// resolveSeriesLocked 返回本次上报应写入的序列：
// 已存在的普通序列照常累加；满额后的新标签集合折叠进唯一的溢出序列。
func (r *Registry) resolveSeriesLocked(m *metric, labels Labels, key string, now time.Time) (target *series, overflow bool) {
	if s, ok := m.active[key]; ok {
		return s, false
	}
	if len(m.active) < r.maxSeries {
		s := newSeries(m, labels, now)
		m.active[key] = s
		m.order = append(m.order, key)
		r.logger.Printf("allocate output=new_series key=%q active=%d/%d at=%s",
			key, len(m.active), r.maxSeries, now.Format(time.RFC3339Nano))
		return s, false
	}
	if m.overflow == nil {
		m.overflow = newSeries(m, Labels{"__overflow__": "true"}, now)
		r.logger.Printf("allocate output=new_overflow key=%q reason=max_series_reached limit=%d at=%s",
			key, r.maxSeries, now.Format(time.RFC3339Nano))
	} else {
		r.logger.Printf("allocate output=overflow key=%q reason=max_series_reached limit=%d at=%s",
			key, r.maxSeries, now.Format(time.RFC3339Nano))
	}
	return m.overflow, true
}

func sameDefinition(m *metric, typ MetricType, allowed map[string]struct{}, buckets []float64) bool {
	if m.typ != typ || len(m.labels) != len(allowed) {
		return false
	}
	for name := range allowed {
		if _, ok := m.labels[name]; !ok {
			return false
		}
	}
	if typ != Histogram {
		return len(m.buckets) == 0
	}
	if len(m.buckets) != len(buckets) {
		return false
	}
	for i := range buckets {
		if m.buckets[i] != buckets[i] {
			return false
		}
	}
	return true
}

func cloneLabels(labels Labels) Labels {
	cp := make(Labels, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	return cp
}

func removeKey(order []string, key string) []string {
	for i, k := range order {
		if k == key {
			return append(order[:i], order[i+1:]...)
		}
	}
	return order
}

func targetName(overflow bool) string {
	if overflow {
		return "overflow"
	}
	return "series"
}
