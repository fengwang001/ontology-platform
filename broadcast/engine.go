

// Package broadcast implements a broadcast-state rule engine with global,
// monotonically versioned rule publications.
//
// Publications are invisible to processing instances until they are
// delivered. Each data item is keyed to a single instance and is stamped
// with the global version that was current at its arrival time; it is
// processed only when that instance has applied exactly that version, so
// the result depends solely on the published rule snapshot and never on
// inter-instance delivery timing.
package broadcast

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ChangeKind selects how a change affects a rule.
type ChangeKind int

const (
	// Upsert inserts or replaces a rule.
	Upsert ChangeKind = iota
	// Delete removes a rule. Deleting a missing rule id is legal (no-op).
	Delete
)

// Rule is a threshold rule. An input datum with Value >= Threshold hits.
type Rule struct {
	ID        string
	Threshold int
}

// Change is one rule change inside a publication.
type Change struct {
	Kind ChangeKind
	Rule Rule
}

// Hit is one rule match emitted for a datum, ordered by rule id.
type Hit struct {
	Instance int
	Seq      int
	Version  int
	Key      int
	Value    int
	RuleID   string
}

// Distinct, distinguishable rejection reasons.
var (
	// ErrInvalidRule: malformed rule in a publication (empty id, negative
	// threshold, duplicate id, or unknown change kind).
	ErrInvalidRule = errors.New("broadcast: invalid rule")
	// ErrUnknownInstance: deliver targets a non-existent instance.
	ErrUnknownInstance = errors.New("broadcast: unknown instance")
	// ErrInvalidVersion: deliver version is out of the published range or
	// skips versions / delivers an already applied version.
	ErrInvalidVersion = errors.New("broadcast: invalid deliver version")
	// ErrNegativeKey: a datum was sent with a negative key.
	ErrNegativeKey = errors.New("broadcast: negative data key")
	// ErrBufferFull: the routed instance buffer is full.
	ErrBufferFull = errors.New("broadcast: instance buffer full")
)

// Logger is the minimal logging surface used by the engine. A nil logger
// disables logging.
type Logger interface {
	Logf(format string, args ...any)
}

// Engine is the concurrency-safe broadcast-state rule engine.
type Engine struct {
	mu       sync.Mutex
	n        int
	cap      int
	logger   Logger
	globalV  int
	snaps    []map[string]int // snaps[v] = rule set (id -> threshold) at version v
	applied  []int            // version currently effective on each instance
	buffers  [][]buffered     // per-instance FIFO of data waiting for its tag
	hits     []Hit
	seq      int // global emission counter; hits within a datum share it
}

// New constructs an engine with numInstances processing instances, each
// owning a buffer of bufferCapacity slots.
func New(numInstances int, bufferCapacity int, logger Logger) *Engine {
	if numInstances < 1 {
		panic("broadcast: numInstances must be >= 1")
	}
	if bufferCapacity < 0 {
		panic("broadcast: bufferCapacity must be >= 0")
	}
	e := &Engine{
		n:       numInstances,
		cap:     bufferCapacity,
		logger:  logger,
		applied: make([]int, numInstances),
		buffers: make([][]buffered, numInstances),
		seq:     0,
	}
	// Version 0 is the empty baseline snapshot.
	e.snaps = []map[string]int{make(map[string]int)}
	return e
}

// Publish validates and publishes a batch of changes as one new global
// version. It returns the new global version. Nothing is applied to any
// instance until Deliver is called.
func (e *Engine) Publish(changes []Change) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Validate the whole batch before mutating anything: a rejection must
	// leave no trace.
	seen := make(map[string]bool, len(changes))
	for i, ch := range changes {
		switch ch.Kind {
		case Upsert, Delete:
		default:
			e.reject("publish", ErrInvalidRule,
				"changes[%d] unknown kind %d (rule id=%q)", i, ch.Kind, ch.Rule.ID)
			return e.globalV, ErrInvalidRule
		}
		if ch.Rule.ID == "" {
			e.reject("publish", ErrInvalidRule,
				"changes[%d] empty rule id", i)
			return e.globalV, ErrInvalidRule
		}
		if seen[ch.Rule.ID] {
			e.reject("publish", ErrInvalidRule,
				"changes[%d] duplicate rule id %q", i, ch.Rule.ID)
			return e.globalV, ErrInvalidRule
		}
		seen[ch.Rule.ID] = true
		if ch.Kind == Upsert && ch.Rule.Threshold < 0 {
			e.reject("publish", ErrInvalidRule,
				"changes[%d] rule %q negative threshold %d",
				i, ch.Rule.ID, ch.Rule.Threshold)
			return e.globalV, ErrInvalidRule
		}
	}

	// Snapshot: copy the previous rule set then apply the batch.
	prev := e.snaps[e.globalV]
	next := make(map[string]int, len(prev)+len(changes))
	for id, t := range prev {
		next[id] = t
	}
	for _, ch := range changes {
		if ch.Kind == Upsert {
			next[ch.Rule.ID] = ch.Rule.Threshold
		} else {
			delete(next, ch.Rule.ID)
		}
	}

	e.globalV++
	e.snaps = append(e.snaps, next)
	e.log("publish", "changes=%d newVersion=%d rules=%d decision=accepted",
		len(changes), e.globalV, len(next))
	return e.globalV, nil
}

// Deliver asks one instance to apply global version. Versions must be
// applied strictly in order. After applying the version, buffered data
// stamped with that version is flushed before returning.
func (e *Engine) Deliver(instance int, version int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if instance < 0 || instance >= e.n {
		e.reject("deliver", ErrUnknownInstance,
			"instance=%d out of range [0,%d)", instance, e.n)
		return ErrUnknownInstance
	}
	// Versions are applied strictly one at a time in ascending order, and
	// cannot exceed what has been published.
	if version != e.applied[instance]+1 || version < 1 || version > e.globalV {
		e.reject("deliver", ErrInvalidVersion,
			"instance=%d from=%d requested=%d published=%d",
			instance, e.applied[instance], version, e.globalV)
		return ErrInvalidVersion
	}

	e.applied[instance] = version
	e.log("deliver", "instance=%d applied=%d bufferBefore=%d decision=accepted",
		instance, version, len(e.buffers[instance]))

	// Apply exactly this version, then flush buffered items tagged with it
	// in arrival order before any later version may be applied.
	e.flushLocked(instance, version)
	return nil
}

// Send routes one datum by key to a single instance. The datum is stamped
// with the current global version and either processed immediately or
// buffered until the instance catches up.
func (e *Engine) Send(key int, value int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if key < 0 {
		e.reject("send", ErrNegativeKey, "key=%d value=%d", key, value)
		return ErrNegativeKey
	}

	instance := key % e.n
	tag := e.globalV

	// If the instance is already effective at the datum's arrival version,
	// process now regardless of buffer occupancy. Otherwise buffer.
	if e.applied[instance] == tag {
		e.log("send", "key=%d value=%d instance=%d tag=%d applied=%d decision=process-now",
			key, value, instance, tag, e.applied[instance])
		e.processLocked(instance, key, value, tag)
		return nil
	}
	if len(e.buffers[instance]) >= e.cap {
		e.reject("send", ErrBufferFull,
			"key=%d instance=%d tag=%d applied=%d buffered=%d cap=%d",
			key, instance, tag, e.applied[instance],
			len(e.buffers[instance]), e.cap)
		return ErrBufferFull
	}
	e.buffers[instance] = append(e.buffers[instance],
		buffered{key: key, value: value, tag: tag})
	e.log("send", "key=%d value=%d instance=%d tag=%d applied=%d decision=buffer buffered=%d",
		key, value, instance, tag, e.applied[instance],
		len(e.buffers[instance]))
	return nil
}

// Hits returns a snapshot copy of all hits in emission order.
func (e *Engine) Hits() []Hit {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Hit, len(e.hits))
	copy(out, e.hits)
	e.log("query-hits", "hits=%d decision=accepted", len(out))
	return out
}

// GlobalVersion returns the latest published global version.
func (e *Engine) GlobalVersion() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.globalV
}

// buffered is one datum waiting for its instance to reach its tag.
type buffered struct {
	key   int
	value int
	tag   int
}

// flushLocked processes, in arrival order, every buffered datum whose tag
// equals the instance's now-effective version. Items tagged for a later
// version stay buffered (they cannot precede it in FIFO order).
func (e *Engine) flushLocked(instance int, version int) {
	buf := e.buffers[instance]
	keep := buf[:0]
	flushed := 0
	for _, d := range buf {
		if d.tag == version {
			e.processLocked(instance, d.key, d.value, d.tag)
			flushed++
		} else {
			keep = append(keep, d)
		}
	}
	e.buffers[instance] = keep
	e.log("flush", "instance=%d version=%d flushed=%d buffered=%d",
		instance, version, flushed, len(keep))
}

// processLocked evaluates one datum against the rule snapshot of the
// stamped version and emits one hit per matching rule, ordered by rule id.
func (e *Engine) processLocked(instance int, key int, value int, version int) {
	rules := e.snaps[version]
	ids := make([]string, 0, len(rules))
	for id, threshold := range rules {
		if value >= threshold {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	e.seq++
	for _, id := range ids {
		e.hits = append(e.hits, Hit{
			Instance: instance,
			Seq:      e.seq,
			Version:  version,
			Key:      key,
			Value:    value,
			RuleID:   id,
		})
	}
	e.log("process", "instance=%d key=%d value=%d version=%d hits=%d rules=%d",
		instance, key, value, version, len(ids), len(rules))
}

func (e *Engine) log(op string, format string, args ...any) {
	if e.logger == nil {
		return
	}
	e.logger.Logf("[%s] %s", op, sprintf(format, args...))
}

// reject logs a rejected operation with its reason and state snapshot.
func (e *Engine) reject(op string, err error, format string, args ...any) {
	if e.logger == nil {
		return
	}
	e.logger.Logf("[%s] DECISION=reject reason=%q state{global=%d} detail: %s",
		op, err.Error(), e.globalV, sprintf(format, args...))
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
