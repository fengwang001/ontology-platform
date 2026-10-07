// Package naive 是孤儿判定的独立朴素实现：不做任何增量索引，
// 对事件流逐事件重放。它用于与索引化引擎做逐条差分对照，
// 与 orphan 包仅共享数据类型，不共享任何判定逻辑。
package naive

import (
	"sort"

	"ontology/orphan"
)

// Determine 以全量扫描方式实现与引擎一致的判定语义（不含版本治理检查，
// 版本治理由调用方先行完成）。
func Determine(events []orphan.Event, q orphan.Query, spec orphan.RuleSpec) (orphan.Determination, *orphan.Error) {
	det := orphan.Determination{Query: q, Version: q.Version, Status: orphan.StatusNotOrphan}

	// 朴素重建：对象/链接/链接类型的创建信息。
	type objInfo struct {
		created   bool
		createdAt orphan.Time
		objType   orphan.ObjectTypeID
	}
	objects := map[orphan.ObjectID]objInfo{}
	type lnkInfo struct {
		createdAt orphan.Time
		linkType  orphan.LinkTypeID
		from, to  orphan.ObjectID
	}
	links := map[orphan.LinkID]lnkInfo{}
	linkTypes := map[orphan.LinkTypeID]orphan.Time{}

	sorted := append([]orphan.Event(nil), events...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Time != sorted[j].Time {
			return sorted[i].Time < sorted[j].Time
		}
		return sorted[i].Seq < sorted[j].Seq
	})

	for _, e := range sorted {
		switch e.Kind {
		case orphan.EvObjectCreated:
			o, ok := objects[e.Object]
			if !ok || !o.created || e.Time < o.createdAt {
				objects[e.Object] = objInfo{created: true, createdAt: e.Time, objType: e.ObjectType}
			}
		case orphan.EvLinkCreated:
			l, ok := links[e.Link]
			if !ok || e.Time < l.createdAt {
				links[e.Link] = lnkInfo{createdAt: e.Time, linkType: e.LinkType, from: e.From, to: e.To}
			}
		case orphan.EvLinkTypeCreated:
			if t, ok := linkTypes[e.LinkType]; !ok || e.Time < t {
				linkTypes[e.LinkType] = e.Time
			}
		}
	}

	self, ok := objects[q.Object]
	if !ok || !self.created {
		return det, &orphan.Error{Code: orphan.ErrDanglingReference, Detail: "object has not been created"}
	}

	// 切片：Time <= At 且与本对象相关的事件。
	var slice []orphan.Event
	for _, e := range sorted {
		if e.Time > q.At {
			continue
		}
		switch e.Kind {
		case orphan.EvObjectCreated, orphan.EvPropertySet, orphan.EvObjectMarkedOrphan:
			if e.Object == q.Object {
				slice = append(slice, e)
			}
		case orphan.EvLinkCreated:
			if e.From == q.Object || e.To == q.Object {
				slice = append(slice, e)
			}
		case orphan.EvLinkRevoked:
			if l, ok := links[e.Link]; ok && (l.from == q.Object || l.to == q.Object) {
				slice = append(slice, e)
			}
		}
	}
	det.EventsScanned = len(events)

	// 并列记录顺序检查。
	for i := 0; i < len(slice); {
		j := i
		for j < len(slice) && slice[j].Time == slice[i].Time {
			j++
		}
		if j-i > 1 && conflictingUnordered(slice[i:j]) {
			return det, &orphan.Error{Code: orphan.ErrAmbiguousOrder, Detail: "concurrent events cannot be ordered"}
		}
		i = j
	}

	// 引用合法性检查。
	for _, e := range slice {
		switch e.Kind {
		case orphan.EvPropertySet, orphan.EvObjectMarkedOrphan:
			o := objects[e.Object]
			if !o.created || o.createdAt > e.Time {
				return det, &orphan.Error{Code: orphan.ErrDanglingReference, Detail: "object referenced before creation"}
			}
		case orphan.EvLinkCreated:
			t, ok := linkTypes[e.LinkType]
			if !ok || t > e.Time {
				return det, &orphan.Error{Code: orphan.ErrDanglingReference, Detail: "unknown link type"}
			}
			for _, ep := range []orphan.ObjectID{e.From, e.To} {
				o := objects[ep]
				if !o.created || o.createdAt > e.Time {
					return det, &orphan.Error{Code: orphan.ErrDanglingReference, Detail: "link endpoint not yet created"}
				}
			}
		case orphan.EvLinkRevoked:
			l, ok := links[e.Link]
			if !ok || l.createdAt > e.Time {
				return det, &orphan.Error{Code: orphan.ErrDanglingReference, Detail: "revoked link not yet created"}
			}
		}
	}

	if q.At < self.createdAt {
		return det, &orphan.Error{Code: orphan.ErrTimeBeforeFirstAppearance, Detail: "time before first appearance"}
	}

	// 逐事件重放，计算归零区间。
	active := map[orphan.LinkID]bool{}
	everHad := false
	zeroSince := orphan.Time(-1)
	var marks []orphan.Time
	for _, e := range slice {
		switch e.Kind {
		case orphan.EvLinkCreated:
			if e.From == q.Object && spec.Requires(self.objType, e.LinkType) {
				active[e.Link] = true
				everHad = true
				zeroSince = -1
			}
		case orphan.EvLinkRevoked:
			delete(active, e.Link)
			if everHad && len(active) == 0 && zeroSince < 0 {
				zeroSince = e.Time
			}
		case orphan.EvObjectMarkedOrphan:
			marks = append(marks, e.Time)
		}
	}

	if !everHad || len(active) != 0 || zeroSince < 0 {
		return det, nil
	}
	det.ZeroSince = zeroSince
	det.OrphanDue = zeroSince + spec.GracePeriod
	if det.OrphanDue > q.At {
		return det, nil
	}

	confirmed := false
	var confirmedAt orphan.Time
	for _, m := range marks {
		if m >= zeroSince && (!confirmed || m < confirmedAt) {
			confirmed = true
			confirmedAt = m
		}
	}
	if confirmed {
		det.Status = orphan.StatusOrphanConfirmed
		det.ConfirmedAt = confirmedAt
		return det, nil
	}

	for _, e := range slice {
		if e.Time <= det.OrphanDue {
			continue
		}
		if e.Kind == orphan.EvPropertySet || e.Kind == orphan.EvLinkCreated {
			det.Activities = append(det.Activities, orphan.Activity{Time: e.Time, Kind: e.Kind, Seq: e.Seq})
		}
	}
	if len(det.Activities) > 0 {
		det.Status = orphan.StatusRetroOrphanWithActivity
	} else {
		det.Status = orphan.StatusRetroOrphanDormant
	}
	return det, nil
}

// touches 返回事件触及的实体键（与引擎独立实现同一规则）。
func touches(e orphan.Event) []string {
	switch e.Kind {
	case orphan.EvLinkTypeCreated:
		return []string{"lt:" + string(e.LinkType)}
	case orphan.EvObjectCreated, orphan.EvPropertySet, orphan.EvObjectMarkedOrphan:
		return []string{"o:" + string(e.Object)}
	case orphan.EvLinkCreated:
		return []string{"l:" + string(e.Link), "o:" + string(e.From), "o:" + string(e.To)}
	case orphan.EvLinkRevoked:
		return []string{"l:" + string(e.Link)}
	}
	return nil
}

// conflictingUnordered 判断同刻事件组内是否存在"触及共同实体但
// 无法经 After 链（传递）确定先后"的事件对。
func conflictingUnordered(group []orphan.Event) bool {
	inGroup := map[orphan.EventID]bool{}
	for _, e := range group {
		inGroup[e.ID] = true
	}
	// 反复扩张直到不动点：after 关系的传递闭包。
	after := map[orphan.EventID]map[orphan.EventID]bool{}
	for _, e := range group {
		if inGroup[e.After] {
			if after[e.ID] == nil {
				after[e.ID] = map[orphan.EventID]bool{}
			}
			after[e.ID][e.After] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for a, bs := range after {
			for b := range bs {
				for c := range after[b] {
					if !after[a][c] {
						after[a][c] = true
						changed = true
					}
				}
			}
		}
	}
	for x := 0; x < len(group); x++ {
		for y := x + 1; y < len(group); y++ {
			a, b := group[x], group[y]
			if after[a.ID][b.ID] || after[b.ID][a.ID] {
				continue
			}
			shared := map[string]bool{}
			for _, s := range touches(a) {
				shared[s] = true
			}
			for _, s := range touches(b) {
				if shared[s] {
					return true
				}
			}
		}
	}
	return false
}
