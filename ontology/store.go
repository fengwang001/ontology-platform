package ontology

import (
	"sort"
	"sync"
)

// PropertyValue 是被索引属性的可比较值（统一以字符串键承载）。
//
// 选字符串键而非哈希桶：哈希会引入碰撞，使“按值查找”无法单凭审计
// 自证正确；直接以规范化后的值为键，条目天然无碰撞、可逐条复核。
type PropertyValue = string

// WriteRecord 记录一次对象写入对单个属性的影响，带全局单调 LSN。
type WriteRecord struct {
	LSN      int64         // 全局单调、可比较的写入序号（非墙钟时间）
	ObjectID string        // 被写对象
	Property string        // 被写属性
	Value    PropertyValue // 写入后的新值
	Version  int64         // 该对象在此次写入后的版本号（每次 Put +1）
}

// ObjectState 是某一时刻一个对象全部属性的快照（属性 -> 当前值）。
type ObjectState map[string]PropertyValue

// propertyHist 是单个属性的版本历史，按 LSN 升序排列。
type propertyHist struct {
	lsns    []int64         // 排序后的写入 LSN
	values  []PropertyValue // 与 lsns 对齐：该 LSN 生效后的值
	version []int64         // 与 lsns 对齐：该对象当时的版本号
}

type objectData struct {
	current ObjectState // 最新属性值
	hists   map[string]*propertyHist
	version int64
	exists  bool
	typ     string
}

// Store 是带全局单调 LSN 与每对象版本号的对象存储。
//
// 所有写入在同一把锁上分配 LSN，因此写入集合天然等价于某个全局串行顺序；
// 重建状态机在拿到相同锁的情况下划分基线/增量，边界精确、不重不漏。
type Store struct {
	mu      sync.Mutex
	nextLSN int64
	objects map[string]*objectData

	// historyInspections 统计复核期间“历史记录检查条数”，
	// 用于以可验证的方式证明单条目复核不随历史长度线性增长。
	historyInspections int64
}

func NewStore() *Store {
	return &Store{
		nextLSN: 0,
		objects: make(map[string]*objectData),
	}
}

// Lock/Unlock 供同一串行域内的索引管理器借用，保证 LSN 围栏与
// 状态机迁移在同一个原子区间内完成。
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// NextLSN 返回下一个将被分配的 LSN（调用方须持锁）。
func (s *Store) NextLSN() int64 { return s.nextLSN }

// Put 在单个全局串行点上对某类型的一个对象写入若干属性，返回该次写入的 LSN。
// 同一对象的多个属性在本次写入中共享同一个 LSN 与同一个对象版本。
func (s *Store) Put(objectType, objectID string, props map[string]PropertyValue) int64 {
	s.mu.Lock()
	lsn := s.putLocked(objectType, objectID, props)
	s.mu.Unlock()
	return lsn
}

// PutLocked 同 Put，但调用方已持有 Store 锁。
func (s *Store) PutLocked(objectType, objectID string, props map[string]PropertyValue) int64 {
	return s.putLocked(objectType, objectID, props)
}

func (s *Store) putLocked(objectType, objectID string, props map[string]PropertyValue) int64 {
	s.nextLSN++
	lsn := s.nextLSN
	obj := s.objects[objectID]
	if obj == nil {
		obj = &objectData{
			current: make(ObjectState),
			hists:   make(map[string]*propertyHist),
		}
		s.objects[objectID] = obj
	}
	obj.typ = objectType
	obj.exists = true
	obj.version++
	ver := obj.version
	// 固定属性遍历顺序，使同一次 Put 产生确定的历史追加次序。
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, p := range keys {
		v := props[p]
		obj.current[p] = v
		h := obj.hists[p]
		if h == nil {
			h = &propertyHist{}
			obj.hists[p] = h
		}
		h.lsns = append(h.lsns, lsn)
		h.values = append(h.values, v)
		h.version = append(h.version, ver)
	}
	return lsn
}

// Current 返回对象当前属性快照、当前版本、最近一次写入 LSN。
// 返回的 map 是副本，调用方无需持锁即可安全读取。
func (s *Store) Current(objectID string) (ObjectState, int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj := s.objects[objectID]
	if obj == nil || !obj.exists {
		return nil, 0, 0
	}
	out := make(ObjectState, len(obj.current))
	for k, v := range obj.current {
		out[k] = v
	}
	var lastLSN int64
	for _, h := range obj.hists {
		if n := len(h.lsns); n > 0 && h.lsns[n-1] > lastLSN {
			lastLSN = h.lsns[n-1]
		}
	}
	return out, obj.version, lastLSN
}

// CurrentLocked 同 Current，但调用方已持有 Store 锁；不复制 map，
// 仅供状态机在锁内立即读取，禁止逃逸到锁外。
func (s *Store) CurrentLocked(objectID string) (ObjectState, int64) {
	obj := s.objects[objectID]
	if obj == nil || !obj.exists {
		return nil, 0
	}
	return obj.current, obj.version
}

// SnapshotAt 返回对象在 atLSN 时刻（含该 LSN 上已生效的写入）的
// 属性快照与对象版本。复杂度与历史长度相关（二分），仅重建基线扫描使用；
// 复核单个条目不走该方法，而是走 ValueAt 的 O(1) 定位。
func (s *Store) SnapshotAt(objectID string, atLSN int64) (ObjectState, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotAtLocked(objectID, atLSN)
}

func (s *Store) snapshotAtLocked(objectID string, atLSN int64) (ObjectState, int64) {
	obj := s.objects[objectID]
	if obj == nil || !obj.exists {
		return nil, 0
	}
	out := make(ObjectState)
	var ver int64
	for p, h := range obj.hists {
		idx := sort.Search(len(h.lsns), func(i int) bool { return h.lsns[i] > atLSN }) - 1
		if idx >= 0 {
			out[p] = h.values[idx]
			if h.version[idx] > ver {
				ver = h.version[idx]
			}
		}
	}
	return out, ver
}

// ValueAt 返回对象某属性在 atLSN 时刻的生效值。
//
// 关键性能性质：定位到来源版本后只检查 1 条历史记录（不回溯整条链），
// 因此“复核单个条目所检查的历史写入记录数”是常数 1，
// 与该对象历史写入总次数无关。found=false 表示该时刻属性尚无值。
func (s *Store) ValueAt(objectID, property string, atLSN int64, sourceLSN int64) (
	value PropertyValue, version int64, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.valueAtLocked(objectID, property, atLSN, sourceLSN)
}

// valueAtLocked 调用方须持有 Store 锁；计数逻辑与 ValueAt 相同。
func (s *Store) valueAtLocked(objectID, property string, atLSN, sourceLSN int64) (
	value PropertyValue, version int64, found bool) {
	obj := s.objects[objectID]
	if obj == nil {
		return "", 0, false
	}
	h := obj.hists[property]
	if h == nil {
		return "", 0, false
	}
	// 审计条目中的 SourceLSN 直接给出了来源写入位置，O(1) 校验它确实
	// 是 atLSN 时点的生效版本：即 sourceLSN <= atLSN，且其后方紧邻的
	// 历史写入 LSN（若存在）> atLSN。此处只读 1 条值记录 + 至多 1 条
	// 邻接边界记录，检查总数恒为常数。
	idx := sort.Search(len(h.lsns), func(i int) bool { return h.lsns[i] >= sourceLSN })
	s.historyInspections++ // 读取来源条目本身：1 条
	if idx >= len(h.lsns) || h.lsns[idx] != sourceLSN {
		return "", 0, false
	}
	if sourceLSN > atLSN {
		return "", 0, false
	}
	if idx+1 < len(h.lsns) {
		s.historyInspections++ // 仅检查紧邻的 1 条边界记录
		if h.lsns[idx+1] <= atLSN {
			return "", 0, false
		}
	}
	return h.values[idx], h.version[idx], true
}

// historyLengthLocked 调用方须持有 Store 锁。
func (s *Store) historyLengthLocked(objectID, property string) int {
	obj := s.objects[objectID]
	if obj == nil {
		return 0
	}
	h := obj.hists[property]
	if h == nil {
		return 0
	}
	return len(h.lsns)
}

// HistoryLength 返回某对象某属性的历史写入总次数（供取证对照）。
func (s *Store) HistoryLength(objectID, property string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.historyLengthLocked(objectID, property)
}

// ObjectIDs 返回当前存在的全部对象 ID，顺序确定（字典序）。
func (s *Store) ObjectIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.objects))
	for id, obj := range s.objects {
		if obj.exists {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// ObjectType 返回对象当前所属类型；对象不存在时返回 ""。
func (s *Store) ObjectType(objectID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj := s.objects[objectID]
	if obj == nil || !obj.exists {
		return ""
	}
	return obj.typ
}

// ObjectIDsOfType 返回某类型当前存在的全部对象 ID（字典序）。
func (s *Store) ObjectIDsOfType(typ string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objectIDsOfTypeLocked(typ)
}

// objectIDsOfTypeLocked 调用方须持有 Store 锁。
func (s *Store) objectIDsOfTypeLocked(typ string) []string {
	var ids []string
	for id, obj := range s.objects {
		if obj.exists && obj.typ == typ {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// LastLSN 返回已分配的最大 LSN（即最近一次已提交写入的 LSN）。
func (s *Store) LastLSN() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextLSN
}

// HistoryInspections 返回自上次 Reset 以来复核读取的历史记录条数。
func (s *Store) HistoryInspections() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.historyInspections
}

func (s *Store) ResetHistoryInspections() {
	s.mu.Lock()
	s.historyInspections = 0
	s.mu.Unlock()
}
