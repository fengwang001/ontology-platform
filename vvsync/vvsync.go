// Package vvsync implements version-vector based incremental anti-entropy
// synchronization between key/value replicas.
package vvsync

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// Change is one local write, uniquely identified by its Origin replica and
// strictly increasing sequence number.
type Change struct {
	Origin    string
	Seq       int64
	Key       string
	Value     string
	Timestamp int64
}

// VersionVector maps an origin replica id to the highest sequence number
// produced by that origin and seen by the holder. Origins that have never
// been seen are absent (implicitly zero).
type VersionVector map[string]int64

// SyncError is a rejected operation with a distinct, machine-readable reason.
type SyncError struct {
	Reason  string
	Message string
}

func (e *SyncError) Error() string { return e.Reason + ": " + e.Message }

// Distinct rejection reasons.
const (
	ReasonUnknownReplica = "unknown_replica"
	ReasonNonContiguous  = "non_contiguous_changes"
	ReasonInvalidVector  = "invalid_version_vector"
	ReasonLogLimit       = "log_limit_exceeded"
)

func errf(reason, format string, args ...any) error {
	return &SyncError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// Cluster is a closed group of registered replicas that synchronize among
// themselves.
type Cluster struct {
	mu       sync.Mutex
	replicas map[string]*Replica
	order    []string
	maxLog   int
	logMu    sync.Mutex
	logTo    io.Writer
}

// NewCluster creates a cluster that rejects logs larger than maxLog entries.
func NewCluster(maxLog int) *Cluster {
	return &Cluster{
		replicas: make(map[string]*Replica),
		maxLog:   maxLog,
		logTo:    os.Stderr,
	}
}

// SetLogWriter redirects the per-step synchronization decision log.
func (c *Cluster) SetLogWriter(w io.Writer) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	c.logTo = w
}

func (c *Cluster) logf(format string, args ...any) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	w := c.logTo
	if w != nil {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

// lockedLookup requires c.mu to be held.
func (c *Cluster) lockedLookup(id string) (*Replica, bool) {
	r, ok := c.replicas[id]
	return r, ok
}

func (c *Cluster) lookup(id string) (*Replica, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lockedLookup(id)
}

// Register creates and registers a named replica.
func (c *Cluster) Register(id string) (*Replica, error) {
	c.mu.Lock()
	if id == "" {
		c.mu.Unlock()
		return nil, errf(ReasonUnknownReplica, "replica id must not be empty")
	}
	if _, exists := c.replicas[id]; exists {
		c.mu.Unlock()
		return nil, errf(ReasonUnknownReplica, "replica %q already registered", id)
	}
	r := &Replica{
		cluster: c,
		id:      id,
		rank:    len(c.order),
		vector:  make(VersionVector),
	}
	c.replicas[id] = r
	c.order = append(c.order, id)
	c.mu.Unlock()
	c.logf("[register] replica=%s rank=%d maxLog=%d", id, r.rank, c.maxLog)
	return r, nil
}

// Replica is a single mutable copy holding its own write log and vector.
type Replica struct {
	cluster *Cluster

	mu     sync.Mutex
	id     string
	rank   int
	seq    int64
	vector VersionVector
	// log holds applied changes in application order.
	log []Change
}

// ID returns the replica identifier.
func (r *Replica) ID() string { return r.id }

// Write applies a local write, advancing the replica's own sequence and vector.
func (r *Replica) Write(key, value string, timestamp int64) (Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.log)+1 > r.cluster.maxLog {
		return Change{}, errf(ReasonLogLimit,
			"local write rejected: log would have %d entries, limit is %d",
			len(r.log)+1, r.cluster.maxLog)
	}
	r.seq++
	ch := Change{
		Origin:    r.id,
		Seq:       r.seq,
		Key:       key,
		Value:     value,
		Timestamp: timestamp,
	}
	r.log = append(r.log, ch)
	r.vector[r.id] = r.seq
	r.cluster.logf("[write] replica=%s origin=%s seq=%d key=%q ts=%d vector=%s",
		r.id, ch.Origin, ch.Seq, key, timestamp, formatVector(r.vector))
	return ch, nil
}

// Snapshot returns copies of the replica's version vector and full log.
func (r *Replica) Snapshot() (VersionVector, []Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return copyVector(r.vector), copyChanges(r.log)
}

// View returns the replicated key/value read view.
func (r *Replica) View() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return computeView(r.log)
}

// highest returns the highest applied sequence for origin, or 0 if unseen.
// The caller must hold r.mu.
func (r *Replica) highest(origin string) int64 {
	return r.vector[origin]
}

// copyVectorLocked requires r.mu to be held.
func (r *Replica) copyVectorLocked() VersionVector { return copyVector(r.vector) }

// SyncOnce performs one push of source's missing changes into target:
// target sends its vector, source answers with the minimal ordered diff,
// target validates and applies it atomically. It returns the changes sent.
func (c *Cluster) SyncOnce(source, target *Replica) ([]Change, error) {
	if source == nil || target == nil {
		return nil, errf(ReasonUnknownReplica, "source and target replicas must not be nil")
	}
	// Stable cluster-wide lock order prevents deadlocks.
	first, second := source, target
	if first.rank > second.rank {
		first, second = second, first
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	second.mu.Lock()
	defer second.mu.Unlock()

	c.mu.Lock()
	sr, sok := c.replicas[source.id]
	tr, tok := c.replicas[target.id]
	c.mu.Unlock()
	if !sok || sr != source {
		return nil, errf(ReasonUnknownReplica, "source replica %q is not registered in this cluster", source.id)
	}
	if !tok || tr != target {
		return nil, errf(ReasonUnknownReplica, "target replica %q is not registered in this cluster", target.id)
	}

	targetVector := target.copyVectorLocked()
	c.logf("[sync-begin] source=%s target=%s targetVector=%s",
		source.id, target.id, formatVector(targetVector))

	diff := computeDiff(source.log, targetVector)
	c.logf("[sync-diff] source=%s target=%s sent=%d changes=%s reason=%q",
		source.id, target.id, len(diff), formatChanges(diff),
		"seq > target vector value per origin (absent=>0); ordered by (origin, seq)")

	if err := validateAndApplyLocked(target, diff, c.maxLog); err != nil {
		c.logf("[sync-reject] source=%s target=%s error=%v state=unchanged",
			source.id, target.id, err)
		return nil, err
	}

	c.logf("[sync-accept] source=%s target=%s applied=%d targetVector=%s",
		source.id, target.id, len(diff), formatVector(target.vector))
	return copyChanges(diff), nil
}

// SyncBoth performs two opposing SyncOnce rounds (a pairwise exchange).
func (c *Cluster) SyncBoth(a, b *Replica) error {
	if _, err := c.SyncOnce(a, b); err != nil {
		return err
	}
	_, err := c.SyncOnce(b, a)
	return err
}

// computeDiff returns the minimal ordered set of source changes the target,
// described by its vector, has not seen. Origins unknown to the target simply
// contribute all of that origin's changes; no vector entry is created for
// origins that never produced changes.
func computeDiff(sourceLog []Change, targetVector VersionVector) []Change {
	diff := make([]Change, 0)
	for _, ch := range sourceLog {
		if ch.Seq > targetVector[ch.Origin] {
			diff = append(diff, ch)
		}
	}
	sort.SliceStable(diff, func(i, j int) bool {
		if diff[i].Origin != diff[j].Origin {
			return diff[i].Origin < diff[j].Origin
		}
		return diff[i].Seq < diff[j].Seq
	})
	return diff
}

// validateAndApplyLocked validates a remote batch against the receiver and,
// only if the whole batch is legal, applies every change. Any rejection leaves
// log and vector untouched. The caller must hold target.mu.
func validateAndApplyLocked(target *Replica, batch []Change, maxLog int) error {
	c := target.cluster

	// 1. Every origin must be a registered replica in this cluster.
	c.mu.Lock()
	for _, ch := range batch {
		if r, ok := c.replicas[ch.Origin]; !ok || r.cluster != c {
			c.mu.Unlock()
			return errf(ReasonUnknownReplica,
				"change origin %q is not a registered replica", ch.Origin)
		}
	}
	c.mu.Unlock()

	// 2. Batch must be globally ordered by (origin, seq) and, per origin,
	// must be a contiguous run starting at the receiver's next expected
	// sequence: no gaps, duplicates or regressions.
	expected := make(map[string]int64)
	for i, ch := range batch {
		if ch.Seq <= 0 {
			return errf(ReasonNonContiguous,
				"change from origin %q has non-positive sequence %d", ch.Origin, ch.Seq)
		}
		if i > 0 {
			prev := batch[i-1]
			if prev.Origin > ch.Origin ||
				(prev.Origin == ch.Origin && prev.Seq >= ch.Seq) {
				return errf(ReasonNonContiguous,
					"batch not strictly ordered by (origin, seq) at index %d", i)
			}
		}
		want, seen := expected[ch.Origin]
		if !seen {
			want = target.highest(ch.Origin) + 1
		}
		if ch.Seq != want {
			return errf(ReasonNonContiguous,
				"origin %q expected seq %d but got %d (gap or overlap)",
				ch.Origin, want, ch.Seq)
		}
		expected[ch.Origin] = want + 1
	}

	// 3. Log size limit applies to the whole batch atomically.
	if len(target.log)+len(batch) > maxLog {
		return errf(ReasonLogLimit,
			"batch rejected: log would grow to %d entries, limit is %d",
			len(target.log)+len(batch), maxLog)
	}

	// All checks passed: commit. New origins appear only if they actually
	// produced changes; no zero-valued vector entry is ever written.
	for _, ch := range batch {
		target.log = append(target.log, ch)
		target.vector[ch.Origin] = ch.Seq
	}
	return nil
}

// ValidateVector checks a vector received over the wire: keys must name
// registered replicas and entries must be positive and not ahead of the named
// origin's own log. It never mutates any state.
func (c *Cluster) ValidateVector(v VersionVector) error {
	if v == nil {
		return nil
	}
	for origin, seq := range v {
		if seq < 0 {
			return errf(ReasonInvalidVector,
				"vector entry for origin %q is negative (%d)", origin, seq)
		}
		if seq == 0 {
			return errf(ReasonInvalidVector,
				"vector entry for origin %q is zero (absent origins must be omitted)", origin)
		}
		r, ok := c.lookup(origin)
		if !ok || r.cluster != c {
			return errf(ReasonInvalidVector,
				"vector names unregistered replica %q", origin)
		}
		r.mu.Lock()
		have := r.highest(origin)
		r.mu.Unlock()
		if seq > have {
			return errf(ReasonInvalidVector,
				"vector claims origin %q at seq %d, but its log only reaches %d",
				origin, seq, have)
		}
	}
	return nil
}

// computeView picks, per key, the change with the lexicographically largest
// (timestamp, origin, seq) triple. (origin, seq) uniquely identifies a change,
// so the rule defines a total order.
func computeView(log []Change) map[string]string {
	view := make(map[string]string)
	type winner struct {
		ts     int64
		origin string
		seq    int64
	}
	winners := make(map[string]winner)
	for _, ch := range log {
		w, ok := winners[ch.Key]
		if !ok || dominates(ch, w.ts, w.origin, w.seq) {
			winners[ch.Key] = winner{ch.Timestamp, ch.Origin, ch.Seq}
			view[ch.Key] = ch.Value
		}
	}
	return view
}

func dominates(ch Change, ts int64, origin string, seq int64) bool {
	if ch.Timestamp != ts {
		return ch.Timestamp > ts
	}
	if ch.Origin != origin {
		return ch.Origin > origin
	}
	return ch.Seq > seq
}

func copyVector(v VersionVector) VersionVector {
	out := make(VersionVector, len(v))
	for k, n := range v {
		out[k] = n
	}
	return out
}

func copyChanges(in []Change) []Change {
	if len(in) == 0 {
		return []Change{}
	}
	out := make([]Change, len(in))
	copy(out, in)
	return out
}

func formatVector(v VersionVector) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, v[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatChanges(cs []Change) string {
	parts := make([]string, 0, len(cs))
	for _, ch := range cs {
		parts = append(parts, fmt.Sprintf("(%s,%d,%q=%q@%d)",
			ch.Origin, ch.Seq, ch.Key, ch.Value, ch.Timestamp))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// CanonicalLog returns the full log sorted by (origin, seq), which is the
// order in which converged replicas compare equal regardless of delivery
// order.
func (r *Replica) CanonicalLog() []Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := copyChanges(r.log)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin < out[j].Origin
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}
