package tzperm

import "time"

// Decision is the public allow/deny verdict.
type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionDeny  Decision = "DENY"
)

// Querier identifies the viewing subject and the subject's timezone. The
// querier timezone is audited but never used as a normalization baseline.
type Querier struct {
	ID   string
	Zone *ZoneRules
}

// ViewRequest is one attribute view request. At is the query instant (Unix
// seconds UTC). When zero, the engine clock is used.
type ViewRequest struct {
	RequestID string
	ObjectID  string
	AttrName  string
	At        int64
	// HasAt distinguishes an explicit zero instant from "use the engine
	// clock": without it, tests (and callers) could never request the
	// Unix epoch explicitly.
	HasAt   bool
	Querier *Querier
}

// Outcome is the public, leak-free result of a view request. It carries only
// the request id, a binary verdict and, when applicable, one generic error
// code. It never contains normalized values, baseline zone information or
// window boundaries.
type Outcome struct {
	RequestID string
	Decision  Decision
	ErrorCode string
}

// ReferenceDecider is the optional naive cross-checker. It receives the
// exact snapshot a decision was computed on and returns the independently
// derived decision/error code plus its linear-scan comparison count.
type ReferenceDecider interface {
	Decide(snap Snapshot, req ViewRequest) (decision Decision, errorCode string, comparisons int)
}

// Engine evaluates view requests.
type Engine struct {
	store     *Store
	sink      AuditSink
	clock     func() int64
	reference ReferenceDecider
}

// NewEngine builds an engine backed by store, reporting audit records to
// sink (may be nil).
func NewEngine(store *Store, sink AuditSink) *Engine {
	return &Engine{
		store: store,
		sink:  sink,
		clock: func() int64 { return time.Now().Unix() },
	}
}

// WithReference installs a naive model cross-checker.
func (e *Engine) WithReference(ref ReferenceDecider) *Engine {
	e.reference = ref
	return e
}

// WithClock overrides the query-time clock, mainly for deterministic tests.
func (e *Engine) WithClock(clock func() int64) *Engine {
	e.clock = clock
	return e
}

// Check performs normalization and permission evaluation for one request.
//
// The whole evaluation runs under the store's write lock (audit recording
// is part of the critical section), so a concurrent stream of Checks,
// timezone-definition updates and window-rule updates is equivalent to some
// global serial order: the mutex acquisition order.
func (e *Engine) Check(req ViewRequest) Outcome {
	at := req.At
	if !req.HasAt {
		at = e.clock()
	}

	rec := Record{
		RequestID: req.RequestID,
		At:        at,
		ObjectID:  req.ObjectID,
		AttrName:  req.AttrName,
	}

	e.store.wLock()
	snap := e.store.snapshotLocked()
	decision, code := e.evaluateLocked(snap, req, at, &rec)
	rec.Decision = decision
	rec.ErrorCode = code

	if e.reference != nil {
		refDecision, refCode, refComparisons := e.reference.Decide(snap, req)
		rec.NaiveComparisons = refComparisons
		rec.ReferenceConsistent = refDecision == decision && refCode == code
	}

	if e.sink != nil {
		e.sink.Write(rec)
	}
	e.store.wUnlock()

	return Outcome{RequestID: req.RequestID, Decision: decision, ErrorCode: code}
}

// evaluateLocked runs the fixed decision pipeline. All error conditions are
// gathered independently and reduced through the fixed priority order, so a
// request that triggers several conditions reports exactly one code.
func (e *Engine) evaluateLocked(snap Snapshot, req ViewRequest, at int64, rec *Record) (Decision, string) {
	if req.Querier != nil {
		rec.Querier = req.Querier.ID
		if req.Querier.Zone != nil {
			rec.QuerierZone = req.Querier.Zone.Name
		}
	}

	obj, objOK := snap.Objects[req.ObjectID]
	if !objOK {
		return DecisionDeny, ""
	}
	attr, attrOK := obj.Attrs[req.AttrName]
	if !attrOK {
		return DecisionDeny, ""
	}

	rec.RegionID = obj.RegionID
	rec.EntryWall = attr.WallSec
	if attr.EntryZone != nil {
		rec.EntryZone = attr.EntryZone.Name
	}

	var codes []string

	// 1. Schema-level deprecation (effective at query time).
	typeVersions := snap.Types[obj.TypeID]
	tl := lookupEffective(typeVersions, func(tv TypeVersion) int64 { return tv.EffectiveFrom }, at)
	rec.EnginePathComparisons += tl.comparisons
	// Deprecation is cumulative: once a version retires an attribute, every
	// later version inherits that retirement, so the union of all versions
	// effective at t is checked.
	if tl.found {
		for i := 0; i <= tl.index; i++ {
			if typeVersions[i].AttrsDeprecated[req.AttrName] {
				codes = append(codes, CodeAttrDeprecated)
				break
			}
		}
	}

	// 2. Normalization baseline. The value instant is first decoded using
	// the immutable zone annotated at entry. The region baseline is then
	// selected at that instant for the value and at query time for the
	// window evaluation: both are the object's region default timezone
	// definition effective at the instant being normalized.
	regionVersions := snap.Regions[obj.RegionID]

	var valueInstant int64
	if attr.EntryZone == nil {
		return DecisionDeny, ""
	}
	instant, wallStatus := attr.EntryZone.ResolveWall(attr.WallSec)
	valueInstant = instant
	rec.ValueInstant = valueInstant
	rec.ValueWallStatus = wallStatus

	vl := lookupEffective(regionVersions, func(v ZoneVersion) int64 { return v.EffectiveFrom }, valueInstant)
	rec.EnginePathComparisons += vl.comparisons
	if !vl.found {
		codes = append(codes, CodeRegionZoneUnknown)
	} else {
		base := regionVersions[vl.index].Zone
		rec.BaselineZoneAtValue = zoneName(base)
	}

	ql := lookupEffective(regionVersions, func(v ZoneVersion) int64 { return v.EffectiveFrom }, at)
	rec.EnginePathComparisons += ql.comparisons
	if !ql.found {
		codes = append(codes, CodeRegionZoneUnknown)
	} else {
		queryBase := regionVersions[ql.index].Zone
		rec.BaselineZoneAtQuery = zoneName(queryBase)
		rec.QueryNormalizedWall = queryBase.ToWall(at)
	}

	// 3. Window rule validity.
	pol, hasPolicy := snap.Policies[policyKey{objectType: obj.TypeID, attr: req.AttrName}]
	var inside bool
	if hasPolicy {
		rec.WindowSetID = pol.windowSetID
		winVersions := snap.WindowSets[pol.windowSetID]
		wl := lookupEffective(winVersions, func(w WindowVersion) int64 { return w.EffectiveFrom }, at)
		rec.EnginePathComparisons += wl.comparisons
		if !wl.found {
			codes = append(codes, CodeWindowInvalid)
		} else {
			rules := winVersions[wl.index].Rules
			rec.Window = rules
			if !rules.Valid() {
				codes = append(codes, CodeWindowInvalid)
			} else if ql.found {
				sod, _ := SecondsOfDay(regionVersions[ql.index].Zone.ToWall(at))
				inside = rules.Contains(sod)
				rec.InsideWindow = inside
			}
		}
	}

	// 4. Querier identity.
	if req.Querier == nil || req.Querier.ID == "" {
		codes = append(codes, CodeQuerierMissing)
	}

	if code := highestErrorCode(codes...); code != "" {
		return DecisionDeny, code
	}
	if !hasPolicy {
		return DecisionAllow, ""
	}
	if inside {
		return DecisionAllow, ""
	}
	return DecisionDeny, ""
}

func zoneName(z *ZoneRules) string {
	if z == nil {
		return ""
	}
	return z.Name
}
