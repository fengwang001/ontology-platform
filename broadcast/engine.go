package broadcast

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// Rule 是一条阈值规则：数据值大于等于 Threshold 时命中。
type Rule struct {
	ID        string
	Threshold int
}

// RuleSet 是一次发布所包含的全量规则快照。
// 新版本未包含的规则即视为删除。
type RuleSet []Rule

// Hit 是一条数据对某条规则的命中输出。
type Hit struct {
	Instance int
	Key      int
	Value    int
	RuleID   string
	Version  int64
	Seq      int64
}

// buffered 是等待实例版本追平的数据。
type buffered struct {
	key     int
	value   int
	version int64
	seq     int64
}

type instance struct {
	version int64
	buffer  []buffered
}

// Engine 管理全局规则版本、多个处理实例的投递与数据路由。
//
// 所有公开方法都可被多个执行体并发调用：单把互斥锁串行化所有
// 状态变更与读取，因此每条数据的判定依据只取决于它到达那一刻
// 全局已发布的规则版本，与各实例投递版本的先后顺序无关。
type Engine struct {
	mu        sync.Mutex
	n         int
	bufferCap int

	globalVersion int64
	nextSeq       int64
	versions      map[int64]RuleSet
	instances     []instance
	hits          []Hit

	logWriter io.Writer
}

// Option 配置引擎。
type Option func(*Engine)

// WithLogger 将每一步输入、版本、缓冲与判定依据写入 w。
func WithLogger(w io.Writer) Option {
	return func(e *Engine) { e.logWriter = w }
}

// NewEngine 创建拥有 n 个处理实例、每实例缓冲容量为 bufferCap 的引擎。
func NewEngine(n int, bufferCap int, opts ...Option) *Engine {
	e := &Engine{
		n:         n,
		bufferCap: bufferCap,
		versions:  make(map[int64]RuleSet),
		instances: make([]instance, n),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Publish 发布一版全量规则快照，全局版本加一，不立即作用于任何实例。
func (e *Engine) Publish(rules RuleSet) (int64, error) {
	if err := validateRules(rules); err != nil {
		e.logf("PUBLISH reject reason=%q rules=%v", err, rules)
		return 0, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	snapshot := make(RuleSet, len(rules))
	copy(snapshot, rules)
	e.globalVersion++
	e.versions[e.globalVersion] = snapshot
	e.logf("PUBLISH ok global=%d rules=%v", e.globalVersion, snapshot)
	return e.globalVersion, nil
}

// Deliver 向指定实例投递版本 v，实例必须按 1、2、3… 的顺序应用。
func (e *Engine) Deliver(instance int, version int64) error {
	if instance < 0 || instance >= e.n {
		err := ErrInstanceOutOfRange
		e.logf("DELIVER reject reason=%q instance=%d version=%d", err, instance, version)
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	current := e.instances[instance].version
	if version != current+1 || version > e.globalVersion {
		err := ErrVersionOutOfRange
		e.logf("DELIVER reject reason=%q instance=%d version=%d want=%d global=%d",
			err, instance, version, current+1, e.globalVersion)
		return err
	}

	e.instances[instance].version = version
	e.drainLocked(instance)
	e.logf("DELIVER ok instance=%d applied=%d buffer=%d hits=%d",
		instance, version, len(e.instances[instance].buffer), len(e.hits))
	return nil
}

// Send 发送一条数据：按 key 路由到单一实例并打上当前全局版本标签。
// 实例已生效版本等于标签时立即处理，否则进入该实例缓冲区。
func (e *Engine) Send(key, value int) ([]Hit, error) {
	if key < 0 {
		err := ErrNegativeKey
		e.logf("SEND reject reason=%q key=%d value=%d", err, key, value)
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	target := key % e.n
	inst := &e.instances[target]
	tag := e.globalVersion

	if inst.version != tag {
		if len(inst.buffer) >= e.bufferCap {
			err := ErrBufferFull
			e.logf("SEND reject reason=%q instance=%d key=%d value=%d tag=%d instVersion=%d buffer=%d/%d",
				err, target, key, value, tag, inst.version, len(inst.buffer), e.bufferCap)
			return nil, err
		}
		e.nextSeq++
		inst.buffer = append(inst.buffer, buffered{
			key: key, value: value, version: tag, seq: e.nextSeq,
		})
		e.logf("SEND buffered instance=%d key=%d value=%d tag=%d instVersion=%d buffer=%d/%d seq=%d",
			target, key, value, tag, inst.version, len(inst.buffer), e.bufferCap, e.nextSeq)
		return nil, nil
	}

	e.nextSeq++
	seq := e.nextSeq
	hits := e.evaluateLocked(target, key, value, tag, seq)
	e.logf("SEND immediate instance=%d key=%d value=%d tag=%d seq=%d hits=%d rules=%v",
		target, key, value, tag, seq, len(hits), e.versions[tag])
	return hits, nil
}

// Hits 返回已有命中的有序快照：同一实例内严格按数据到达顺序，
// 每条数据的多条命中按规则标识排序。
func (e *Engine) Hits() []Hit {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Hit, len(e.hits))
	copy(out, e.hits)
	e.logf("QUERY hits=%d snapshot=%v", len(out), out)
	return out
}

// GlobalVersion 返回当前全局已发布版本。
func (e *Engine) GlobalVersion() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.globalVersion
}

// InstanceVersion 返回指定实例已生效版本。
func (e *Engine) InstanceVersion(instance int) (int64, error) {
	if instance < 0 || instance >= e.n {
		return 0, ErrInstanceOutOfRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.instances[instance].version, nil
}

// drainLocked 在实例应用完一个版本后，按到达顺序处理缓冲中
// 所有标签恰好等于该版本的数据，然后才允许应用下一版本。
func (e *Engine) drainLocked(instance int) {
	inst := &e.instances[instance]
	kept := inst.buffer[:0]
	for _, item := range inst.buffer {
		if item.version == inst.version {
			e.evaluateLocked(instance, item.key, item.value, item.version, item.seq)
		} else {
			kept = append(kept, item)
		}
	}
	inst.buffer = kept
}

// evaluateLocked 按数据标签对应版本的规则快照进行判定，
// 对每条命中的阈值规则按规则标识排序输出一条命中。
func (e *Engine) evaluateLocked(instance, key, value int, version, seq int64) []Hit {
	rules := e.versions[version]

	var matched []Rule
	for _, rule := range rules {
		if value >= rule.Threshold {
			matched = append(matched, rule)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	hits := make([]Hit, 0, len(matched))
	for _, rule := range matched {
		hits = append(hits, Hit{
			Instance: instance,
			Key:      key,
			Value:    value,
			RuleID:   rule.ID,
			Version:  version,
			Seq:      seq,
		})
	}
	e.hits = append(e.hits, hits...)
	return hits
}

func validateRules(rules RuleSet) error {
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if rule.ID == "" {
			return fmt.Errorf("%w: empty id", ErrInvalidRule)
		}
		if _, dup := seen[rule.ID]; dup {
			return fmt.Errorf("%w: duplicate id %q", ErrInvalidRule, rule.ID)
		}
		seen[rule.ID] = struct{}{}
	}
	return nil
}

func (e *Engine) logf(format string, args ...any) {
	if e.logWriter != nil {
		fmt.Fprintf(e.logWriter, format+"\n", args...)
	}
}
