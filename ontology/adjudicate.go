package ontology

import "sort"

// finalizeHook is used only by tests to interleave a rule adjustment at
// the exact point between snapshot adjudication and finalization.
var finalizeHook func()

// cutoffKey is the inclusive upper bound for a request at time t.
func cutoffKey(t int64) OrderKey { return OrderKey{Time: t, Seq: ^uint64(0)} }

// orderedSlice sorts a per-object slice by OrderKey and reports whether
// any two records share the same key (undeterminable tie).
func orderedSlice(in []Event) (out []Event, ambiguous bool) {
	out = append([]Event(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Key().Before(out[j].Key())
	})
	for i := 1; i < len(out); i++ {
		if out[i-1].Key() == out[i].Key() {
			return out, true
		}
	}
	return out, false
}

type adjContext struct {
	snap       *snapshot
	objectID   string
	objectType string
	createdAt  OrderKey
	atTime     int64
	rule       RuleVersion

	active     map[EdgeKey]bool
	inCount    map[string]int
	everActive map[string]bool
	clearedAt  map[string]OrderKey
	marked     []Event
	virtual    OrderKey
	hasVirtual bool
	properties map[string]string
	scanned    int
}

func newAdjContext(s *snapshot, id, objectType string, created OrderKey, atTime int64, rule RuleVersion) *adjContext {
	return &adjContext{
		snap:       s,
		objectID:   id,
		objectType: objectType,
		createdAt:  created,
		atTime:     atTime,
		rule:       rule,
		active:     map[EdgeKey]bool{},
		inCount:    map[string]int{},
		everActive: map[string]bool{},
		clearedAt:  map[string]OrderKey{},
		properties: map[string]string{},
	}
}

func (c *adjContext) edgeOf(e Event) EdgeKey {
	return EdgeKey{LinkType: e.TypeID, From: e.ObjectID, To: e.PeerID}
}

// checkDangling validates references inside the object's relevant slice
// prefix: link types must be declared before use, and edge peers must
// already exist when referenced.
func checkDangling(s *snapshot, id string, events []Event, cutoff OrderKey) error {
	for _, e := range events {
		if cutoff.Before(e.Key()) {
			continue
		}
		if e.Kind != EvLinkEstablished && e.Kind != EvLinkRevoked {
			continue
		}
		if _, ok := s.linkTypes[e.TypeID]; !ok {
			return danglingf("event seq=%d uses undeclared link type %q", e.Seq, e.TypeID)
		}
		if at, ok := s.linkDeclaredAt[e.TypeID]; ok && at.After(e.Key()) {
			return danglingf("event seq=%d uses link type %q before its declaration at %+v", e.Seq, e.TypeID, at)
		}
		for _, peer := range [2]string{e.ObjectID, e.PeerID} {
			if peer == "" || peer == id {
				continue
			}
			if first, ok := s.firstAppearance[peer]; !ok || first.After(e.Key()) {
				return danglingf("event seq=%d references object %q before its creation", e.Seq, peer)
			}
		}
	}
	return nil
}

// adjudicate runs the indexed, per-object-slice evaluation against a
// fixed rule version. All rule logic lives here and is a pure function
// of (snapshot, id, atTime, rule), which is what makes repeated calls
// with the same pinned version idempotent regardless of caller time.
func adjudicate(s *snapshot, id string, atTime int64, rule RuleVersion) (*DetermineResult, error) {
	events, ambiguous := orderedSlice(s.byObject[id])
	cutoff := cutoffKey(atTime)

	// Priority 4: collect an unresolvable-order error when an identical
	// order key pair falls inside the relevant prefix.
	var ambiguousErr error
	if ambiguous {
		var prev Event
		for _, e := range events {
			if e.Key() == prev.Key() && !cutoff.Before(e.Key()) {
				ambiguousErr = ambiguousf("events seq=%d and seq=%d share order key %+v", prev.Seq, e.Seq, e.Key())
				break
			}
			prev = e
		}
	}

	var created Event
	var found bool
	for _, e := range events {
		if e.Kind == EvObjectCreated && !cutoff.Before(e.Key()) {
			created, found = e, true
			break
		}
	}
	if !found {
		if first, ok := s.firstAppearance[id]; ok && cutoff.Before(first) {
			return nil, pickError(nil, beforeFirstf("time %d precedes first appearance of %q at %+v", atTime, id, first), ambiguousErr)
		}
		return nil, pickError(nil, beforeFirstf("object %q has no creation event at or before time %d", id, atTime), ambiguousErr)
	}

	var beforeErr error
	if atTime < created.Time {
		beforeErr = beforeFirstf("time %d precedes creation of %q at %d", atTime, id, created.Time)
	}

	// Priority 2: dangling references inside the relevant prefix.
	danglingErr := checkDangling(s, id, events, cutoff)

	// When several classes apply simultaneously only the highest
	// priority is reported (1 superseded is handled by the caller;
	// here: 2 dangling > 3 before-first > 4 ambiguous).
	if err := pickError(danglingErr, beforeErr, ambiguousErr); err != nil {
		return nil, err
	}

	objectType := s.objectType[id]
	c := newAdjContext(s, id, objectType, created.Key(), atTime, rule)
	c.scanned = len(events)

	horizon := OrderKey{Time: rule.EffectiveFrom, Seq: rule.EffectiveFromSeq}
	inWindow := func(e Event) bool {
		return rule.Retroactive || !e.Key().Before(horizon)
	}

	for _, e := range events {
		if cutoff.Before(e.Key()) {
			break
		}
		switch e.Kind {
		case EvLinkEstablished, EvLinkRevoked:
			edge := c.edgeOf(e)
			windowed := inWindow(e)
			if e.Kind == EvLinkEstablished {
				c.active[edge] = true
				if edge.To == id && windowed && rule.Requires(objectType, edge.LinkType) {
					if c.inCount[edge.LinkType] == 0 {
						c.clearedAt[edge.LinkType] = OrderKey{}
					}
					c.inCount[edge.LinkType]++
					c.everActive[edge.LinkType] = true
				}
			} else {
				if c.active[edge] {
					delete(c.active, edge)
					if edge.To == id && windowed && rule.Requires(objectType, edge.LinkType) {
						c.inCount[edge.LinkType]--
						if c.inCount[edge.LinkType] == 0 {
							c.clearedAt[edge.LinkType] = e.Key()
						}
					}
				}
			}
		case EvPropertyAssigned:
			c.properties[e.PropertyKey] = e.PropertyValue
		case EvOrphanMarked:
			c.marked = append(c.marked, e)
		}
	}

	res := &DetermineResult{
		ObjectID:      id,
		AtTime:        atTime,
		RuleVersion:   rule,
		Status:        StatusActive,
		EventsScanned: c.scanned,
	}

	var markedAt OrderKey
	var hasMarked bool
	for _, e := range c.marked {
		if !hasMarked || e.Key().Before(markedAt) {
			markedAt, hasMarked = e.Key(), true
		}
	}

	// Compute the virtual orphan point AFTER the full replay: it is the
	// earliest time at which every required type simultaneously had
	// count zero, taking the maximum of per-type clearance times while
	// the state remains cleared at cutoff. A later re-establishment
	// resets that type's clearance, so an earlier point is retracted.
	required := c.rule.RequiredTypes(objectType)
	if !hasMarked && len(required) > 0 {
		allClearedNow := true
		var point OrderKey
		for _, linkType := range required {
			if c.inCount[linkType] != 0 || !c.everActive[linkType] {
				allClearedNow = false
				break
			}
			at := c.clearedAt[linkType]
			if point.Time == 0 && point.Seq == 0 || at.After(point) {
				point = at
			}
		}
		if allClearedNow && !(point.Time == 0 && point.Seq == 0) {
			c.hasVirtual = true
			c.virtual = point
		}
	}

	if hasMarked {
		res.Status = StatusCascadeOrphan
		res.HasMarkedRecord = true
		res.MarkedOrphanAt = markedAt
	} else if c.hasVirtual {
		res.Status = StatusRetroactiveOrphan
		res.HasVirtualPoint = true
		res.VirtualOrphanAt = c.virtual
	}

	state := &ObjectState{
		ID:              id,
		ObjectType:      objectType,
		CreatedAt:       created.Key(),
		Properties:      c.properties,
		ActiveLinks:     map[EdgeKey]struct{}{},
		Status:          res.Status,
		VirtualOrphanAt: c.virtual,
		MarkedOrphanAt:  markedAt,
		HasVirtualPoint: c.hasVirtual,
		HasMarkedRecord: hasMarked,
	}
	for edge := range c.active {
		if edge.To == id {
			state.ActiveLinks[edge] = struct{}{}
		}
	}
	state.ActivityAfterVirtual = activityAfter(events, c.hasVirtual, c.virtual, cutoff)
	state.ActivityAfterMarked = activityAfter(events, hasMarked, markedAt, cutoff)
	res.ActivityAfterVirtual = state.ActivityAfterVirtual
	res.State = state
	return res, nil
}

// activityAfter lists normal events (property assignment, new links)
// strictly after point and within the cutoff.
func activityAfter(events []Event, has bool, point, cutoff OrderKey) []SubsequentActivity {
	if !has {
		return nil
	}
	var acts []SubsequentActivity
	for _, e := range events {
		if cutoff.Before(e.Key()) {
			break
		}
		if !point.Before(e.Key()) {
			continue
		}
		switch e.Kind {
		case EvPropertyAssigned:
			acts = append(acts, SubsequentActivity{Key: e.Key(), Kind: e.Kind, Detail: e.PropertyKey})
		case EvLinkEstablished:
			acts = append(acts, SubsequentActivity{Key: e.Key(), Kind: e.Kind, Detail: e.TypeID + ":" + e.PeerID})
		}
	}
	return acts
}

// Determine adjudicates one object against the declared rule basis.
func (st *Store) Determine(req DetermineRequest) (*DetermineResult, error) {
	s := st.current()

	var rule *RuleVersion
	resolved := req.Basis
	if req.Basis.IsHead() {
		if s.headVersion == nil {
			return nil, ruleSupersededf("no rule version installed")
		}
		rule = s.headVersion
		resolved.HeadSeq = rule.EffectiveFromSeq
	} else {
		rv, ok := s.ruleVersions[req.Basis.VersionID]
		if !ok {
			return nil, ruleSupersededf("pinned rule version %q is not installed", req.Basis.VersionID)
		}
		rule = rv
	}

	res, err := adjudicate(s, req.ObjectID, req.AtTime, *rule)
	if err != nil {
		return nil, err
	}
	res.BasisResolved = resolved

	// Finalization guard: run exclusively with all mutators. A HEAD
	// basis whose head moved while adjudication was in flight fails with
	// ErrRuleSuperseded and leaves no observable trace behind. Pinned
	// immutable versions always finalize, matching idempotency.
	st.mu.Lock()
	if finalizeHook != nil {
		st.mu.Unlock()
		finalizeHook()
		st.mu.Lock()
	}
	if resolved.IsHead() {
		if st.s.headVersion == nil || st.s.headVersion.EffectiveFromSeq != resolved.HeadSeq {
			st.mu.Unlock()
			return nil, ruleSupersededf("HEAD rule version was superseded during determination")
		}
	}
	st.mu.Unlock()

	cross := runCrossCheck(s, req)
	if !req.SkipCrossCheck {
		res.CrossCheck = cross
	}

	st.appendAudit(AuditRecord{
		ObjectID:         req.ObjectID,
		AtTime:           req.AtTime,
		BasisResolved:    resolved,
		RuleVersionID:    rule.ID,
		RuleEffectiveSeq: rule.EffectiveFromSeq,
		RuleRetroactive:  rule.Retroactive,
		Status:           res.Status,
		EventsScanned:    res.EventsScanned,
		StreamSeqHigh:    s.eventSeqHigh,
		CrossCheck:       cross,
		Outcome:          "ok",
	})
	return res, nil
}

// Rebuild reconstructs every object visible at time T under basis b.
// Per-object adjudication failures are collected in Problems instead of
// aborting the whole reconstruction.
func (st *Store) Rebuild(atTime int64, b Basis) (*NetworkState, error) {
	s := st.current()
	var rule *RuleVersion
	resolved := b
	if b.IsHead() {
		if s.headVersion == nil {
			return nil, ruleSupersededf("no rule version installed")
		}
		rule = s.headVersion
		resolved.HeadSeq = rule.EffectiveFromSeq
	} else {
		rv, ok := s.ruleVersions[b.VersionID]
		if !ok {
			return nil, ruleSupersededf("pinned rule version %q is not installed", b.VersionID)
		}
		rule = rv
	}

	// One finalization guard covers the whole rebuild.
	st.mu.Lock()
	if resolved.IsHead() && (st.s.headVersion == nil || st.s.headVersion.EffectiveFromSeq != resolved.HeadSeq) {
		st.mu.Unlock()
		return nil, ruleSupersededf("HEAD rule version was superseded during rebuild")
	}
	st.mu.Unlock()

	net := &NetworkState{
		At:       atTime,
		Objects:  map[string]*ObjectState{},
		Problems: map[string]error{},
	}
	for id := range s.objectType {
		first := s.firstAppearance[id]
		if first.Time > atTime {
			net.Problems[id] = beforeFirstf("time %d precedes first appearance of %q", atTime, id)
			continue
		}
		res, err := adjudicate(s, id, atTime, *rule)
		if err != nil {
			net.Problems[id] = err
			continue
		}
		res.BasisResolved = resolved
		net.Objects[id] = res.State
	}
	return net, nil
}
