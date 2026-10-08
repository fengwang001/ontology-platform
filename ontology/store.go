package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Failpoint 是迁移过程的故障注入点，仅用于测试验证迁移原子性。
type Failpoint int

const (
	FailNone Failpoint = iota
	// FailAfterValidation 在校验通过、提交之前注入失败。
	FailAfterValidation
	// FailBeforeCommit 在新版本列表构建完成、原子交换之前注入失败。
	FailBeforeCommit
)

// ErrInjectedFault 是故障注入触发时返回的错误。
var ErrInjectedFault = errors.New("injected fault")

// correctionList 是同一有效时间上按记录时间单调不减排列的修正轨迹。
// 由于写入时强制记录时间单调不倒退，追加即保持有序。
type correctionList struct {
	facts []Fact
}

// latestAtOrBefore 二分查找记录时刻 <= rt 的最新一条修正，不存在时返回 false。
// probes 非空时对每次比较计数，用于独立验证展开开销。
func (c *correctionList) latestAtOrBefore(rt RecordTime, probes *int64) (Fact, bool) {
	i := sort.Search(len(c.facts), func(i int) bool {
		if probes != nil {
			*probes++
		}
		return c.facts[i].RecordTime > rt
	})
	if i == 0 {
		return Fact{}, false
	}
	return c.facts[i-1], true
}

// objectHistory 是单个对象的双时态历史。
// validTimes 为升序的有效时间索引，byValid 为每个有效时间的修正轨迹。
// 展开开销为 O(log V + K log C)，与历史事实总量无关。
type objectHistory struct {
	typeID      string
	validTimes  []ValidTime
	byValid     map[ValidTime]*correctionList
	lastRecord  RecordTime
	firstRecord RecordTime
	hasFact     bool
}

func newObjectHistory(typeID string) *objectHistory {
	return &objectHistory{typeID: typeID, byValid: make(map[ValidTime]*correctionList)}
}

// firstRecordTime 返回该对象首条事实的记录时刻。记录时间单调不倒退，
// 首条写入的事实即最小记录时刻，O(1) 读取。
func (h *objectHistory) firstRecordTime() RecordTime {
	return h.firstRecord
}

// objectTypeState 是对象类型的属性定义版本序列（按 From 升序、互不重叠）。
type objectTypeState struct {
	versions []SchemaVersion
	nextID   int64
	// allPropsCache 缓存跨版本属性名全集，迁移时失效。
	// 避免每次展开都线性扫描全部版本。
	allPropsCache []string
}

// versionAt 二分定位记录时刻 rt 落入的版本。
// 边界取等规则：rt == From 时归属该版本（左闭右开，取右侧新版本）。
// probes 非空时对每次比较计数。
func (s *objectTypeState) versionAt(rt RecordTime, probes *int64) (SchemaVersion, bool) {
	i := sort.Search(len(s.versions), func(i int) bool {
		if probes != nil {
			*probes++
		}
		return s.versions[i].From > rt
	})
	if i == 0 {
		return SchemaVersion{}, false
	}
	v := s.versions[i-1]
	if !v.Contains(rt) {
		return SchemaVersion{}, false
	}
	return v, true
}

func (s *objectTypeState) findVersion(id int64) (SchemaVersion, bool) {
	for _, v := range s.versions {
		if v.ID == id {
			return v, true
		}
	}
	return SchemaVersion{}, false
}

// Store 是本体子系统的并发安全存储。
// 所有操作在单一互斥锁下原子完成，因此任意并发操作集合的最终
// 可观察结果等价于按锁获取顺序串行执行的结果（可串行化）；
// 该顺序不要求与操作到达顺序一致。
type Store struct {
	mu            sync.RWMutex
	types         map[string]*objectTypeState
	objects       map[string]*objectHistory
	failpoint     Failpoint
	auditMu       sync.Mutex
	audit         []DecisionRecord
	factProbes    atomic.Int64
	versionProbes atomic.Int64
}

// Stats 是展开路径的探针计数，用于独立验证展开开销
// 不随历史事实总量或迁移总次数线性增长。
type Stats struct {
	FactProbes    int64 // 修正轨迹二分查找的比较次数
	VersionProbes int64 // 版本序列二分查找的比较次数
}

// Stats 返回累计探针计数。
func (s *Store) Stats() Stats {
	return Stats{FactProbes: s.factProbes.Load(), VersionProbes: s.versionProbes.Load()}
}

// ResetStats 清零探针计数。
func (s *Store) ResetStats() {
	s.factProbes.Store(0)
	s.versionProbes.Store(0)
}

func NewStore() *Store {
	return &Store{
		types:   make(map[string]*objectTypeState),
		objects: make(map[string]*objectHistory),
	}
}

// SetFailpoint 设置迁移故障注入点（仅测试使用）。
func (s *Store) SetFailpoint(fp Failpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failpoint = fp
}

// SchemaVersions 返回对象类型当前有效的属性定义版本序列副本，
// 供调用方独立验证展开所依据的版本信息。
func (s *Store) SchemaVersions(typeID string) []SchemaVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ts, ok := s.types[typeID]
	if !ok {
		return nil
	}
	out := make([]SchemaVersion, len(ts.versions))
	copy(out, ts.versions)
	return out
}

// CreateObjectType 以初始属性定义创建对象类型，初始版本自 effectiveFrom（含）起生效。
func (s *Store) CreateObjectType(typeID string, props map[string]PropertyDef, effectiveFrom RecordTime) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.types[typeID]; exists {
		return fmt.Errorf("object type %q already exists", typeID)
	}
	st := &objectTypeState{
		versions: []SchemaVersion{{
			ID:    1,
			Props: cloneProps(props),
			From:  effectiveFrom,
			To:    openEnd,
		}},
		nextID: 2,
	}
	st.allPropsCache = unionPropNames(st.versions)
	s.types[typeID] = st
	return nil
}

// RegisterObject 注册对象实例并绑定对象类型。
func (s *Store) RegisterObject(typeID, objectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.types[typeID]; !ok {
		return fmt.Errorf("unknown object type %q", typeID)
	}
	if _, exists := s.objects[objectID]; exists {
		return fmt.Errorf("object %q already registered", objectID)
	}
	s.objects[objectID] = newObjectHistory(typeID)
	return nil
}

// WriteFact 写入一条历史事实。记录时间对该对象必须单调不倒退；
// 取值必须能被写入时刻生效的属性定义版本接受（可强制转换）。
func (s *Store) WriteFact(f Fact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeFactLocked(f)
}

func (s *Store) writeFactLocked(f Fact) error {
	h, ok := s.objects[f.ObjectID]
	if !ok {
		return fmt.Errorf("unknown object %q", f.ObjectID)
	}
	if h.hasFact && f.RecordTime < h.lastRecord {
		return fmt.Errorf("record time %d goes backward (last %d)", f.RecordTime, h.lastRecord)
	}
	ts := s.types[h.typeID]
	sv, ok := ts.versionAt(f.RecordTime, nil)
	if !ok {
		return fmt.Errorf("no schema version effective at record time %d", f.RecordTime)
	}
	coerced := make(map[string]Value, len(f.Values))
	for name, v := range f.Values {
		pd, known := sv.Props[name]
		if !known {
			return fmt.Errorf("property %q not defined in schema version %d", name, sv.ID)
		}
		cv, ok := v.CoerceTo(pd.Type)
		if !ok {
			return fmt.Errorf("value of property %q not coercible to %s", name, pd.Type)
		}
		coerced[name] = cv
	}
	// 注意：写入不强制必填属性在场。历史事实允许遗漏取值，
	// 展开时以 missing-required 标记；必填约束在迁移校验时强制。
	nf := Fact{
		ObjectID:   f.ObjectID,
		ValidTime:  f.ValidTime,
		RecordTime: f.RecordTime,
		Values:     coerced,
	}
	cl, ok := h.byValid[f.ValidTime]
	if !ok {
		cl = &correctionList{}
		h.byValid[f.ValidTime] = cl
		i := sort.Search(len(h.validTimes), func(i int) bool {
			return h.validTimes[i] >= f.ValidTime
		})
		h.validTimes = append(h.validTimes, 0)
		copy(h.validTimes[i+1:], h.validTimes[i:])
		h.validTimes[i] = f.ValidTime
	}
	cl.facts = append(cl.facts, nf)
	h.lastRecord = f.RecordTime
	if !h.hasFact {
		h.firstRecord = f.RecordTime
	}
	h.hasFact = true
	return nil
}

func cloneProps(props map[string]PropertyDef) map[string]PropertyDef {
	out := make(map[string]PropertyDef, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}
