package ontology

import "fmt"

// naiveModel 是朴素生命周期模型：与 Coordinator 完全独立的参照实现，
// 仅用于测试对照。它不维护任何索引或增量状态，所有判定都通过
// 线性扫描追加式日志完成（例如判断链接恢复条件要扫描全部历史事件，
// 复杂度随删除复活循环次数线性增长——这正是 Coordinator 避免的）。
type naiveModel struct {
	grants  map[GrantID]Grant
	types   map[string]LinkType
	objects map[ObjectID]bool
	journal []Event // 被接受的事件；Interval 字段在查询时重放填充
	links   map[LinkID]*naiveLink
	clock   LogicalTime
}

// naiveLink 记录链接失效时点的完整变迁序列（追加失效时点或 0 表示恢复）。
type naiveLink struct {
	def  Link
	hist []LogicalTime
}

func newNaive() *naiveModel {
	return &naiveModel{
		grants:  make(map[GrantID]Grant),
		types:   make(map[string]LinkType),
		objects: make(map[ObjectID]bool),
		links:   make(map[LinkID]*naiveLink),
	}
}

func (n *naiveModel) addGrant(g Grant)        { n.grants[g.ID] = g }
func (n *naiveModel) registerType(t LinkType) { n.types[t.Name] = t }

func (n *naiveModel) revokeGrant(id GrantID) {
	if g, ok := n.grants[id]; ok {
		g.Revoked = true
		n.grants[id] = g
	}
}

func (n *naiveModel) checkGrant(actor Actor, action Action, obj ObjectID, gid GrantID) (Grant, error) {
	g, ok := n.grants[gid]
	if !ok {
		return Grant{}, fmt.Errorf("grant %q: %w", gid, ErrPermissionDenied)
	}
	if g.Revoked || g.Actor != actor || g.Action != action ||
		(g.Object != "" && g.Object != obj) {
		return Grant{}, fmt.Errorf("grant %q: %w", gid, ErrPermissionDenied)
	}
	return g, nil
}

// alive 扫描全部日志判断对象当前是否存活。
func (n *naiveModel) alive(obj ObjectID) bool {
	alive := false
	for _, e := range n.journal {
		if e.Object != obj {
			continue
		}
		switch e.Kind {
		case EventCreate, EventRevive:
			alive = true
		case EventDelete:
			alive = false
		}
	}
	return alive
}

// lastDeletion 扫描全部日志找对象最近一次删除事件。
func (n *naiveModel) lastDeletion(obj ObjectID) (Event, bool) {
	for i := len(n.journal) - 1; i >= 0; i-- {
		if n.journal[i].Object == obj && n.journal[i].Kind == EventDelete {
			return n.journal[i], true
		}
	}
	return Event{}, false
}

func (n *naiveModel) linkValue(l *naiveLink) LogicalTime {
	if len(l.hist) == 0 {
		return 0
	}
	return l.hist[len(l.hist)-1]
}

func (n *naiveModel) next() (EventID, LogicalTime) {
	n.clock++
	return EventID(fmt.Sprintf("evt-%d", n.clock)), n.clock
}

func (n *naiveModel) createObject(actor Actor, id ObjectID) error {
	if n.objects[id] {
		return fmt.Errorf("object %q: %w", id, ErrAlreadyExists)
	}
	eid, ts := n.next()
	n.journal = append(n.journal, Event{ID: eid, Kind: EventCreate, Object: id, Actor: actor, Time: ts})
	n.objects[id] = true
	return nil
}

func (n *naiveModel) deleteObject(actor Actor, id ObjectID, gid GrantID) error {
	if !n.objects[id] {
		return fmt.Errorf("object %q: %w", id, ErrNotFound)
	}
	grant, err := n.checkGrant(actor, ActionDelete, id, gid)
	if err != nil {
		return err
	}
	if !n.alive(id) {
		return fmt.Errorf("object %q: %w", id, ErrObjectDeleted)
	}
	eid, ts := n.next()
	n.journal = append(n.journal, Event{
		ID: eid, Kind: EventDelete, Object: id, Actor: actor, Time: ts, Grant: grant,
	})
	// 线性扫描全部链接（无索引）。
	for _, l := range n.links {
		if n.linkValue(l) == 0 && n.types[l.def.Type].Policy == CascadeInvalidate &&
			(l.def.From == id || l.def.To == id) {
			l.hist = append(l.hist, ts)
		}
	}
	return nil
}

func (n *naiveModel) reviveObject(req ReviveRequest) error {
	if !n.objects[req.Object] {
		return fmt.Errorf("object %q: %w", req.Object, ErrNotFound)
	}
	grant, err := n.checkGrant(req.Actor, ActionRevive, req.Object, req.GrantID)
	if err != nil {
		return err
	}
	if n.alive(req.Object) {
		return fmt.Errorf("object %q: %w", req.Object, ErrObjectNotDeleted)
	}
	del, _ := n.lastDeletion(req.Object)
	if req.ResumesDeletion != del.ID {
		return fmt.Errorf("object %q: %w", req.Object, ErrStaleDeletion)
	}
	restorable := func(l *naiveLink) bool {
		v := n.linkValue(l)
		return v != 0 && v == del.Time &&
			n.types[l.def.Type].Policy == CascadeInvalidate &&
			(l.def.From == req.Object || l.def.To == req.Object)
	}
	var restore []LinkID
	if req.RestoreLinks {
		if len(req.LinkIDs) > 0 {
			for _, lid := range req.LinkIDs {
				l, ok := n.links[lid]
				if !ok {
					return fmt.Errorf("link %q: %w", lid, ErrNotFound)
				}
				if !restorable(l) {
					return fmt.Errorf("link %q: %w", lid, ErrLinkRestore)
				}
			}
			restore = append(restore, req.LinkIDs...)
		} else {
			for lid, l := range n.links {
				if restorable(l) {
					restore = append(restore, lid)
				}
			}
			sortStrings(restore)
		}
	}
	eid, ts := n.next()
	n.journal = append(n.journal, Event{
		ID: eid, Kind: EventRevive, Object: req.Object,
		Actor: req.Actor, Time: ts, Grant: grant,
		ResumesDeletion: req.ResumesDeletion,
		RestoreLinks:    req.RestoreLinks,
		RestoredLinks:   restore,
	})
	for _, lid := range restore {
		n.links[lid].hist = append(n.links[lid].hist, 0)
	}
	return nil
}

func (n *naiveModel) accessObject(actor Actor, id ObjectID, kind EventKind, payload string) error {
	if !n.objects[id] {
		return fmt.Errorf("object %q: %w", id, ErrNotFound)
	}
	if !n.alive(id) {
		return fmt.Errorf("object %q: %w", id, ErrObjectDeleted)
	}
	eid, ts := n.next()
	n.journal = append(n.journal, Event{
		ID: eid, Kind: kind, Object: id, Actor: actor, Time: ts, Payload: payload,
	})
	return nil
}

func (n *naiveModel) linkObjects(actor Actor, id LinkID, typeName string, from, to ObjectID) error {
	if _, ok := n.types[typeName]; !ok {
		return fmt.Errorf("link type %q: %w", typeName, ErrNotFound)
	}
	if _, ok := n.links[id]; ok {
		return fmt.Errorf("link %q: %w", id, ErrAlreadyExists)
	}
	for _, oid := range []ObjectID{from, to} {
		if !n.objects[oid] {
			return fmt.Errorf("object %q: %w", oid, ErrNotFound)
		}
		if !n.alive(oid) {
			return fmt.Errorf("object %q: %w", oid, ErrObjectDeleted)
		}
	}
	n.links[id] = &naiveLink{def: Link{ID: id, Type: typeName, From: from, To: to}}
	return nil
}

// history 重放全部日志，现场切分存活区间并填充事件的区间归属。
func (n *naiveModel) history(obj ObjectID) (ObjectHistory, error) {
	if !n.objects[obj] {
		return ObjectHistory{}, fmt.Errorf("object %q: %w", obj, ErrNotFound)
	}
	h := ObjectHistory{Object: obj, Alive: n.alive(obj)}
	for _, e := range n.journal {
		if e.Object != obj {
			continue
		}
		switch e.Kind {
		case EventCreate, EventRevive:
			iv := Interval{
				ID:       IntervalID(fmt.Sprintf("%s#%d", obj, len(h.Intervals)+1)),
				Object:   obj,
				Index:    len(h.Intervals) + 1,
				OpenedBy: e.ID,
			}
			e.Interval = iv.ID
			iv.Events = append(iv.Events, e)
			h.Intervals = append(h.Intervals, iv)
		case EventDelete:
			iv := &h.Intervals[len(h.Intervals)-1]
			e.Interval = iv.ID
			iv.Events = append(iv.Events, e)
			iv.ClosedBy = e.ID
		default:
			iv := &h.Intervals[len(h.Intervals)-1]
			e.Interval = iv.ID
			iv.Events = append(iv.Events, e)
		}
	}
	return h, nil
}

func (n *naiveModel) getLink(id LinkID) (Link, error) {
	l, ok := n.links[id]
	if !ok {
		return Link{}, fmt.Errorf("link %q: %w", id, ErrNotFound)
	}
	def := l.def
	def.InvalidatedAt = n.linkValue(l)
	return def, nil
}

func sortStrings(s []LinkID) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
