package ontology

import (
	"fmt"
	"sort"
	"time"
)

// memberRec 记录一个对象当前的归属信息。
type memberRec struct {
	Key     int64
	TypeID  string
	Instant int64
}

// groupMember 是分组内的一个成员。
type groupMember struct {
	ObjectID string
	TypeID   string
	Instant  int64 // 归一化后的 UTC 时刻（Unix 秒）
}

// group 是同一分组键下的有序成员集合。
type group struct {
	key     int64
	members []groupMember // 按 (Instant, TypeID, ObjectID) 升序
}

// View 是跨链接聚合的增量维护视图。
type View struct {
	ID        string
	LinkID    string
	groups    map[int64]*group
	keys      []int64              // 有序分组键
	members   map[string]memberRec // objectID -> 当前归属（保证不重复计入多个分组）
	lastSeq   map[string]uint64
	decisions []DecisionRecord
}

func newView(id, linkID string) *View {
	return &View{
		ID:      id,
		LinkID:  linkID,
		groups:  make(map[int64]*group),
		members: make(map[string]memberRec),
		lastSeq: make(map[string]uint64),
	}
}

// floorDiv 数学意义上的向下取整除法（b > 0）。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

const secondsPerDay = 86400

// groupKeyOf 由归一化后的 UTC 时刻计算分组键（UTC 日桶）。
func groupKeyOf(instantUnix int64) int64 {
	return floorDiv(instantUnix, secondsPerDay)
}

// memberLess 是同组内的确定次序裁决规则：
// 先按归一化时刻升序；相等时按 (对象类型 ID, 对象 ID) 字典序。
// 该规则只依赖元素自身，与增量维护的处理先后顺序无关。
func memberLess(a, b groupMember) bool {
	if a.Instant != b.Instant {
		return a.Instant < b.Instant
	}
	if a.TypeID != b.TypeID {
		return a.TypeID < b.TypeID
	}
	return a.ObjectID < b.ObjectID
}

func (v *View) record(d DecisionRecord) {
	d.Seq = len(v.decisions) + 1
	d.ViewID = v.ID
	v.decisions = append(v.decisions, d)
}

func (v *View) logMigration(typeID string, tv TzDefVersion, ok bool, detail string) {
	op := "migrate"
	if !ok {
		op = "migrate-rejected"
	}
	v.record(DecisionRecord{
		Op:                op,
		ObjectTypeID:      typeID,
		AnchoredTzVersion: tv.Version,
		ZoneID:            tv.ZoneID,
		Err:               detail,
		Note:              fmt.Sprintf("effectiveSeq=%d", tv.EffectiveSeq),
	})
}

// insertMember 把成员按裁决规则插入有序分组。
func (g *group) insertMember(m groupMember) {
	i := sort.Search(len(g.members), func(i int) bool {
		return !memberLess(g.members[i], m)
	})
	g.members = append(g.members, groupMember{})
	copy(g.members[i+1:], g.members[i:])
	g.members[i] = m
}

func (g *group) removeMember(objectID string) {
	for i, m := range g.members {
		if m.ObjectID == objectID {
			g.members = append(g.members[:i], g.members[i+1:]...)
			return
		}
	}
}

func (v *View) insertKey(key int64) {
	i := sort.Search(len(v.keys), func(i int) bool { return v.keys[i] >= key })
	if i == len(v.keys) || v.keys[i] != key {
		v.keys = append(v.keys, 0)
		copy(v.keys[i+1:], v.keys[i:])
		v.keys[i] = key
	}
}

func (v *View) removeKeyIfEmpty(key int64) {
	if g, ok := v.groups[key]; ok && len(g.members) == 0 {
		delete(v.groups, key)
		i := sort.Search(len(v.keys), func(i int) bool { return v.keys[i] >= key })
		if i < len(v.keys) && v.keys[i] == key {
			v.keys = append(v.keys[:i], v.keys[i+1:]...)
		}
	}
}

// assign 把对象放入指定分组；若对象已在其他分组，先移除，保证不会重复计入。
func (v *View) assign(objectID, typeID string, instant int64) {
	key := groupKeyOf(instant)
	if rec, ok := v.members[objectID]; ok {
		if rec.Key == key && rec.Instant == instant && rec.TypeID == typeID {
			return
		}
		if g, ok := v.groups[rec.Key]; ok {
			g.removeMember(objectID)
		}
		v.removeKeyIfEmpty(rec.Key)
	}
	g, ok := v.groups[key]
	if !ok {
		g = &group{key: key}
		v.groups[key] = g
		v.insertKey(key)
	}
	g.insertMember(groupMember{ObjectID: objectID, TypeID: typeID, Instant: instant})
	v.members[objectID] = memberRec{Key: key, TypeID: typeID, Instant: instant}
}

// evict 把对象从当前分组中移除。
func (v *View) evict(objectID, note string) {
	rec, ok := v.members[objectID]
	if !ok {
		return
	}
	if g, ok := v.groups[rec.Key]; ok {
		g.removeMember(objectID)
	}
	v.removeKeyIfEmpty(rec.Key)
	delete(v.members, objectID)
	v.record(DecisionRecord{
		Op:           "evict",
		ObjectID:     objectID,
		ObjectTypeID: rec.TypeID,
		GroupKey:     rec.Key,
		InstantUnix:  rec.Instant,
		Note:         note,
	})
}

// evictType 驱逐某对象类型的全部成员（类型删除或分组属性废弃时调用）。
func (v *View) evictType(typeID, note string) {
	var ids []string
	for objectID, rec := range v.members {
		if rec.TypeID == typeID {
			ids = append(ids, objectID)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		v.evict(id, note)
	}
}

// evictAll 清空整个视图（链接端点类型被删除时调用）。
func (v *View) evictAll(note string) {
	ids := make([]string, 0, len(v.members))
	for objectID := range v.members {
		ids = append(ids, objectID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v.evict(id, note)
	}
}

// applyEvent 应用一条变更事件。调用方须持有平台锁。
// 返回 nil 表示成功归入分组；否则返回错误类别并保证对象不重复计入、
// 既有归属不被部分修改。
func (v *View) applyEvent(p *Platform, evt ChangeEvent) *ViewError {
	// last-write-wins：过期事件（写入序号不高于已应用序号）完全无效，
	// 这是最终分组归属与事件到达顺序无关的关键。
	if evt.WriteSeq <= v.lastSeq[evt.ObjectID] {
		v.record(DecisionRecord{
			Op:           "stale",
			ObjectID:     evt.ObjectID,
			ObjectTypeID: evt.ObjectTypeID,
			WriteSeq:     evt.WriteSeq,
			Note:         "superseded by newer write",
		})
		return nil
	}

	fail := func(kind ErrorKind, detail string) *ViewError {
		// 最新一次写入不可解释：对象不得留在任何分组中。
		v.evict(evt.ObjectID, "latest write rejected: "+kind.String())
		v.lastSeq[evt.ObjectID] = evt.WriteSeq
		v.record(DecisionRecord{
			Op:                "skip-error",
			ObjectID:          evt.ObjectID,
			ObjectTypeID:      evt.ObjectTypeID,
			WriteSeq:          evt.WriteSeq,
			AnchoredTzVersion: evt.AnchoredTzVersion,
			Err:               kind.String(),
			Note:              detail,
		})
		return &ViewError{Kind: kind, ViewID: v.ID, ObjectID: evt.ObjectID,
			ObjectTypeID: evt.ObjectTypeID, Detail: detail}
	}

	link, ok := p.links[v.LinkID]
	if !ok {
		return fail(ErrLinkEndpointTypeMissing, "link "+v.LinkID+" not registered")
	}
	for _, endpoint := range []string{link.LeftType, link.RightType, evt.ObjectTypeID} {
		t, ok := p.types[endpoint]
		if !ok || t.Deleted {
			return fail(ErrLinkEndpointTypeMissing, "object type "+endpoint+" missing or deleted")
		}
	}
	t := p.types[evt.ObjectTypeID]
	if t.DeprecatedGroupingProp {
		return fail(ErrGroupingPropertyDeprecated,
			"grouping property "+t.GroupingProp+" deprecated in current type version")
	}
	if evt.AnchoredTzVersion <= 0 {
		return fail(ErrNoDefaultTimezoneAtWrite,
			"no default timezone defined at write seq "+fmt.Sprint(evt.WriteSeq))
	}
	// 事件自包含：直接使用写入时刻锚定的时区，无需等待迁移记录到达。
	zone, ok := LookupZone(evt.AnchoredZoneID)
	if !ok {
		return fail(ErrNoDefaultTimezoneAtWrite, "unknown anchored zone "+evt.AnchoredZoneID)
	}

	instant := zone.ToUTC(evt.LocalValue).Unix()
	v.assign(evt.ObjectID, evt.ObjectTypeID, instant)
	v.lastSeq[evt.ObjectID] = evt.WriteSeq
	v.record(DecisionRecord{
		Op:                "assign",
		ObjectID:          evt.ObjectID,
		ObjectTypeID:      evt.ObjectTypeID,
		WriteSeq:          evt.WriteSeq,
		AnchoredTzVersion: evt.AnchoredTzVersion,
		ZoneID:            evt.AnchoredZoneID,
		LocalValue:        evt.LocalValue.Format(time.RFC3339),
		InstantUnix:       instant,
		GroupKey:          groupKeyOf(instant),
	})
	return nil
}

// GroupMemberView 是查询结果中的成员。
type GroupMemberView struct {
	ObjectID     string
	ObjectTypeID string
	InstantUnix  int64
}

// GroupView 是查询结果中的一个分组。
type GroupView struct {
	Key     int64
	Members []GroupMemberView
}

// QueryStats 记录一次查询实际访问的条目数，
// 用于独立验证查询开销与历史事件总量无关。
type QueryStats struct {
	GroupsVisited  int
	EntriesVisited int
}
