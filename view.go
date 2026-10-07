package ontology

import (
	"sort"
	"time"
)

// baseLocation is the unified base timezone all grouping and ordering is
// normalized into, regardless of which default timezone each object's type
// used at write time.
var baseLocation = time.UTC

// normalize interprets a wall-clock value in the given IANA zone and returns
// the corresponding instant in the base timezone.
func normalize(w WallClock, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	c := w.t
	return time.Date(c.Year(), c.Month(), c.Day(),
		c.Hour(), c.Minute(), c.Second(), c.Nanosecond(), loc).In(baseLocation), nil
}

// groupKeyOf buckets a normalized instant into its base-timezone calendar day.
func groupKeyOf(t time.Time) string { return t.In(baseLocation).Format("2006-01-02") }

// lessItem is the deterministic in-group order: normalized instant, then
// (type id, object id) as the tie-breaker. It never depends on arrival order.
func lessItem(a, b Item) bool {
	if !a.Normalized.Equal(b.Normalized) {
		return a.Normalized.Before(b.Normalized)
	}
	if a.TypeID != b.TypeID {
		return a.TypeID < b.TypeID
	}
	return a.ObjID < b.ObjID
}

// objState is the view's incremental record for one object.
type objState struct {
	id     string
	typeID string
	links  int
	write  *WriteRec // latest write of the grouping property, by WriteSeq
	placed bool
	item   Item
}

// group is one materialized bucket, kept sorted by lessItem.
type group struct {
	items []Item
}

// View maintains the grouped, ordered aggregate over one link relation.
type View struct {
	id       string
	linkType string
	cursor   int // number of delivery-log events applied

	objs   map[string]*objState
	pairs  map[[2]string]bool // live links of this view's link type
	groups map[string]*group
	keys   []string // group keys, sorted ascending

	audit        []AuditEntry
	auditSeq     uint64
	lastQueryOps int64
}

func newView(id, linkType string) *View {
	return &View{
		id:       id,
		linkType: linkType,
		objs:     make(map[string]*objState),
		pairs:    make(map[[2]string]bool),
		groups:   make(map[string]*group),
	}
}

// decidePlacement is the pure placement rule shared by the incremental view
// and the naive rebuild model. Given an object's link count, its latest
// grouping-property write, and the current type metadata, it returns either
// a placed item or the error category that quarantines the object.
//
// Error checks run in the global priority order so that an object matching
// several categories is always attributed to the same one.
func decidePlacement(s snapshot, linkType string, links int, w *WriteRec) (Item, *ErrCode) {
	if links == 0 || w == nil {
		return Item{}, nil // not a member; no error
	}
	if !s.linkEndpointsExist(linkType) {
		return Item{}, errPtr(ErrLinkEndpointMissing)
	}
	if _, _, ok := s.groupingPropActive(w.TypeID); !ok {
		return Item{}, errPtr(ErrLinkEndpointMissing) // object's own type is gone
	}
	if w.TZVersion == 0 {
		return Item{}, errPtr(ErrNoTimezoneAtWrite)
	}
	zone, ok := s.tzZone(w.TypeID, w.TZVersion)
	if !ok {
		return Item{}, errPtr(ErrMigrationInvalid)
	}
	if _, active, _ := s.groupingPropActive(w.TypeID); !active {
		return Item{}, errPtr(ErrPropertyDeprecated)
	}
	norm, err := normalize(w.Wall, zone)
	if err != nil {
		return Item{}, errPtr(ErrMigrationInvalid)
	}
	return Item{
		ObjID:      w.ObjID,
		TypeID:     w.TypeID,
		Normalized: norm,
		TZVersion:  w.TZVersion,
		GroupKey:   groupKeyOf(norm),
	}, nil
}

func errPtr(c ErrCode) *ErrCode { return &c }

// sync applies all not-yet-applied log events and reports the single
// highest-priority error category observed during this pass, if any.
func (v *View) sync(e *Engine) Report {
	s := e.snap()
	errObjs := make(map[ErrCode]map[string]bool)
	applied := 0
	for ; v.cursor < len(e.log); v.cursor++ {
		ev := e.log[v.cursor]
		applied++
		switch ev.Kind {
		case EvWrite:
			v.applyWrite(s, ev, errObjs)
		case EvLink, EvUnlink:
			v.applyLink(s, ev, errObjs)
		case EvTypeChanged:
			v.applyTypeChanged(s, ev, errObjs)
		}
	}
	rep := Report{Applied: applied}
	if code, objs, ok := highestPriorityErr(errObjs); ok {
		rep.Err = &ViewError{Code: code, Detail: code.String(), Objects: objs}
	}
	return rep
}

func highestPriorityErr(errObjs map[ErrCode]map[string]bool) (ErrCode, []string, bool) {
	var best ErrCode
	bestPrio := int(^uint(0) >> 1)
	found := false
	for code, set := range errObjs {
		if len(set) == 0 {
			continue
		}
		if p := errPriority[code]; p < bestPrio {
			bestPrio, best, found = p, code, true
		}
	}
	if !found {
		return 0, nil, false
	}
	return best, sortedKeys(errObjs[best]), true
}

func (v *View) applyWrite(s snapshot, ev Event, errObjs map[ErrCode]map[string]bool) {
	w := ev.Write
	prop, _, typeOK := s.groupingPropActive(w.TypeID)
	if typeOK && w.Prop != prop {
		return // not the grouping property; irrelevant to this view
	}
	o := v.objs[w.ObjID]
	if o == nil {
		o = &objState{id: w.ObjID, typeID: w.TypeID}
		v.objs[w.ObjID] = o
	}
	if o.write != nil && o.write.WriteSeq >= w.WriteSeq {
		v.record(ev, o, "ignored-stale", nil)
		return // older or duplicate write loses; arrival order is irrelevant
	}
	ow := w
	o.write = &ow
	v.recompute(s, ev, o, errObjs)
}

func (v *View) applyLink(s snapshot, ev Event, errObjs map[ErrCode]map[string]bool) {
	if ev.LinkType != v.linkType {
		return
	}
	pair := [2]string{ev.LeftID, ev.RightID}
	delta := 0
	if ev.Kind == EvLink {
		if v.pairs[pair] {
			return // idempotent
		}
		v.pairs[pair] = true
		delta = 1
	} else {
		if !v.pairs[pair] {
			return // idempotent
		}
		delete(v.pairs, pair)
		delta = -1
	}
	for _, end := range []struct{ id, typeID string }{
		{ev.LeftID, ev.LeftType}, {ev.RightID, ev.RightType},
	} {
		o := v.objs[end.id]
		if o == nil {
			o = &objState{id: end.id}
			v.objs[end.id] = o
		}
		if o.typeID == "" {
			o.typeID = end.typeID
		}
		o.links += delta
		v.recompute(s, ev, o, errObjs)
	}
}

// applyTypeChanged re-evaluates placements after type metadata changed
// (property deprecation or type deletion). If the changed type is an
// endpoint of this view's link relation, every placement is re-evaluated
// because the view-level ErrLinkEndpointMissing condition may have flipped.
func (v *View) applyTypeChanged(s snapshot, ev Event, errObjs map[ErrCode]map[string]bool) {
	all := false
	if l, ok := s.links[v.linkType]; ok && (l.leftType == ev.ChangedType || l.rightType == ev.ChangedType) {
		all = true
	}
	for _, o := range v.objs {
		if all || o.typeID == ev.ChangedType {
			v.recompute(s, ev, o, errObjs)
		}
	}
}

// recompute re-runs the placement rule for one object and moves it between
// groups if needed. An object occupies at most one group at any moment.
func (v *View) recompute(s snapshot, ev Event, o *objState, errObjs map[ErrCode]map[string]bool) {
	item, errCode := decidePlacement(s, v.linkType, o.links, o.write)
	switch {
	case errCode != nil:
		if o.placed {
			v.remove(o)
		}
		if errObjs[*errCode] == nil {
			errObjs[*errCode] = make(map[string]bool)
		}
		errObjs[*errCode][o.write.ObjID] = true
		v.record(ev, o, "quarantined", errCode)
	case o.links == 0 || o.write == nil:
		if o.placed {
			v.remove(o)
			v.record(ev, o, "removed", nil)
		} else {
			v.record(ev, o, "pending", nil)
		}
	default:
		if o.placed && o.item.GroupKey == item.GroupKey && o.item.Normalized.Equal(item.Normalized) &&
			o.item.TZVersion == item.TZVersion {
			return // nothing changed
		}
		if o.placed {
			v.remove(o)
		}
		o.item = item
		o.placed = true
		v.insert(item)
		v.record(ev, o, "placed", nil)
	}
}

func (v *View) insert(it Item) {
	g := v.groups[it.GroupKey]
	if g == nil {
		g = &group{}
		v.groups[it.GroupKey] = g
		i := sort.SearchStrings(v.keys, it.GroupKey)
		v.keys = append(v.keys, "")
		copy(v.keys[i+1:], v.keys[i:])
		v.keys[i] = it.GroupKey
	}
	i := sort.Search(len(g.items), func(i int) bool { return !lessItem(g.items[i], it) })
	g.items = append(g.items, Item{})
	copy(g.items[i+1:], g.items[i:])
	g.items[i] = it
}

func (v *View) remove(o *objState) {
	key := o.item.GroupKey
	g := v.groups[key]
	i := sort.Search(len(g.items), func(i int) bool { return !lessItem(g.items[i], o.item) })
	if i < len(g.items) && g.items[i].ObjID == o.item.ObjID {
		g.items = append(g.items[:i], g.items[i+1:]...)
	}
	if len(g.items) == 0 {
		delete(v.groups, key)
		i := sort.SearchStrings(v.keys, key)
		v.keys = append(v.keys[:i], v.keys[i+1:]...)
	}
	o.placed = false
}

// result renders the materialized groups. Its cost is proportional to the
// result size only: no log scans, no history replay.
func (v *View) result() []Group {
	var ops int64
	out := make([]Group, 0, len(v.keys))
	for _, k := range v.keys {
		ops++
		g := v.groups[k]
		items := make([]Item, len(g.items))
		copy(items, g.items)
		ops += int64(len(items))
		out = append(out, Group{Key: k, Items: items})
	}
	v.lastQueryOps = ops
	return out
}

func (v *View) record(ev Event, o *objState, decision string, errCode *ErrCode) {
	v.auditSeq++
	entry := AuditEntry{
		Seq:         v.auditSeq,
		EventLogSeq: ev.LogSeq,
		EventKind:   ev.Kind.String(),
		Decision:    decision,
	}
	if o.write != nil {
		entry.ObjID = o.write.ObjID
		entry.TypeID = o.write.TypeID
		entry.Prop = o.write.Prop
		entry.WriteSeq = o.write.WriteSeq
		entry.TZVersion = o.write.TZVersion
		entry.Wall = o.write.Wall.String()
	} else {
		entry.ObjID = o.id
		entry.TypeID = o.typeID
	}
	if o.placed {
		entry.Group = o.item.GroupKey
		entry.Normalized = o.item.Normalized.Format(time.RFC3339)
	}
	if errCode != nil {
		entry.ErrCode = *errCode
		entry.Err = errCode.String()
	}
	v.audit = append(v.audit, entry)
}
