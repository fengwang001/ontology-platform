package broadcast

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Logger 是引擎使用的最小日志接口，*slog.Logger 天然满足。
type Logger interface {
	Info(msg string, args ...any)
}

type datum struct {
	key     int
	value   int
	version int // 到达时的全局版本标签
	seq     int64
}

type instanceState struct {
	applied int // 已生效版本（0 表示尚未生效任何版本）
	buffer  []datum
	hits    []Hit
}

// Engine 是广播状态规则版本化引擎。零值不可用，必须用 NewEngine 构造。
type Engine struct {
	mu        sync.Mutex
	instances int
	bufCap    int
	version   int // 全局已发布版本
	// versions[v] 是版本 v 生效后的完整规则快照；versions[0] 为空规则集。
	versions []map[string]Rule
	states   []*instanceState
	seq      int64
	log      Logger
}

// NewEngine 创建实例数为 instanceCount、每实例缓冲容量为 bufferCapacity
// （必须为正）的引擎，并预置版本 0 的空规则集。
func NewEngine(instanceCount, bufferCapacity int, logger Logger) (*Engine, error) {
	if instanceCount <= 0 || bufferCapacity <= 0 {
		return nil, ErrInvalidInstance
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{
		instances: instanceCount,
		bufCap:    bufferCapacity,
		versions:  []map[string]Rule{{}},
		states:    make([]*instanceState, instanceCount),
		log:       logger,
	}
	for i := range e.states {
		e.states[i] = &instanceState{}
	}
	return e, nil
}

// GlobalVersion 返回当前全局已发布版本。
func (e *Engine) GlobalVersion() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.version
}

// Publish 原子地应用一批规则变更，使全局版本加一。发布本身不作用于任何
// 实例，必须随后通过 Deliver 投递才会生效。任一条变更非法则整体拒绝，
// 不留任何状态痕迹。
func (e *Engine) Publish(changes []Change) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := validateChanges(changes); err != nil {
		e.logReject(opPublish, err,
			"changes", len(changes), "globalVersion", e.version)
		return e.version, err
	}
	prev := e.versions[e.version]
	next := make(map[string]Rule, len(prev)+len(changes))
	for id, rule := range prev {
		next[id] = rule
	}
	for _, change := range changes {
		switch change.Op {
		case OpUpsert:
			next[change.Rule.ID] = change.Rule
		case OpDelete:
			delete(next, change.Rule.ID)
		}
	}
	e.versions = append(e.versions, next)
	e.version++
	e.log.Info("broadcast publish accepted",
		"op", opPublish.String(),
		"changes", len(changes),
		"publishedVersion", e.version,
		"ruleCount", len(next),
		"decision", "publish advances global version only; no instance affected")
	return e.version, nil
}

// Deliver 向实例 instance 投递下一个版本：实例按版本顺序应用该版本，
// 随后立即按到达顺序处理缓冲区中标签等于新版本的数据，然后才允许投递
// 再下一个版本。越界（没有可投递的版本）返回 ErrNoVersionToApply。
func (e *Engine) Deliver(instance int) (appliedVersion int, processed int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if instance < 0 || instance >= e.instances {
		e.logReject(opDeliver, ErrInvalidInstance,
			"instance", instance, "globalVersion", e.version)
		return 0, 0, ErrInvalidInstance
	}
	st := e.states[instance]
	if st.applied >= e.version {
		e.logReject(opDeliver, ErrNoVersionToApply,
			"instance", instance,
			"appliedVersion", st.applied,
			"globalVersion", e.version,
			"bufferLen", len(st.buffer))
		return st.applied, 0, ErrNoVersionToApply
	}
	st.applied++
	appliedVersion = st.applied
	flushed := e.flush(st)
	e.log.Info("broadcast deliver accepted",
		"op", opDeliver.String(),
		"instance", instance,
		"appliedVersion", appliedVersion,
		"globalVersion", e.version,
		"bufferLenBefore", flushed.before,
		"flushed", flushed.count,
		"bufferLenAfter", len(st.buffer),
		"decision",
		"applied next version then drained buffer entries tagged with that version")
	return appliedVersion, flushed.count, nil
}

// Send 发送一条数据：以当前全局版本打标签，并按 key 稳定路由到单一实例。
// 若该实例已生效版本等于标签则立即处理，否则进入该实例缓冲区；缓冲区
// 已满则整体拒绝（ErrBufferFull），不留痕。返回标签版本与路由到的实例。
func (e *Engine) Send(key, value int) (version int, instance int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if key < 0 {
		e.logReject(opSend, ErrNegativeKey,
			"key", key, "value", value, "globalVersion", e.version)
		return e.version, 0, ErrNegativeKey
	}
	instance = e.routeInstance(key)
	st := e.states[instance]
	tag := e.version
	if tag != st.applied && len(st.buffer) >= e.bufCap {
		e.logReject(opSend, ErrBufferFull,
			"key", key, "value", value,
			"tagVersion", tag,
			"instance", instance,
			"appliedVersion", st.applied,
			"bufferLen", len(st.buffer))
		return tag, instance, ErrBufferFull
	}
	e.seq++
	d := datum{key: key, value: value, version: tag, seq: e.seq}
	decision := ""
	if st.applied == tag {
		e.processNow(st, d)
		decision = "applied version equals tag: processed immediately"
	} else {
		st.buffer = append(st.buffer, d)
		decision = "applied version differs from tag: buffered in arrival order"
	}
	e.log.Info("broadcast send accepted",
		"op", opSend.String(),
		"seq", d.seq,
		"key", key,
		"value", value,
		"tagVersion", tag,
		"instance", instance,
		"appliedVersion", st.applied,
		"bufferLen", len(st.buffer),
		"totalHits", len(st.hits),
		"decision", decision)
	return tag, instance, nil
}

// Hits 返回指定实例上已有命中的副本，顺序即为该实例的处理顺序。
func (e *Engine) Hits(instance int) ([]Hit, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if instance < 0 || instance >= e.instances {
		e.logReject(opQuery, ErrInvalidInstance,
			"instance", instance, "globalVersion", e.version)
		return nil, ErrInvalidInstance
	}
	st := e.states[instance]
	out := append([]Hit(nil), st.hits...)
	e.log.Info("broadcast hits query",
		"op", opQuery.String(),
		"instance", instance,
		"appliedVersion", st.applied,
		"globalVersion", e.version,
		"bufferLen", len(st.buffer),
		"returnedHits", len(out),
		"decision", "snapshot copy of per-instance hits in processing order")
	return out, nil
}

// AppliedVersion 返回指定实例当前已生效的版本。
func (e *Engine) AppliedVersion(instance int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if instance < 0 || instance >= e.instances {
		return 0, ErrInvalidInstance
	}
	return e.states[instance].applied, nil
}

// BufferLen 返回指定实例当前缓冲区中的数据条数。
func (e *Engine) BufferLen(instance int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if instance < 0 || instance >= e.instances {
		return 0, ErrInvalidInstance
	}
	return len(e.states[instance].buffer), nil
}

// AllHits 返回各实例命中副本的快照（按下标实例号排列）。
func (e *Engine) AllHits() [][]Hit {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]Hit, e.instances)
	for i, st := range e.states {
		out[i] = append([]Hit(nil), st.hits...)
	}
	return out
}

// ---- 内部实现（调用时必须持有 e.mu）----

func validateChanges(changes []Change) error {
	if len(changes) == 0 {
		return ErrInvalidPublish
	}
	for _, change := range changes {
		switch change.Op {
		case OpUpsert:
			if change.Rule.ID == "" {
				return fmt.Errorf("%w: empty rule id", ErrInvalidRule)
			}
		case OpDelete:
			if change.Rule.ID == "" {
				return fmt.Errorf("%w: empty rule id on delete", ErrInvalidRule)
			}
		default:
			return fmt.Errorf("%w: unknown op %d", ErrInvalidRule, change.Op)
		}
	}
	return nil
}

func (e *Engine) routeInstance(key int) int {
	return key % e.instances
}

func (e *Engine) processNow(st *instanceState, d datum) {
	rules := e.versions[d.version]
	ids := e.ruleIDs(rules)
	for _, id := range ids {
		if rule := rules[id]; d.value >= rule.Threshold {
			st.hits = append(st.hits, Hit{
				Instance: e.routeInstance(d.key),
				Key:      d.key,
				Value:    d.value,
				Version:  d.version,
				RuleID:   id,
				Seq:      d.seq,
			})
		}
	}
}

type flushResult struct {
	before int
	count  int
}

// flush 按到达顺序处理缓冲区中标签等于实例当前已生效版本的数据；
// 标签更大的数据继续保留。调用时必须持有 e.mu。
func (e *Engine) flush(st *instanceState) flushResult {
	kept := st.buffer[:0]
	res := flushResult{before: len(st.buffer)}
	for _, d := range st.buffer {
		if d.version == st.applied {
			e.processNow(st, d)
			res.count++
		} else {
			kept = append(kept, d)
		}
	}
	st.buffer = kept
	return res
}

func (e *Engine) ruleIDs(rules map[string]Rule) []string {
	ids := make([]string, 0, len(rules))
	for id := range rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (e *Engine) logReject(kind opKind, cause error, args ...any) {
	args = append(args, "op", kind.String(), "rejected", true, "reason", cause.Error())
	e.log.Info("broadcast operation rejected", args...)
}
