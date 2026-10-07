package temporalauth

import (
	"sync"
	"sync/atomic"
)

// state 是某一全局序列号下的完整不可变状态。任何“变更”都不会原地修改
// state，而是在写锁内基于当前 state 复制出一个新 state 并原子发布；读路径
// 通过 atomic 加载获得 state 指针后无需持锁即可安全读取。这既保证了
// copy-on-write 的 O(1) 快照获取，也使并发操作等价于某个全局串行顺序。
type state struct {
	seq          int64
	regions      map[string]Region
	objectTypes  map[string]ObjectType
	objects      map[string]string
	objectRegion map[string]string
	records      map[string]map[string]TemporalRecord
	windows      map[string]map[string]WindowChain
}

func cloneState(prev *state) *state {
	ns := &state{
		seq:          prev.seq,
		regions:      make(map[string]Region, len(prev.regions)+1),
		objectTypes:  make(map[string]ObjectType, len(prev.objectTypes)+1),
		objects:      make(map[string]string, len(prev.objects)+1),
		objectRegion: make(map[string]string, len(prev.objectRegion)+1),
		records:      make(map[string]map[string]TemporalRecord, len(prev.records)+1),
		windows:      make(map[string]map[string]WindowChain, len(prev.windows)+1),
	}
	for k, v := range prev.regions {
		ns.regions[k] = v
	}
	for k, v := range prev.objectTypes {
		ns.objectTypes[k] = v
	}
	for k, v := range prev.objects {
		ns.objects[k] = v
	}
	for k, v := range prev.objectRegion {
		ns.objectRegion[k] = v
	}
	for k, v := range prev.records {
		m := make(map[string]TemporalRecord, len(v)+1)
		for pk, pv := range v {
			m[pk] = pv
		}
		ns.records[k] = m
	}
	for k, v := range prev.windows {
		m := make(map[string]WindowChain, len(v)+1)
		for pk, pv := range v {
			m[pk] = pv
		}
		ns.windows[k] = m
	}
	return ns
}

// Store 是内存中的可串行化状态容器。
type Store struct {
	catalog *ZoneCatalog

	mu  sync.Mutex // 仅写路径持有，串行化所有变更
	cur atomic.Pointer[state]

	// regionProbes 统计生效版本解析的比较探测次数，供复杂度独立验证使用。
	regionProbes atomic.Int64
}

// NewStore 构造状态容器。
func NewStore(catalog *ZoneCatalog) *Store {
	s := &Store{catalog: catalog}
	s.cur.Store(&state{
		regions:      map[string]Region{},
		objectTypes:  map[string]ObjectType{},
		objects:      map[string]string{},
		objectRegion: map[string]string{},
		records:      map[string]map[string]TemporalRecord{},
		windows:      map[string]map[string]WindowChain{},
	})
	return s
}

// commit 在写锁内基于当前状态派生新状态、推进序列号并原子发布。
func (s *Store) commit(mutate func(ns *state) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := cloneState(s.cur.Load())
	if err := mutate(ns); err != nil {
		return err
	}
	ns.seq++
	s.cur.Store(ns)
	return nil
}

// Seq 返回已提交变更的全局单调序列号。
func (s *Store) Seq() int64 { return s.cur.Load().seq }

// RegionProbes 返回生效版本解析累计探测次数。
func (s *Store) RegionProbes() int64 { return s.regionProbes.Load() }

// ResetProbes 清零探测计数。
func (s *Store) ResetProbes() { s.regionProbes.Store(0) }

// AddRegion 注册一个（可能已含版本的）地区。
func (s *Store) AddRegion(r *Region) {
	_ = s.commit(func(ns *state) error {
		ns.regions[r.ID] = *r
		return nil
	})
}

// AddObjectType 注册对象类型。
func (s *Store) AddObjectType(ot *ObjectType) {
	_ = s.commit(func(ns *state) error {
		ns.objectTypes[ot.ID] = *ot
		return nil
	})
}

// RegisterObject 建立对象到对象类型与所属地区的归属。
func (s *Store) RegisterObject(objectID, objectTypeID, regionID string) {
	_ = s.commit(func(ns *state) error {
		ns.objects[objectID] = objectTypeID
		ns.objectRegion[objectID] = regionID
		if _, ok := ns.regions[regionID]; !ok {
			ns.regions[regionID] = Region{ID: regionID}
		}
		return nil
	})
}

// AppendRegionVersion 追加地区默认时区版本，并做非回溯与存在性校验。
func (s *Store) AppendRegionVersion(regionID string, v RegionVersion) error {
	return s.commit(func(ns *state) error {
		r := ns.regions[regionID]
		if n := len(r.Versions); n > 0 && v.ValidFrom <= r.Versions[n-1].ValidFrom {
			return &AuthError{Code: ErrRegionTimezoneUnresolved}
		}
		if _, found := s.catalog.Get(v.ZoneID); !found {
			return &AuthError{Code: ErrRegionTimezoneUnresolved}
		}
		r.ID = regionID
		r.Versions = append(append([]RegionVersion(nil), r.Versions...), v)
		ns.regions[regionID] = r
		return nil
	})
}

// AppendWindowVersion 追加窗口规则版本并立即校验规则本身。
func (s *Store) AppendWindowVersion(objectTypeID, property string, v WindowVersion) error {
	if err := v.Rule.Validate(); err != nil {
		return err
	}
	return s.commit(func(ns *state) error {
		m := ns.windows[objectTypeID]
		if m == nil {
			m = map[string]WindowChain{}
			ns.windows[objectTypeID] = m
		}
		chain := m[property]
		if n := len(chain.Versions); n > 0 && v.ValidFrom <= chain.Versions[n-1].ValidFrom {
			return &AuthError{Code: ErrWindowRuleInvalid}
		}
		chain.Versions = append(append([]WindowVersion(nil), chain.Versions...), v)
		m[property] = chain
		ns.windows[objectTypeID] = m
		return nil
	})
}

// AppendObjectTypeVersion 在写锁内追加对象类型版本（测试与类型迁移使用）。
func (s *Store) AppendObjectTypeVersion(objectTypeID string, v ObjectTypeVersion) error {
	return s.commit(func(ns *state) error {
		ot := ns.objectTypes[objectTypeID]
		ot.ID = objectTypeID
		ot.Versions = append(append([]ObjectTypeVersion(nil), ot.Versions...), v)
		ns.objectTypes[objectTypeID] = ot
		return nil
	})
}

// PutTemporalRecord 录入时间类属性，并在录入时刻一次性冻结归一化基准。
func (s *Store) PutTemporalRecord(rec TemporalRecord) (TemporalRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.cur.Load()
	regionID := cur.objectRegion[rec.ObjectID]
	region, ok := cur.regions[regionID]
	if !ok {
		return TemporalRecord{}, &AuthError{Code: ErrRegionTimezoneUnresolved}
	}
	idx, ok := region.EffectiveVersionAt(rec.RecordedAt)
	if !ok {
		return TemporalRecord{}, &AuthError{Code: ErrRegionTimezoneUnresolved}
	}
	zone, ok := s.catalog.Get(region.Versions[idx].ZoneID)
	if !ok {
		return TemporalRecord{}, &AuthError{Code: ErrRegionTimezoneUnresolved}
	}
	rec.regionID = regionID
	rec.basisVersion = idx
	rec.normalizedAt = zone.CivilToInstant(rec.Wall)

	ns := cloneState(cur)
	m := ns.records[rec.ObjectID]
	if m == nil {
		m = map[string]TemporalRecord{}
		ns.records[rec.ObjectID] = m
	}
	m[rec.Property] = rec
	ns.seq++
	s.cur.Store(ns)
	return rec, nil
}

// Snapshot 以一次原子指针加载返回 O(1) 的一致只读快照；快照内容不可变，
// 读取全程无需持锁，也不会与并发写入竞争。
func (s *Store) Snapshot() Snapshot {
	st := s.cur.Load()
	return Snapshot{
		seq:          st.seq,
		catalog:      s.catalog,
		regions:      st.regions,
		objectTypes:  st.objectTypes,
		objects:      st.objects,
		objectRegion: st.objectRegion,
		records:      st.records,
		windows:      st.windows,
		probeCounter: &s.regionProbes,
	}
}

// Snapshot 是一次查看请求期间固定不变的只读状态视图（指向某一不可变 state）。
type Snapshot struct {
	seq          int64
	catalog      *ZoneCatalog
	regions      map[string]Region
	objectTypes  map[string]ObjectType
	objects      map[string]string
	objectRegion map[string]string
	records      map[string]map[string]TemporalRecord
	windows      map[string]map[string]WindowChain
	probeCounter *atomic.Int64
}

// Seq 返回快照对应的全局序列号。
func (snap Snapshot) Seq() int64 { return snap.seq }

func (snap Snapshot) incrementProbes(n int64) {
	snap.probeCounter.Add(n)
}

// resolveRegionIndex 以二分查找解析时刻 t 生效的地区版本下标，并计入探针。
// 单次查看的版本解析开销为 O(log n)，可由探针独立验证。
func (snap Snapshot) resolveRegionIndex(r *Region, t Instant) (int, bool) {
	n := len(r.Versions)
	if n == 0 || t < r.Versions[0].ValidFrom {
		snap.incrementProbes(1)
		return 0, false
	}
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		snap.incrementProbes(1)
		if r.Versions[mid].ValidFrom <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1, true
}
