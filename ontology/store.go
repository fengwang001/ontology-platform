package ontology

import "sort"

// interval 是版本轴上的闭区间；open 为 true 时 to 被视为 +∞（随最新版本延伸）。
type interval struct {
	from int
	to   int
	open bool
}

func (i interval) contains(v int) bool {
	if v < i.from {
		return false
	}
	return i.open || v <= i.to
}

// unionIntervals 将若干区间求并集，返回互不相交、按起点升序的闭区间列表。
// 开放区间至多保留一个（与其他区间合并）。
func unionIntervals(xs []interval) []interval {
	if len(xs) == 0 {
		return nil
	}
	sorted := make([]interval, len(xs))
	copy(sorted, xs)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].from != sorted[b].from {
			return sorted[a].from < sorted[b].from
		}
		return sorted[a].to < sorted[b].to
	})
	out := make([]interval, 0, len(sorted))
	cur := sorted[0]
	for _, in := range sorted[1:] {
		// 开放区间吞掉其后一切区间；相邻整数区间 [a,b],[b+1,c] 也合并。
		if cur.open {
			continue
		}
		if in.from > cur.to+1 {
			out = append(out, cur)
			cur = in
			continue
		}
		if in.open {
			// 并入开放区间：结果从此一路延伸到 +∞（随最新版本）。
			cur.open = true
			cur.to = in.to
			continue
		}
		if in.to > cur.to {
			cur.to = in.to
		}
	}
	out = append(out, cur)
	return out
}

// 注：当 cur 自身已是开放区间时，前面的 cur.open 分支会直接收束并吞掉
// 后续所有区间，因此开放区间之后不可能再拼接闭区间，合并结果保持开放。

// intervalSet 是已合并的版本区间并集。
type intervalSet []interval

func (s intervalSet) contains(v int) bool {
	// 区间数在本场景为常数级别（每条流水增量合并）；仍用二分保证最坏情况。
	idx := sort.Search(len(s), func(i int) bool { return s[i].from > v }) - 1
	return idx >= 0 && s[idx].contains(v)
}

// attrPerm 是单个 (主体, 属性, 操作) 键上的授权并集与吊销并集。
type attrPerm struct {
	grants  intervalSet
	revokes intervalSet
}

// allowedAt 判定版本 v 上是否有权限：授权并集覆盖且不被吊销区间覆盖（吊销优先）。
func (p attrPerm) allowedAt(v int) bool {
	return p.grants.contains(v) && !p.revokes.contains(v)
}

// attrAllowed 是 nil 安全的属性权限判定。
func (v *permView) attrAllowed(attrID string, op Op, at int) bool {
	byOp := v.attrs[attrID]
	if byOp == nil {
		return false
	}
	p := byOp[op]
	return p != nil && p.allowedAt(at)
}

// typeAllowed 是 nil 安全的类型级权限判定。
func (v *permView) typeAllowed(op Op, at int) bool {
	p := v.typeLevel[op]
	return p != nil && p.allowedAt(at)
}

// permView 是单个 (主体, 类型) 的增量物化权限视图。
// 授权/吊销流水到来时只做常数区间合并，判定时只读本结构，绝不扫描历史流水。
type permView struct {
	attrs map[string]map[Op]*attrPerm // attrID -> op -> perm
	// 类型级权限（attrID == "*"）。
	typeLevel map[Op]*attrPerm
}

func newPermView() *permView {
	return &permView{
		attrs:     map[string]map[Op]*attrPerm{},
		typeLevel: map[Op]*attrPerm{},
	}
}

func (v *permView) attr(attrID string, op Op) *attrPerm {
	if attrID == "*" {
		p := v.typeLevel[op]
		if p == nil {
			p = &attrPerm{}
			v.typeLevel[op] = p
		}
		return p
	}
	byOp := v.attrs[attrID]
	if byOp == nil {
		byOp = map[Op]*attrPerm{}
		v.attrs[attrID] = byOp
	}
	p := byOp[op]
	if p == nil {
		p = &attrPerm{}
		byOp[op] = p
	}
	return p
}

// apply 将一条流水增量并入视图。
func (v *permView) apply(e *PermissionEntry, iv interval) {
	p := v.attr(e.AttrID, e.Op)
	if e.Grant {
		p.grants = unionIntervals(append(p.grants, iv))
	} else {
		p.revokes = unionIntervals(append(p.revokes, iv))
	}
}

// AccessCounters 用于以可验证方式证明性能要求：统计一次视图判定过程中
// 实际访问的历史记录数量。扫描历史流水/历史版本会使计数增长，
// 本实现的在线判定路径不触碰历史，故计数恒为常数。
type AccessCounters struct {
	VersionRecords  int
	PermissionRows  int
	ViewRecords     int
	TypeStateRecord int
}

// Store 是网关的全部内存状态。所有公开写操作经 Gateway 串行化，
// 存储内部不再加锁；保留 mu 仅供计数器等诊断使用。
type Store struct {
	// 类型状态：每个类型恰好一条“当前状态记录”。
	typeState map[string]*typeState
	// 不可变历史：版本记录与权限流水只追加，永不删除（含废弃属性条目）。
	versionHistory map[string][]*TypeVersion
	permLog        []*PermissionEntry
	// 视图：每个 (类型, 主体) 一条物化视图记录，随流水增量推进。
	views map[string]map[string]*permView
	// 对象。
	objects map[string]*Object

	counters AccessCounters
}

type typeState struct {
	typeID      string
	current     *TypeVersion             // 当前版本快照（一条记录）
	byID        map[string]*Attribute    // 标识符 -> 当前属性
	byName      map[string]*Attribute    // 当前名字 -> 当前属性
	attrs       map[string]*AttrSnapshot // 标识符 -> 生命周期
	writePolicy WritePolicy
}

func newStore() *Store {
	return &Store{
		typeState:      map[string]*typeState{},
		versionHistory: map[string][]*TypeVersion{},
		views:          map[string]map[string]*permView{},
		objects:        map[string]*Object{},
	}
}

// ---- 版本与类型状态（调用方持有网关串行锁）----

func (s *Store) getType(typeID string) *typeState {
	s.counters.TypeStateRecord++
	return s.typeState[typeID]
}

func (s *Store) putType(t *typeState) {
	s.typeState[t.typeID] = t
}

func (s *Store) appendVersion(v *TypeVersion) {
	s.versionHistory[v.TypeID] = append(s.versionHistory[v.TypeID], v)
}

// snapshotVersion 按版本号取历史版本，仅审计使用（在线判定不调用，不计入在线计数）。
func (s *Store) snapshotVersion(typeID string, version int) *TypeVersion {
	vs := s.versionHistory[typeID]
	idx := sort.Search(len(vs), func(i int) bool { return vs[i].Version >= version })
	if idx < len(vs) && vs[idx].Version == version {
		return vs[idx]
	}
	return nil
}

// ---- 权限流水（只追加）----

func (s *Store) appendPermission(e *PermissionEntry) {
	e.Seq = int64(len(s.permLog)) + 1
	s.permLog = append(s.permLog, e)
	s.counters.PermissionRows++
	// 写入时增量物化：该条目立即并入其 (类型,主体) 的视图（O(1) 区间合并）。
	// 于是任何后续在线判定都只需读 1 条视图记录，无需扫描任何历史流水。
	v := s.ensureView(e.TypeID, e.Subject)
	iv := interval{from: e.FromVer, to: e.ToVer, open: e.OpenEnded}
	v.apply(e, iv)
}

// permissionHistory 返回历史权限流水（含废弃属性条目），仅供审计查询。
func (s *Store) permissionHistory(typeID, subject, attrID string) []*PermissionEntry {
	var out []*PermissionEntry
	for _, e := range s.permLog {
		if e.TypeID != typeID {
			continue
		}
		if subject != "" && e.Subject != subject {
			continue
		}
		if attrID != "" && e.AttrID != attrID {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ---- 增量物化视图：在线判定的唯一权限数据来源 ----

// ensureView 取出或创建 (类型, 主体) 的物化视图记录。
func (s *Store) ensureView(typeID, subject string) *permView {
	bySubject := s.views[typeID]
	if bySubject == nil {
		bySubject = map[string]*permView{}
		s.views[typeID] = bySubject
	}
	v := bySubject[subject]
	if v == nil {
		v = newPermView()
		bySubject[subject] = v
	}
	return v
}

// view 返回在线判定使用的物化视图：恰好访问 1 条视图记录，
// 不访问任何历史版本记录或历史权限流水，与二者的历史总量无关。
func (s *Store) view(typeID, subject string) *permView {
	s.counters.ViewRecords++
	return s.ensureView(typeID, subject)
}

func (s *Store) resetCounters() { s.counters = AccessCounters{} }

// ---- 对象 ----

func (s *Store) getObject(id string) *Object { return s.objects[id] }

func (s *Store) putObject(o *Object) { s.objects[o.ID] = o }
