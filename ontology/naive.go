package ontology

import "sort"

// NaiveDump 是独立实现的朴素全量重建模型：
// 无视增量视图状态，直接从全量事件历史与当前注册表出发，
// 按函数式规约重算整个视图结果，用于差分测试逐条对照。
//
// 规约（与增量实现共同遵守）：
//  1. 同一对象只取写入序号最大的一条事件（last-write-wins）；
//  2. 链接两端类型与事件对象类型均须存在且未删除，否则该对象被排除；
//  3. 对象类型当前版本已废弃分组属性的，该对象被排除；
//  4. 锚定时区版本无效（<=0 或不存在）的，该对象被排除；
//  5. 分组键 = 归一化 UTC 时刻的日桶；组内按 (时刻, 类型 ID, 对象 ID) 排序。
func (p *Platform) NaiveDump(viewID string) []GroupView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.views[viewID]
	if !ok {
		return nil
	}
	link, ok := p.links[v.LinkID]
	if !ok {
		return nil
	}
	typeAlive := func(id string) bool {
		t, ok := p.types[id]
		return ok && !t.Deleted
	}
	if !typeAlive(link.LeftType) || !typeAlive(link.RightType) {
		return nil
	}

	latest := make(map[string]ChangeEvent)
	for _, evt := range p.history {
		if cur, ok := latest[evt.ObjectID]; !ok || evt.WriteSeq > cur.WriteSeq {
			latest[evt.ObjectID] = evt
		}
	}

	type member struct {
		objectID string
		typeID   string
		instant  int64
	}
	byGroup := make(map[int64][]member)
	for _, evt := range latest {
		t, ok := p.types[evt.ObjectTypeID]
		if !ok || t.Deleted || t.DeprecatedGroupingProp {
			continue
		}
		if evt.AnchoredTzVersion <= 0 {
			continue
		}
		zone, ok := LookupZone(evt.AnchoredZoneID)
		if !ok {
			continue
		}
		instant := zone.ToUTC(evt.LocalValue).Unix()
		key := groupKeyOf(instant)
		byGroup[key] = append(byGroup[key], member{evt.ObjectID, evt.ObjectTypeID, instant})
	}

	keys := make([]int64, 0, len(byGroup))
	for k := range byGroup {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	out := make([]GroupView, 0, len(keys))
	for _, k := range keys {
		ms := byGroup[k]
		sort.Slice(ms, func(i, j int) bool {
			if ms[i].instant != ms[j].instant {
				return ms[i].instant < ms[j].instant
			}
			if ms[i].typeID != ms[j].typeID {
				return ms[i].typeID < ms[j].typeID
			}
			return ms[i].objectID < ms[j].objectID
		})
		gv := GroupView{Key: k, Members: make([]GroupMemberView, 0, len(ms))}
		for _, m := range ms {
			gv.Members = append(gv.Members, GroupMemberView{
				ObjectID:     m.objectID,
				ObjectTypeID: m.typeID,
				InstantUnix:  m.instant,
			})
		}
		out = append(out, gv)
	}
	return out
}
