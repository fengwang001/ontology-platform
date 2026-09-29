// Package metrics implements a capped-series metric registry and aggregator.
//
// Each registered metric keeps at most N ordinary series distinguished by
// their label set. When the cap is reached, further distinct label sets are
// folded into a single per-metric overflow series that never occupies a slot
// and is never reclaimed.
package metrics

import (
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind is the metric type.
type Kind string

const (
	KindCounter   Kind = "counter"
	KindHistogram Kind = "histogram"
)

// Reason identifies why a registration or report was rejected.
type Reason string

const (
	RejectUnknownMetric   Reason = "metric_not_registered"
	RejectKindMismatch    Reason = "metric_kind_mismatch"
	RejectDuplicateDef    Reason = "duplicate_registration_conflict"
	RejectLabelMissing    Reason = "label_missing"
	RejectLabelNotAllowed Reason = "label_not_allowed"
	RejectCounterNegative Reason = "counter_negative_increment"
	RejectValueNaN        Reason = "observation_nan"
	RejectBadBuckets      Reason = "bucket_bounds_not_strictly_increasing"
	RejectBadLabelNames   Reason = "invalid_label_names"
)

// Definition declares a metric: its kind, allowed label names and, for
// histograms, ascending bucket upper bounds (the last bucket implicitly ends
// at +Inf).
type Definition struct {
	Name          string
	Kind          Kind
	AllowedLabels []string
	BucketBounds  []float64
}

// Registry aggregates metrics with a per-metric cap on ordinary series.
type Registry struct {
	mu        sync.Mutex
	maxSeries int
	idleTTL   time.Duration
	now       func() time.Time
	logger    Logger
	metrics   map[string]*metricState
	rejected  map[Reason]int64
	seq       uint64
}

// RejectError wraps a rejection with its classified reason.
type RejectError struct {
	Reason Reason
	msg    string
}

func (e *RejectError) Error() string { return string(e.Reason) + ": " + e.msg }

func reject(reason Reason, msg string) error {
	return &RejectError{Reason: reason, msg: msg}
}

type series struct {
	labels  map[string]string
	count   float64
	buckets []int64
	lastUse time.Time
}

type metricState struct {
	def          Definition
	series       map[string]*series
	overflow     *series
	total        float64
	bucketTotals []int64
}

// stdLogger adapts the standard library logger to Logger.
type stdLogger struct{ l *log.Logger }

func (s stdLogger) Printf(format string, args ...any) { s.l.Printf(format, args...) }

// DefaultLogger writes audit lines to standard output.
func DefaultLogger() Logger {
	return stdLogger{l: log.New(os.Stdout, "[metrics] ", log.LstdFlags|log.Lmicroseconds)}
}

// canonicalKey renders a label set in the order of the sorted allowed label
// names, so label maps given in different orders share one series.
func canonicalKey(labels map[string]string, orderedNames []string) string {
	var b strings.Builder
	for i, name := range orderedNames {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(labels[name])
	}
	return b.String()
}

// normalizeNames deduplicates label names and returns them sorted.
func normalizeNames(names []string) ([]string, bool) {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" {
			return nil, false
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, true
}

func sameBounds(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NewRegistry creates a Registry. maxSeries is the per-metric ordinary-series
// cap (must be positive), idleTTL is how long an ordinary series may remain
// unused before ReclaimIdle recycles it, and now is the injected clock.
func NewRegistry(maxSeries int, idleTTL time.Duration, now func() time.Time, logger Logger) (*Registry, error) {
	if maxSeries <= 0 {
		return nil, errors.New("maxSeries must be positive")
	}
	if idleTTL <= 0 {
		return nil, errors.New("idleTTL must be positive")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = DefaultLogger()
	}
	return &Registry{
		maxSeries: maxSeries,
		idleTTL:   idleTTL,
		now:       now,
		logger:    logger,
		metrics:   make(map[string]*metricState),
		rejected:  make(map[Reason]int64),
	}, nil
}

// Logger is the logging sink used for input/output/decision audit lines.
type Logger interface {
	Printf(format string, args ...any)
}

// Register declares a metric. Registering the same name with the same kind,
// allowed-label set and bucket bounds is idempotent; a conflicting definition
// is rejected without changing state.
func (r *Registry) Register(def Definition) error {
	r.logger.Printf("REGISTER input name=%q kind=%q labels=%v bounds=%v", def.Name, def.Kind, def.AllowedLabels, def.BucketBounds)

	if def.Kind != KindCounter && def.Kind != KindHistogram {
		return r.fail(def.Name, RejectKindMismatch, "unknown kind %q", def.Kind)
	}
	names, ok := normalizeNames(def.AllowedLabels)
	if !ok {
		return r.fail(def.Name, RejectBadLabelNames, "empty label name in %v", def.AllowedLabels)
	}
	bounds := append([]float64(nil), def.BucketBounds...)
	if def.Kind == KindHistogram {
		if len(bounds) == 0 {
			return r.fail(def.Name, RejectBadBuckets, "histogram requires at least one upper bound")
		}
		for i, b := range bounds {
			if math.IsNaN(b) || math.IsInf(b, 0) {
				return r.fail(def.Name, RejectBadBuckets, "bound #%d is NaN or Inf", i)
			}
			if i > 0 && b <= bounds[i-1] {
				return r.fail(def.Name, RejectBadBuckets, "bound #%d %v not greater than previous %v", i, b, bounds[i-1])
			}
		}
	}

	normalized := Definition{Name: def.Name, Kind: def.Kind, AllowedLabels: names, BucketBounds: bounds}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.metrics[def.Name]; ok {
		same := existing.def.Kind == normalized.Kind &&
			sameBounds(existing.def.BucketBounds, normalized.BucketBounds)
		en, nn := existing.def.AllowedLabels, normalized.AllowedLabels
		if len(en) == len(nn) {
			for i := range en {
				if en[i] != nn[i] {
					same = false
				}
			}
		} else {
			same = false
		}
		if !same {
			r.rejected[RejectDuplicateDef]++
			r.logger.Printf("REGISTER output name=%q REJECT reason=%s state unchanged", def.Name, RejectDuplicateDef)
			return reject(RejectDuplicateDef, "metric "+def.Name+" already registered with a different definition")
		}
		r.logger.Printf("REGISTER output name=%q OK idempotent (definition unchanged)", def.Name)
		return nil
	}

	r.metrics[def.Name] = &metricState{
		def:      normalized,
		series:   make(map[string]*series),
		overflow: nil,
	}
	if normalized.Kind == KindHistogram {
		r.metrics[def.Name].bucketTotals = make([]int64, len(bounds)+1)
	}
	r.logger.Printf("REGISTER output name=%q OK created (cap=%d)", def.Name, r.maxSeries)
	return nil
}

// AddCounter adds a non-negative increment to a counter series.
func (r *Registry) AddCounter(name string, labels map[string]string, increment float64) error {
	r.logger.Printf("ADD_COUNTER input metric=%q labels=%v increment=%v", name, labels, increment)

	if math.IsNaN(increment) {
		return r.fail(name, RejectValueNaN, "counter increment is NaN")
	}
	if increment < 0 {
		return r.fail(name, RejectCounterNegative, "increment %v is negative", increment)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	ms, key, err := r.resolveForWriteLocked(name, labels)
	if err != nil {
		return err
	}
	if ms.def.Kind != KindCounter {
		return r.failLocked(name, RejectKindMismatch, "AddCounter called on histogram metric %q", name)
	}
	target := r.targetSeriesLocked(ms, key, labels)
	target.count += increment
	target.lastUse = r.now()
	ms.total += increment
	r.logger.Printf("ADD_COUNTER output metric=%q key=%q overflow=%t increment=%v seriesCount=%v total=%v",
		name, key, target == ms.overflow, increment, target.count, ms.total)
	return nil
}

// Observe records a value in a histogram series.
func (r *Registry) Observe(name string, labels map[string]string, value float64) error {
	r.logger.Printf("OBSERVE input metric=%q labels=%v value=%v", name, labels, value)

	if math.IsNaN(value) {
		return r.fail(name, RejectValueNaN, "observation is NaN")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	ms, key, err := r.resolveForWriteLocked(name, labels)
	if err != nil {
		return err
	}
	if ms.def.Kind != KindHistogram {
		return r.failLocked(name, RejectKindMismatch, "Observe called on counter metric %q", name)
	}
	target := r.targetSeriesLocked(ms, key, labels)
	idx := sort.SearchFloat64s(ms.def.BucketBounds, value) // first bound >= value: boundary falls in that bucket
	target.count++
	target.buckets[idx]++
	target.lastUse = r.now()
	ms.total++
	ms.bucketTotals[idx]++
	r.logger.Printf("OBSERVE output metric=%q key=%q overflow=%t value=%v bucket=%d total=%v",
		name, key, target == ms.overflow, value, idx, ms.total)
	return nil
}

func (r *Registry) fail(metric string, reason Reason, format string, args ...any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failLocked(metric, reason, format, args...)
}

func (r *Registry) failLocked(metric string, reason Reason, format string, args ...any) error {
	r.rejected[reason]++
	r.logger.Printf("REJECT metric=%q reason=%s detail=%q state unchanged", metric, reason, fmt.Sprintf(format, args...))
	return reject(reason, fmt.Sprintf(format, args...))
}

// resolveForWriteLocked looks up the metric and fully validates the label set.
func (r *Registry) resolveForWriteLocked(name string, labels map[string]string) (*metricState, string, error) {
	ms, ok := r.metrics[name]
	if !ok {
		return nil, "", r.failLocked(name, RejectUnknownMetric, "metric %q is not registered", name)
	}
	for k := range labels {
		allowed := false
		for _, n := range ms.def.AllowedLabels {
			if n == k {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, "", r.failLocked(name, RejectLabelNotAllowed, "label %q not in allowed set", k)
		}
	}
	if len(labels) != len(ms.def.AllowedLabels) {
		return nil, "", r.failLocked(name, RejectLabelMissing,
			"got %d labels, metric requires exactly %d", len(labels), len(ms.def.AllowedLabels))
	}
	for _, allowed := range ms.def.AllowedLabels {
		if _, present := labels[allowed]; !present {
			return nil, "", r.failLocked(name, RejectLabelMissing, "required label %q absent", allowed)
		}
	}
	return ms, canonicalKey(labels, ms.def.AllowedLabels), nil
}

// targetSeriesLocked returns the ordinary series for key, creating it while a
// slot is free; once full (or when key denotes an evicted set) it returns the
// metric's unique overflow series.
func (r *Registry) targetSeriesLocked(ms *metricState, key string, labels map[string]string) *series {
	if s, ok := ms.series[key]; ok {
		return s
	}
	if len(ms.series) < r.maxSeries {
		s := &series{
			labels:  cloneLabels(labels, ms.def.AllowedLabels),
			lastUse: r.now(),
		}
		if ms.def.Kind == KindHistogram {
			s.buckets = make([]int64, len(ms.def.BucketBounds)+1)
		}
		ms.series[key] = s
		r.logger.Printf("SLOT metric=%q key=%q decision=ordinary slot allocated (%d/%d)",
			ms.def.Name, key, len(ms.series), r.maxSeries)
		return s
	}
	if ms.overflow == nil {
		ms.overflow = &series{
			labels:  map[string]string{},
			lastUse: r.now(),
		}
		if ms.def.Kind == KindHistogram {
			ms.overflow.buckets = make([]int64, len(ms.def.BucketBounds)+1)
		}
		r.logger.Printf("SLOT metric=%q decision=overflow series created (cap=%d reached)", ms.def.Name, r.maxSeries)
	} else {
		r.logger.Printf("SLOT metric=%q decision=overflow (cap=%d reached)", ms.def.Name, r.maxSeries)
	}
	return ms.overflow
}

func cloneLabels(labels map[string]string, orderedNames []string) map[string]string {
	out := make(map[string]string, len(orderedNames))
	for _, n := range orderedNames {
		out[n] = labels[n]
	}
	return out
}

// ReclaimIdle recycles ordinary series idle for longer than idleTTL at the
// clock instant. Their accumulated values merge into the metric overflow
// series, freeing their slots. It returns the number of reclaimed series.
func (r *Registry) ReclaimIdle() int {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()

	reclaimed := 0
	for _, ms := range r.metrics {
		for key, s := range ms.series {
			if now.Sub(s.lastUse) <= r.idleTTL {
				continue
			}
			if ms.overflow == nil {
				ms.overflow = &series{labels: map[string]string{}}
				if ms.def.Kind == KindHistogram {
					ms.overflow.buckets = make([]int64, len(ms.def.BucketBounds)+1)
				}
			}
			ms.overflow.count += s.count
			if s.buckets != nil {
				for i, v := range s.buckets {
					ms.overflow.buckets[i] += v
				}
			}
			delete(ms.series, key)
			reclaimed++
			r.logger.Printf("RECLAIM metric=%q key=%q decision=recycle idleSince=%v mergedCount=%v slotsFreed=%d (now %d/%d)",
				ms.def.Name, key, s.lastUse, s.count, reclaimed, len(ms.series), r.maxSeries)
		}
	}
	r.logger.Printf("RECLAIM_IDLE output reclaimed=%d at=%v", reclaimed, now)
	return reclaimed
}

// SeriesSnapshot is one exported series.
type SeriesSnapshot struct {
	Labels   map[string]string
	Overflow bool
	Count    float64
	Buckets  []float64
}

// MetricSnapshot is a consistent export of one metric.
type MetricSnapshot struct {
	Name         string
	Kind         Kind
	Series       []SeriesSnapshot
	Total        float64
	BucketTotals []float64
}

// Export returns a consistent snapshot of every metric.
func (r *Registry) Export() []MetricSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]MetricSnapshot, 0, len(r.metrics))
	names := make([]string, 0, len(r.metrics))
	for name := range r.metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ms := r.metrics[name]
		snap := MetricSnapshot{
			Name:  name,
			Kind:  ms.def.Kind,
			Total: ms.total,
		}
		keys := make([]string, 0, len(ms.series))
		for key := range ms.series {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			s := ms.series[key]
			snap.Series = append(snap.Series, snapshotSeries(s, false, ms.def.Kind))
		}
		if ms.overflow != nil {
			snap.Series = append(snap.Series, snapshotSeries(ms.overflow, true, ms.def.Kind))
		}
		if ms.def.Kind == KindHistogram {
			snap.BucketTotals = cumulative(ms.bucketTotals)
			var seriesCount float64
			var lastCumulative float64
			for _, ss := range snap.Series {
				seriesCount += ss.Count
				lastCumulative += ss.Buckets[len(ss.Buckets)-1]
			}
			r.logger.Printf("EXPORT metric=%q ordinary=%d overflow=%t seriesSum=%v total=%v finalBucket=%v decision=%s",
				name, len(ms.series), ms.overflow != nil, seriesCount, ms.total, lastCumulative,
				conservationOK(seriesCount, ms.total, float64(lastCumulative)))
		} else {
			var seriesCount float64
			for _, ss := range snap.Series {
				seriesCount += ss.Count
			}
			r.logger.Printf("EXPORT metric=%q ordinary=%d overflow=%t seriesSum=%v total=%v decision=%s",
				name, len(ms.series), ms.overflow != nil, seriesCount, ms.total,
				conservationOK(seriesCount, ms.total, seriesCount))
		}
		out = append(out, snap)
	}
	r.logger.Printf("EXPORT output metrics=%d", len(out))
	return out
}

// RejectionCounts returns rejection tallies keyed by reason.
func (r *Registry) RejectionCounts() map[Reason]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[Reason]int64, len(r.rejected))
	for reason, n := range r.rejected {
		out[reason] = n
	}
	return out
}

func snapshotSeries(s *series, overflow bool, kind Kind) SeriesSnapshot {
	snap := SeriesSnapshot{
		Labels:   cloneLabels(s.labels, sortedKeys(s.labels)),
		Overflow: overflow,
		Count:    s.count,
	}
	if kind == KindHistogram {
		snap.Buckets = cumulative(s.buckets)
	}
	return snap
}

func cumulative(raw []int64) []float64 {
	out := make([]float64, len(raw))
	var sum int64
	for i, v := range raw {
		sum += v
		out[i] = float64(sum)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func conservationOK(seriesSum, total, finalBucket float64) string {
	if seriesSum == total && finalBucket == total {
		return "conservation_holds"
	}
	return "CONSERVATION_VIOLATION"
}
