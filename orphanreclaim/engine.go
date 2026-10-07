package orphanreclaim

import (
	"errors"
	"sync"
)

// 四类互斥哨兵错误。
var (
	ErrObjectNotFound       = errors.New("orphanreclaim: target object instance does not exist")
	ErrLinkTypeUnconfigured = errors.New("orphanreclaim: link type has no retention contribution configured")
	ErrJointRefUndefined    = errors.New("orphanreclaim: joint retention group references an undefined link type")
	ErrInvalidGracePeriod   = errors.New("orphanreclaim: grace period must be a positive duration")
)

// Generation 表示对象当前所处的待回收代。
type Generation int

const (
	GenNone Generation = 0
	Gen1    Generation = 1
	Gen2    Generation = 2
)

// CascadePolicy 在对象被真正清理后收到其出边快照通知（级联删除的既有规则由外部掌握）。
// 通知发生在引擎锁仍持有、对象删除已不可分割完成之后；实现不得回调引擎方法。
type CascadePolicy interface {
	OnPurge(obj string, outLinks map[string]map[string]struct{}, at int64)
}

// Reclaimer 是分代孤儿回收子系统的核心引擎。
// 所有变更操作经由单一互斥量串行化，因此对外等价于按某个全局顺序依次执行。
type Reclaimer struct {
	mu      sync.Mutex // 唯一串行化点：所有读写均在锁内，对外等价于全局全序
	cfg     Config
	norm    normalizedConfig
	objects map[string]*objectState
	gen1    *decisionDeadlineHeap
	gen2    *decisionDeadlineHeap
	clock   Clock
	cascade CascadePolicy
	logger  Logger
	seq     uint64 // 全局操作序号，即「等价于某个全局顺序」中次序的凭据

	// 复杂度探针：记录历次判定核对的类型桶数。
	// 不变量：每个值只取决于配置规模（独立类型数 + 联合组长度之和），与入边总数无关。
	lastBucketsChecked int
}

// New 构造引擎；配置非法时按固定次序返回第一类配置错误。
func New(cfg Config, clock Clock, cascade CascadePolicy, logger Logger) (*Reclaimer, error) {
	norm, err := validateNormalize(cfg)
	if err != nil {
		return nil, err
	}
	if clock == nil {
		clock = SystemClock()
	}
	if logger == nil {
		logger = nopLogger{}
	}
	return &Reclaimer{
		cfg:     cfg,
		norm:    norm,
		objects: map[string]*objectState{},
		gen1:    newDeadlineHeap(),
		gen2:    newDeadlineHeap(),
		clock:   clock,
		cascade: cascade,
		logger:  logger,
	}, nil
}

// CreateObject 登记一个对象实例。
func (r *Reclaimer) CreateObject(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.objects[id]; ok {
		return
	}
	r.objects[id] = newObjectState(id)
}

// AddLink 新增一条 source -> target 的指定类型链接，并立即重评 target 的孤儿身份。
func (r *Reclaimer) AddLink(source, target, linkType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tgt, err := r.validateMutation(target, linkType)
	if err != nil {
		return err
	}
	if _, ok := r.objects[source]; !ok {
		return ErrObjectNotFound // (1)：链接两端对象都必须存在
	}
	r.seq++
	seq := r.seq

	tgt.addInEdge(source, linkType)
	if out, ok := r.objects[source]; ok {
		addTypeSet(out.outLinks, target, linkType)
	}
	r.reassess(tgt, "add_link", source, linkType, seq)
	return nil
}

// RemoveLink 删除一条链接，并立即重评 target 的孤儿身份。
func (r *Reclaimer) RemoveLink(source, target, linkType string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tgt, err := r.validateMutation(target, linkType)
	if err != nil {
		return err
	}
	r.seq++
	seq := r.seq

	tgt.removeInEdge(source, linkType)
	if src, ok := r.objects[source]; ok {
		removeTypeSet(src.outLinks, target, linkType)
	}
	r.reassess(tgt, "remove_link", source, linkType, seq)
	return nil
}

// Advance 执行代际推进扫描：到期对象 Gen1 -> Gen2，Gen2 -> 真正清理。
func (r *Reclaimer) Advance() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advanceLocked()
}

func (r *Reclaimer) advanceLocked() AdvanceRecord {
	r.seq++
	now := r.clock()
	rec := AdvanceRecord{Seq: r.seq, At: now, Rechecked: map[string]string{}}

	// 先处理第二代（清理优先），再提升第一代：同一对象不可能在同一次扫描中
	// 先被提升又被清理，且每次出堆都带「锁内即时重评」，杜绝读取过期判定。
	for {
		o := r.gen2.popDue(now)
		if o == nil {
			break
		}
		v := r.evaluate(o)
		r.lastBucketsChecked = v.bucketsChecked
		if !v.orphan {
			// 到期重评时已不再是孤儿：直接退回非孤儿，不回第一代、不留代际记忆。
			r.rescueWithVerdictLocked(o, v, r.seq, now, rec.Rechecked)
			continue
		}
		r.purgeLocked(o, now)
		rec.Purged = append(rec.Purged, o.id)
	}

	for {
		o := r.gen1.popDue(now)
		if o == nil {
			break
		}
		v := r.evaluate(o)
		r.lastBucketsChecked = v.bucketsChecked
		if !v.orphan {
			r.rescueWithVerdictLocked(o, v, r.seq, now, rec.Rechecked)
			continue
		}
		o.gen = Gen2
		o.since = now
		o.deadline = now + r.norm.graceGen2
		r.gen2.push(o)
		rec.Promoted = append(rec.Promoted, o.id)
	}

	if len(rec.Purged) > 0 || len(rec.Promoted) > 0 || len(rec.Rechecked) > 0 {
		r.logger.LogAdvance(rec)
	}
	return rec
}

// reassess 是链接变更后唯一的身份状态机入口；进入与脱离待回收共用同一 evaluate。
func (r *Reclaimer) reassess(o *objectState, trigger, source, linkType string, seq uint64) {
	now := r.clock()
	v := r.evaluate(o)
	r.lastBucketsChecked = v.bucketsChecked

	prev := o.gen
	outcome := "already_non_orphan"
	switch {
	case !v.orphan && prev != GenNone:
		r.rescueLocked(o)
		outcome = "rescued"
	case v.orphan && prev == GenNone:
		// 首次判定为孤儿：从第一代起算并记录判定时刻。
		o.gen = Gen1
		o.since = now
		o.deadline = now + r.norm.graceGen1
		r.gen1.push(o)
		outcome = "queued_gen1"
	case v.orphan:
		// 仍为孤儿且已在某代队列中：不重置计时（计时只由入队时刻决定）。
		if prev == Gen1 {
			outcome = "stayed_gen1"
		} else {
			outcome = "stayed_gen2"
		}
	}

	r.logger.LogDecision(DecisionRecord{
		Seq:            seq,
		At:             now,
		Object:         o.id,
		Trigger:        trigger,
		LinkType:       linkType,
		Source:         source,
		InCounts:       copyCounts(o.inCounts),
		Basis:          v.basis(),
		Orphan:         v.orphan,
		PreviousGen:    prev,
		Outcome:        outcome,
		BucketsChecked: v.bucketsChecked,
	})
}

// rescueLocked 把对象从当前所在代队列移除并清空全部待回收记录。
// 第一代与第二代走同一路径：第二代救回直接变非孤儿，不留任何代际记忆。
// 返回救回前所处的代。
func (r *Reclaimer) rescueLocked(o *objectState) Generation {
	prev := o.gen
	switch o.gen {
	case Gen1:
		r.gen1.remove(o.id)
	case Gen2:
		r.gen2.remove(o.id)
	}
	o.gen = GenNone
	o.since = 0
	o.deadline = 0
	return prev
}

// rescueWithVerdictLocked 用于到期扫描的锁内重评：判定已在调用处完成，
// 直接复用其证据记录一次救回日志，保证时刻与输入不被二次调用污染。
func (r *Reclaimer) rescueWithVerdictLocked(o *objectState, v verdict, seq uint64, at int64,
	rechecked map[string]string) Generation {
	prev := r.rescueLocked(o)
	rechecked[o.id] = "rescued"
	r.logger.LogDecision(DecisionRecord{
		Seq:            seq,
		At:             at,
		Object:         o.id,
		Trigger:        "advance_recheck",
		InCounts:       copyCounts(o.inCounts),
		Basis:          v.basis(),
		Orphan:         false,
		PreviousGen:    prev,
		Outcome:        "rescued",
		BucketsChecked: v.bucketsChecked,
	})
	return prev
}

// purgeLocked 在锁内原子完成：摘除全部出边、移除对象与队列记录。
// 级联策略只在删除已不可分割地完成后收到快照通知，规则本体由外部既有系统掌握。
func (r *Reclaimer) purgeLocked(o *objectState, at int64) {
	snapshot := map[string]map[string]struct{}{}
	var affected []*objectState
	for tgt, types := range o.outLinks {
		snapshot[tgt] = copyTypeSet(types)
		if t, ok := r.objects[tgt]; ok {
			affected = append(affected, t)
			for lt := range types {
				t.removeInEdge(o.id, lt)
			}
		}
	}
	delete(r.objects, o.id)
	o.gen = GenNone
	if r.cascade != nil {
		r.cascade.OnPurge(o.id, snapshot, at)
	}
	// 出边摘除是链接删除的一种：受影响目标须按同一套规则立即重评；
	// 全部发生在同一把锁内，与对象本体删除共同构成原子清理。
	for _, t := range affected {
		if _, stillAlive := r.objects[t.id]; stillAlive {
			r.reassess(t, "remove_link", o.id, "", r.seq)
		}
	}
}

// IsOrphan 报告对象当前是否被判定为孤儿（不改变队列状态）。
func (r *Reclaimer) IsOrphan(id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.objects[id]
	if !ok {
		return false, ErrObjectNotFound
	}
	r.seq++
	v := r.evaluate(o)
	r.lastBucketsChecked = v.bucketsChecked
	r.logger.LogDecision(DecisionRecord{
		Seq:            r.seq,
		At:             r.clock(),
		Object:         id,
		Trigger:        "query",
		InCounts:       copyCounts(o.inCounts),
		Basis:          v.basis(),
		Orphan:         v.orphan,
		PreviousGen:    o.gen,
		Outcome:        "query",
		BucketsChecked: v.bucketsChecked,
	})
	return v.orphan, nil
}

// GenerationOf 返回对象当前所处待回收代。
func (r *Reclaimer) GenerationOf(id string) (Generation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.objects[id]
	if !ok {
		return GenNone, ErrObjectNotFound
	}
	return o.gen, nil
}

// LastBucketsChecked 返回最近一次判定核对的类型桶数（复杂度探针）。
func (r *Reclaimer) LastBucketsChecked() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastBucketsChecked
}

func addTypeSet(m map[string]map[string]struct{}, key, val string) {
	s, ok := m[key]
	if !ok {
		s = map[string]struct{}{}
		m[key] = s
	}
	s[val] = struct{}{}
}

func removeTypeSet(m map[string]map[string]struct{}, key, val string) {
	s, ok := m[key]
	if !ok {
		return
	}
	delete(s, val)
	if len(s) == 0 {
		delete(m, key)
	}
}

func copyCounts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyTypeSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}
