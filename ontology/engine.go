package ontology

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

const (
	// DefaultShards 是按键路由的默认处理实例数。
	DefaultShards = 4
	// DefaultBufferSize 是每个实例版本缓冲区的默认容量。
	DefaultBufferSize = 16
)

// RuleOp 表示一次规则变更的操作类型。
type RuleOp int

const (
	RuleUpsert RuleOp = iota
	RuleDelete
)

// RuleChange 是一次规则发布（一个版本）内对单条规则的变更。
type RuleChange struct {
	Op    RuleOp
	ID    string
	Min   int
	Max   int
	Value int
}

// Datum 是到达待处理的数据。
type Datum struct {
	Key   int
	Value int
}

// Hit 是一条命中结果。
type Hit struct {
	RuleID string
	Key    int
	Value  int
}

// rule 是实例当前已生效规则集中的一条阈值规则。
type rule struct {
	min   int
	max   int
	value int
}

// snapshot 是某个已发布版本的完整规则集，供尚未生效的实例追赶。
type snapshot struct {
	rules map[string]rule
}

// bufferedDatum 是等待实例版本追平标签后处理的数据。
type bufferedDatum struct {
	seq     int64
	version int
	key     int
	value   int
}

// instance 是一个处理实例的本地状态。
type instance struct {
	version int
	rules   map[string]rule
	buffer  []bufferedDatum
}

// Engine 是广播状态模式下的规则版本化处理器。
//
// 发布只推进全局版本并生成快照，不会立即作用于任何实例；
// Deliver 让指定实例按版本顺序逐个应用版本，并在每次应用后
// 按到达顺序刷出缓冲中标签等于新版本的数据。
type Engine struct {
	mu         sync.Mutex
	shards     int
	bufferSize int
	globalVer  int
	snapshots  []snapshot // snapshots[v-1] 为版本 v 的规则集；版本 0 为空规则集
	liveRules  map[string]rule
	instances  []*instance
	seq        int64
	hits       []Hit
	logWriter  io.Writer
}

// NewEngine 创建处理器。
func NewEngine() *Engine {
	return NewEngineWith(DefaultShards, DefaultBufferSize)
}

// NewEngineWith 创建处理器，并指定实例数量与每实例缓冲容量。
func NewEngineWith(shards, bufferSize int) *Engine {
	insts := make([]*instance, shards)
	for i := range insts {
		insts[i] = &instance{rules: map[string]rule{}}
	}
	return &Engine{
		shards:     shards,
		bufferSize: bufferSize,
		liveRules:  map[string]rule{},
		instances:  insts,
	}
}

// SetLogger 设置逐步日志输出。
func (e *Engine) SetLogger(w io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logWriter = w
}

// Publish 发布一批规则变更，使全局版本加一。
// 发布在任何状态变更之前完成整批校验：非法变更、批次内重复标识、
// 删除不存在的规则都会以 ErrInvalidRule 拒绝，且不留任何痕迹。
func (e *Engine) Publish(changes []RuleChange) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	seen := make(map[string]bool, len(changes))
	for _, ch := range changes {
		if ch.ID == "" {
			e.logf("PUBLISH REJECT invalid-rule: empty rule id; globalVersion=%d (unchanged)", e.globalVer)
			return 0, fmt.Errorf("%w: empty rule id", ErrInvalidRule)
		}
		if seen[ch.ID] {
			e.logf("PUBLISH REJECT invalid-rule: duplicate rule id %q in batch; globalVersion=%d (unchanged)", ch.ID, e.globalVer)
			return 0, fmt.Errorf("%w: duplicate rule id %q in batch", ErrInvalidRule, ch.ID)
		}
		seen[ch.ID] = true
		switch ch.Op {
		case RuleUpsert:
			if ch.Min > ch.Max {
				e.logf("PUBLISH REJECT invalid-rule: rule %q min %d > max %d; globalVersion=%d (unchanged)", ch.ID, ch.Min, ch.Max, e.globalVer)
				return 0, fmt.Errorf("%w: rule %q min %d > max %d", ErrInvalidRule, ch.ID, ch.Min, ch.Max)
			}
		case RuleDelete:
			if _, ok := e.liveRules[ch.ID]; !ok {
				e.logf("PUBLISH REJECT invalid-rule: delete missing rule %q; globalVersion=%d (unchanged)", ch.ID, e.globalVer)
				return 0, fmt.Errorf("%w: rule %q does not exist", ErrInvalidRule, ch.ID)
			}
		default:
			e.logf("PUBLISH REJECT invalid-rule: unknown op %d for rule %q; globalVersion=%d (unchanged)", ch.Op, ch.ID, e.globalVer)
			return 0, fmt.Errorf("%w: unknown op %d", ErrInvalidRule, ch.Op)
		}
	}

	next := make(map[string]rule, len(e.liveRules)+len(changes))
	for id, r := range e.liveRules {
		next[id] = r
	}
	for _, ch := range changes {
		if ch.Op == RuleDelete {
			delete(next, ch.ID)
		} else {
			next[ch.ID] = rule{min: ch.Min, max: ch.Max, value: ch.Value}
		}
	}

	e.globalVer++
	e.liveRules = next
	e.snapshots = append(e.snapshots, snapshot{rules: next})
	e.logf("PUBLISH OK version=%d changes=%d globalVersion=%d ruleSetSize=%d", e.globalVer, len(changes), e.globalVer, len(next))
	return e.globalVer, nil
}

// Deliver 让指定实例按版本顺序应用下一个版本。
// 应用完成后立即按到达顺序处理缓冲中标签等于新版本的数据，
// 之后才允许应用再下一个版本。
func (e *Engine) Deliver(instance int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if instance < 0 {
		e.logf("DELIVER REJECT out-of-range: instance=%d < 0", instance)
		return 0, fmt.Errorf("%w: instance %d is negative", ErrDeliveryOutOfRange, instance)
	}
	if instance >= e.shards {
		e.logf("DELIVER REJECT out-of-range: instance=%d >= shards=%d", instance, e.shards)
		return 0, fmt.Errorf("%w: instance %d >= shards %d", ErrDeliveryOutOfRange, instance, e.shards)
	}

	inst := e.instances[instance]
	if inst.version >= e.globalVer {
		e.logf("DELIVER REJECT no-pending: instance=%d version=%d globalVersion=%d", instance, inst.version, e.globalVer)
		return 0, fmt.Errorf("%w: instance %d already at version %d", ErrNoPendingVersion, instance, inst.version)
	}

	applied := inst.version + 1
	snap := e.snapshots[applied-1].rules
	rules := make(map[string]rule, len(snap))
	for id, r := range snap {
		rules[id] = r
	}
	inst.rules = rules
	inst.version = applied
	e.logf("DELIVER OK instance=%d appliedVersion=%d ruleSetSize=%d buffer=%d -> flush", instance, applied, len(rules), len(inst.buffer))

	e.flushLocked(instance, applied)
	return applied, nil
}

// Send 发送一条数据，按键路由到固定实例。
// 数据到达时打上当前全局版本标签：若目标实例已生效版本等于标签
// 则立即处理，否则进入该实例缓冲区。
func (e *Engine) Send(d Datum) (int, int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if d.Key < 0 {
		e.logf("SEND REJECT negative-key: key=%d; globalVersion=%d (unchanged)", d.Key, e.globalVer)
		return 0, 0, fmt.Errorf("%w: key %d", ErrNegativeKey, d.Key)
	}

	e.seq++
	target := d.Key % e.shards
	inst := e.instances[target]
	tag := e.globalVer

	if inst.version == tag {
		hits := e.evaluateLocked(inst, d)
		e.logf("SEND OK seq=%d key=%d value=%d -> instance=%d tag=%d instanceVersion=%d immediate=true hits=%d ruleSetSize=%d",
			e.seq, d.Key, d.Value, target, tag, inst.version, len(hits), len(inst.rules))
		return target, tag, nil
	}

	if len(inst.buffer) >= e.bufferSize {
		e.logf("SEND REJECT buffer-full: seq=%d key=%d -> instance=%d tag=%d instanceVersion=%d buffer=%d capacity=%d (no data retained)",
			e.seq, d.Key, target, tag, inst.version, len(inst.buffer), e.bufferSize)
		return 0, 0, fmt.Errorf("%w: instance %d buffer has capacity %d", ErrBufferFull, target, e.bufferSize)
	}

	inst.buffer = append(inst.buffer, bufferedDatum{
		seq: e.seq, version: tag, key: d.Key, value: d.Value,
	})
	e.logf("SEND OK seq=%d key=%d value=%d -> instance=%d tag=%d instanceVersion=%d immediate=false buffered buffer=%d/%d",
		e.seq, d.Key, d.Value, target, tag, inst.version, len(inst.buffer), e.bufferSize)
	return target, tag, nil
}

// Hits 返回截至目前已产生的命中副本。
func (e *Engine) Hits() []Hit {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Hit, len(e.hits))
	copy(out, e.hits)
	e.logf("QUERY hits=%d globalVersion=%d", len(out), e.globalVer)
	return out
}

// GlobalVersion 返回当前全局版本。
func (e *Engine) GlobalVersion() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.globalVer
}

// InstanceVersion 返回指定实例的已生效版本。
func (e *Engine) InstanceVersion(instance int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if instance < 0 || instance >= e.shards {
		return 0, fmt.Errorf("%w: instance %d, shards %d", ErrDeliveryOutOfRange, instance, e.shards)
	}
	return e.instances[instance].version, nil
}

// BufferLen 返回指定实例缓冲区中等待刷出的数据条数。
func (e *Engine) BufferLen(instance int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if instance < 0 || instance >= e.shards {
		return 0, fmt.Errorf("%w: instance %d, shards %d", ErrDeliveryOutOfRange, instance, e.shards)
	}
	return len(e.instances[instance].buffer), nil
}

// flushLocked 按到达顺序处理缓冲中标签等于 version 的数据。
// 不匹配的数据（更早或更晚标签）保留在缓冲区中，顺序不变。
func (e *Engine) flushLocked(instance, version int) {
	inst := e.instances[instance]
	kept := inst.buffer[:0]
	flushed := 0
	for _, bd := range inst.buffer {
		if bd.version != version {
			kept = append(kept, bd)
			continue
		}
		hits := e.evaluateLocked(inst, Datum{Key: bd.key, Value: bd.value})
		flushed++
		e.logf("FLUSH instance=%d seq=%d key=%d value=%d tag=%d hits=%d bufferLeft=%d",
			instance, bd.seq, bd.key, bd.value, bd.version, len(hits), len(inst.buffer)-flushed)
	}
	inst.buffer = kept
	e.logf("FLUSH DONE instance=%d version=%d flushed=%d buffer=%d", instance, version, flushed, len(inst.buffer))
}

// evaluateLocked 用实例当前规则集处理一条数据，对每条命中阈值的规则
// 按规则标识排序输出命中。
func (e *Engine) evaluateLocked(inst *instance, d Datum) []Hit {
	ids := make([]string, 0, len(inst.rules))
	for id, r := range inst.rules {
		if d.Value >= r.min && d.Value <= r.max {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	hits := make([]Hit, 0, len(ids))
	for _, id := range ids {
		hit := Hit{RuleID: id, Key: d.Key, Value: inst.rules[id].value}
		hits = append(hits, hit)
		e.hits = append(e.hits, hit)
	}
	return hits
}

func (e *Engine) logf(format string, args ...any) {
	if e.logWriter != nil {
		fmt.Fprintf(e.logWriter, format+"\n", args...)
	}
}
