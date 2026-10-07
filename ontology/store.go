package ontology

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Value 是属性值（string / int64 / float64 / bool）。
type Value = any

// gapKind 标记缺失历史数据的作用域类别。
type gapKind int

const (
	gapObjectProps gapKind = iota // 某对象的属性历史
	gapObjectLife                 // 某对象的存在性历史
	gapLink                       // 某条链接的存在性历史
	gapLinkType                   // 某链接类型的全部链接历史
	gapSchema                     // 某对象类型的定义历史
)

// gap 描述一段不可回放的历史区间 [From, To)。
// 用于建模底层历史数据缺失（如日志段损坏、归档丢失）。
type gap struct {
	kind     gapKind
	scope    string // 对象 ID / 链接类型 ID / 对象类型 ID
	Interval Interval
}

// Event 是全局串行事件日志中的一条记录。
type Event struct {
	Version Version
	Op      string
	Detail  string
}

// objectHistory 是单个对象的版本化历史。
type objectHistory struct {
	typeID string
	life   []Interval           // 存在性区间
	props  map[string][]propVal // propID -> 版本化取值
}

type propVal struct {
	Interval
	Val Value
}

// Store 是本体平台的历史化存储核心。
//
// 一致性模型：所有变更在写锁下提交并获得单调递增的 Version；
// 版本号不大于 v 的历史数据一旦发布即不可变（关闭区间一律
// copy-on-write 整体替换），因此遍历只需在读取瞬间持有读锁，
// 锁定 asOf 版本后即可与并发写入、迁移、约束调整安全交织，
// 且永远观察不到 asOf 之后的任何信息。
type Store struct {
	mu       sync.RWMutex
	version  Version
	horizon  Version // 可回放的最早边界（含）
	types    map[string]*objectTypeHistory
	linkTys  map[string]*linkTypeState
	objects  map[string]*objectHistory
	gaps     []gap
	eventLog []Event
}

// commit 在写锁下分配下一个版本号并追加事件日志。
// 调用方必须已持有写锁。
func (s *Store) commit(op, detail string) Version {
	s.version++
	s.eventLog = append(s.eventLog, Event{Version: s.version, Op: op, Detail: detail})
	return s.version
}

// CurrentVersion 返回当前最新版本号。
func (s *Store) CurrentVersion() Version {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Horizon 返回可回放的最早边界（含）。
func (s *Store) Horizon() Version {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.horizon
}

// Events 返回全局串行事件日志的副本。
func (s *Store) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Event, len(s.eventLog))
	copy(out, s.eventLog)
	return out
}

var (
	ErrObjectTypeExists   = errors.New("object type already defined")
	ErrObjectTypeUnknown  = errors.New("object type not defined")
	ErrLinkTypeExists     = errors.New("link type already defined")
	ErrLinkTypeUnknown    = errors.New("link type not defined")
	ErrObjectExists       = errors.New("object already exists")
	ErrObjectUnknown      = errors.New("object does not exist")
	ErrLinkExists         = errors.New("link already exists")
	ErrLinkUnknown        = errors.New("link does not exist")
	ErrCardinalityViolate = errors.New("cardinality constraint violated")
	ErrPropertyUnknown    = errors.New("property not defined at write version")
	ErrPropertyType       = errors.New("property value type mismatch")
	ErrEndpointType       = errors.New("link endpoint object type mismatch")
)

// DefineObjectType 定义对象类型及其初始属性集。
func (s *Store) DefineObjectType(typeID string, props []PropertyDef) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.types[typeID]; ok {
		return 0, ErrObjectTypeExists
	}
	v := s.commit("define-object-type", typeID)
	h := &objectTypeHistory{}
	h.versions = append(h.versions, ObjectTypeVersion{
		Interval: Interval{From: v, To: Open},
		Props:    cloneProps(props),
	})
	s.types[typeID] = h
	return v, nil
}

// MigrateObjectType 对对象类型施加一次属性定义迁移，产生新的定义版本。
// 迁移只追加新版本并关闭旧版本，绝不改写历史取值；
// 历史时刻的解释永远锚定覆盖该时刻的旧版本。
//
// add 中的属性若复用已有 PropertyDef.ID 则视为对该属性的重命名/改类型，
// 否则视为新增属性；dropIDs 列出被移除的属性 ID。
func (s *Store) MigrateObjectType(typeID string, add []PropertyDef, dropIDs []string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.types[typeID]
	if !ok {
		return 0, ErrObjectTypeUnknown
	}
	v := s.commit("migrate-object-type", typeID)
	cur := h.versions[len(h.versions)-1]
	dropped := make(map[string]bool, len(dropIDs))
	for _, id := range dropIDs {
		dropped[id] = true
	}
	replaced := make(map[string]bool, len(add))
	next := make([]PropertyDef, 0, len(cur.Props)+len(add))
	for _, p := range cur.Props {
		if dropped[p.ID] {
			continue
		}
		next = append(next, p)
	}
	for _, p := range add {
		replaced[p.ID] = true
	}
	// 用新定义替换同 ID 的旧定义，保持顺序稳定。
	merged := next[:0]
	for _, p := range next {
		if replaced[p.ID] {
			continue
		}
		merged = append(merged, p)
	}
	merged = append(merged, add...)
	h.versions = append(h.versions, ObjectTypeVersion{
		Interval: Interval{From: v, To: Open},
		Props:    cloneProps(merged),
	})
	// 关闭上一版本（整体替换切片元素，已发布的旧切片不被改写：
	// versions 的追加可能触发扩容拷贝，旧引用保持原样）。
	h.versions[len(h.versions)-2].To = v
	return v, nil
}

// DefineLinkType 定义链接类型及其初始基数约束。
func (s *Store) DefineLinkType(typeID, fromType, toType string, card Cardinality) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linkTys[typeID]; ok {
		return 0, ErrLinkTypeExists
	}
	if _, ok := s.types[fromType]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrObjectTypeUnknown, fromType)
	}
	if _, ok := s.types[toType]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrObjectTypeUnknown, toType)
	}
	v := s.commit("define-link-type", typeID)
	s.linkTys[typeID] = &linkTypeState{
		fromType: fromType,
		toType:   toType,
		cards: []CardinalityVersion{{
			Interval: Interval{From: v, To: Open},
			Card:     card,
		}},
		links: make(map[LinkID]*linkHistory),
		out:   make(map[string][]LinkID),
	}
	return v, nil
}

// AdjustCardinality 调整链接类型的基数约束，追加一个新的约束版本。
// 只影响之后发生的写入校验；不改变任何已存在链接的历史。
func (s *Store) AdjustCardinality(typeID string, card Cardinality) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTys[typeID]
	if !ok {
		return 0, ErrLinkTypeUnknown
	}
	v := s.commit("adjust-cardinality", typeID)
	lt.cards = append(lt.cards, CardinalityVersion{
		Interval: Interval{From: v, To: Open},
		Card:     card,
	})
	lt.cards[len(lt.cards)-2].To = v
	return v, nil
}

// PutObject 创建对象并写入初始属性。属性按写入版本的定义校验。
func (s *Store) PutObject(typeID, objID string, props map[string]Value) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.types[typeID]
	if !ok {
		return 0, ErrObjectTypeUnknown
	}
	if _, ok := s.objects[objID]; ok {
		return 0, ErrObjectExists
	}
	tv, _, _ := h.versionAt(s.version)
	oh := &objectHistory{
		typeID: typeID,
		props:  make(map[string][]propVal),
	}
	type pendingProp struct {
		id  string
		val Value
	}
	var pending []pendingProp
	for name, val := range props {
		pd, ok := tv.propByName(name)
		if !ok {
			return 0, fmt.Errorf("%w: %s", ErrPropertyUnknown, name)
		}
		if !valueTypeMatches(val, pd.Type) {
			return 0, fmt.Errorf("%w: %s", ErrPropertyType, name)
		}
		pending = append(pending, pendingProp{id: pd.ID, val: val})
	}
	v := s.commit("put-object", typeID+"/"+objID)
	oh.life = []Interval{{From: v, To: Open}}
	for _, p := range pending {
		oh.props[p.id] = []propVal{{Interval: Interval{From: v, To: Open}, Val: p.val}}
	}
	s.objects[objID] = oh
	return v, nil
}

// SetProperty 在对象已定义属性上写入新值（按当前版本的定义解释）。
func (s *Store) SetProperty(objID, propName string, val Value) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oh, ok := s.objects[objID]
	if !ok {
		return 0, ErrObjectUnknown
	}
	h := s.types[oh.typeID]
	tv, _, _ := h.versionAt(s.version)
	pd, ok := tv.propByName(propName)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrPropertyUnknown, propName)
	}
	if !valueTypeMatches(val, pd.Type) {
		return 0, fmt.Errorf("%w: %s", ErrPropertyType, propName)
	}
	v := s.commit("set-property", objID+"/"+propName)
	chain := oh.props[pd.ID]
	if n := len(chain); n > 0 {
		chain[n-1].To = v
	}
	oh.props[pd.ID] = append(chain, propVal{Interval: Interval{From: v, To: Open}, Val: val})
	return v, nil
}

// DeleteObject 关闭对象的存在性区间。历史保留，可继续被历史遍历读取。
func (s *Store) DeleteObject(objID string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oh, ok := s.objects[objID]
	if !ok {
		return 0, ErrObjectUnknown
	}
	v := s.commit("delete-object", objID)
	if n := len(oh.life); n > 0 && oh.life[n-1].To == Open {
		oh.life[n-1].To = v
	}
	for id, chain := range oh.props {
		if n := len(chain); n > 0 && chain[n-1].To == Open {
			chain[n-1].To = v
			oh.props[id] = chain
		}
	}
	return v, nil
}

// AddLink 创建链接。按提交版本恰好覆盖的基数约束版本校验，
// 约束收紧后已存在的链接不会被追溯清理。
func (s *Store) AddLink(typeID, from, to string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTys[typeID]
	if !ok {
		return 0, ErrLinkTypeUnknown
	}
	id := LinkID{Type: typeID, From: from, To: to}
	if lh, ok := lt.links[id]; ok {
		if n := len(lh.intervals); n > 0 && lh.intervals[n-1].To == Open {
			return 0, ErrLinkExists
		}
	}
	if _, ok := s.objects[from]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrObjectUnknown, from)
	}
	if _, ok := s.objects[to]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrObjectUnknown, to)
	}
	for _, ep := range []struct {
		id       string
		wantType string
	}{
		{from, lt.fromType},
		{to, lt.toType},
	} {
		oh := s.objects[ep.id]
		if oh.typeID != ep.wantType {
			return 0, fmt.Errorf("%w: %s is %s, want %s", ErrEndpointType, ep.id, oh.typeID, ep.wantType)
		}
		if n := len(oh.life); n == 0 || oh.life[n-1].To != Open {
			return 0, fmt.Errorf("%w: %s", ErrObjectUnknown, ep.id)
		}
	}
	if err := checkCardinality(lt, id, s.version); err != nil {
		return 0, err
	}
	v := s.commit("add-link", typeID+"/"+from+"/"+to)
	lh := lt.links[id]
	if lh == nil {
		lh = &linkHistory{}
		lt.links[id] = lh
		lt.out[from] = insertSortedLink(lt.out[from], id)
	}
	lh.intervals = append(lh.intervals, Interval{From: v, To: Open})
	return v, nil
}

// RemoveLink 关闭链接当前的存在性区间。
func (s *Store) RemoveLink(typeID, from, to string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lt, ok := s.linkTys[typeID]
	if !ok {
		return 0, ErrLinkTypeUnknown
	}
	id := LinkID{Type: typeID, From: from, To: to}
	lh, ok := lt.links[id]
	if !ok {
		return 0, ErrLinkUnknown
	}
	n := len(lh.intervals)
	if n == 0 || lh.intervals[n-1].To != Open {
		return 0, ErrLinkUnknown
	}
	v := s.commit("remove-link", typeID+"/"+from+"/"+to)
	lh.intervals[n-1].To = v
	return v, nil
}

// checkCardinality 按恰好覆盖版本 v 的约束版本校验新增链接。
func checkCardinality(lt *linkTypeState, id LinkID, v Version) error {
	cv, _, ok := lt.cardinalityAt(v)
	if !ok {
		return nil
	}
	countFrom, countTo := 0, 0
	for lid, lh := range lt.links {
		if n := len(lh.intervals); n == 0 || lh.intervals[n-1].To != Open {
			continue
		}
		if lid.From == id.From {
			countFrom++
		}
		if lid.To == id.To {
			countTo++
		}
	}
	switch cv.Card {
	case OneToOne:
		if countFrom > 0 || countTo > 0 {
			return ErrCardinalityViolate
		}
	case OneToMany: // 一个 From 对多个 To：每个 To 至多一条入链
		if countTo > 0 {
			return ErrCardinalityViolate
		}
	case ManyToOne: // 多个 From 对一个 To：每个 From 至多一条出链
		if countFrom > 0 {
			return ErrCardinalityViolate
		}
	case ManyToMany:
	}
	return nil
}

// Compact 将可回放边界推进到 keepFrom，丢弃 keepFrom 之前的历史细节，
// 但保留 keepFrom 时刻的基线状态（把跨界区间截断为从 keepFrom 开始）。
func (s *Store) Compact(keepFrom Version) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keepFrom <= s.horizon {
		return
	}
	s.horizon = keepFrom
	truncate := func(ivs []Interval) []Interval {
		out := ivs[:0]
		for _, iv := range ivs {
			if iv.To != Open && iv.To <= keepFrom {
				continue
			}
			if iv.From < keepFrom {
				iv.From = keepFrom
			}
			out = append(out, iv)
		}
		return out
	}
	for _, oh := range s.objects {
		oh.life = truncate(oh.life)
		for id, chain := range oh.props {
			out := chain[:0]
			for _, pv := range chain {
				if pv.To != Open && pv.To <= keepFrom {
					continue
				}
				if pv.From < keepFrom {
					pv.From = keepFrom
				}
				out = append(out, pv)
			}
			oh.props[id] = out
		}
	}
	for _, h := range s.types {
		out := h.versions[:0]
		for _, tv := range h.versions {
			if tv.To != Open && tv.To <= keepFrom {
				continue
			}
			if tv.From < keepFrom {
				tv.From = keepFrom
			}
			out = append(out, tv)
		}
		h.versions = out
	}
	for _, lt := range s.linkTys {
		out := lt.cards[:0]
		for _, cv := range lt.cards {
			if cv.To != Open && cv.To <= keepFrom {
				continue
			}
			if cv.From < keepFrom {
				cv.From = keepFrom
			}
			out = append(out, cv)
		}
		lt.cards = out
		for _, lh := range lt.links {
			lh.intervals = truncate(lh.intervals)
		}
	}
}

// DeclareGap 声明一段缺失的历史数据（建模底层日志段损坏/归档丢失）。
// 遍历查询落入该区间的状态时将得到 ErrKindHistoryMissing。
func (s *Store) DeclareGap(kind string, scope string, from, to Version) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k gapKind
	switch kind {
	case "object-props":
		k = gapObjectProps
	case "object-life":
		k = gapObjectLife
	case "link":
		k = gapLink
	case "link-type":
		k = gapLinkType
	case "schema":
		k = gapSchema
	default:
		return
	}
	s.gaps = append(s.gaps, gap{kind: k, scope: scope, Interval: Interval{From: from, To: to}})
}

// gapAt 报告 (kind, scope) 在版本 v 是否处于缺失区间。调用方需持有锁。
func (s *Store) gapAt(k gapKind, scope string, v Version) bool {
	for _, g := range s.gaps {
		if g.kind == k && g.scope == scope && g.Interval.Contains(v) {
			return true
		}
	}
	return false
}

func cloneProps(in []PropertyDef) []PropertyDef {
	out := make([]PropertyDef, len(in))
	copy(out, in)
	return out
}

func valueTypeMatches(v Value, t ValueType) bool {
	switch t {
	case TypeString:
		_, ok := v.(string)
		return ok
	case TypeInt:
		_, ok := v.(int64)
		return ok
	case TypeFloat:
		_, ok := v.(float64)
		return ok
	case TypeBool:
		_, ok := v.(bool)
		return ok
	default:
		return false
	}
}

func insertSortedLink(s []LinkID, id LinkID) []LinkID {
	i := sort.Search(len(s), func(i int) bool {
		return !linkLess(s[i], id)
	})
	s = append(s, LinkID{})
	copy(s[i+1:], s[i:])
	s[i] = id
	return s
}

// ProbeLink 判定链接在 asOf 是否存在，并返回本次判定耗费的比较步数。
// 步数只随这一条链接自身的创建/撤销次数呈对数增长，
// 与该链接类型自创建以来累计的创建撤销历史总量无关；
// 测试可独立调用本方法测量并验证该性质。
func (s *Store) ProbeLink(typeID, from, to string, asOf Version) (exists bool, steps int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lt, ok := s.linkTys[typeID]
	if !ok {
		return false, 0, ErrLinkTypeUnknown
	}
	lh, ok := lt.links[LinkID{Type: typeID, From: from, To: to}]
	if !ok {
		return false, 1, nil
	}
	exists, steps = lh.existsAt(asOf)
	return exists, steps, nil
}

func linkLess(a, b LinkID) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	if a.From != b.From {
		return a.From < b.From
	}
	return a.To < b.To
}

// NewStore 创建空存储。horizon 初始为 0，即全部历史可回放。
func NewStore() *Store {
	return &Store{
		types:   make(map[string]*objectTypeHistory),
		linkTys: make(map[string]*linkTypeState),
		objects: make(map[string]*objectHistory),
	}
}
