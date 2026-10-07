package ontology

import (
	"encoding/json"
	"sort"
	"sync/atomic"
)

// Engine performs read/write adjudication for one object type.
type Engine struct {
	ot    *ObjectType
	rows  RowPolicySet
	props PropPolicySet
	mode  WriteMode
	db    *store
	log   Logger
	seq   atomic.Int64

	// Candidate indexes: keep per-call work proportional only to rules
	// that can actually hit this subject, never the total registered
	// rule count.
	rowBySubject map[string][]int
	rowByGroup   map[string][]int
	rowAll       []int

	propBySubject map[string][]int
	propByGroup   map[string][]int
	propAll       []int
}

// Config configures a new Engine.
type Config struct {
	Type      *ObjectType
	Rows      RowPolicySet
	Props     PropPolicySet
	WriteMode WriteMode
	Logger    Logger
}

// NewEngine validates policies and builds candidate indexes.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Type == nil {
		return nil, &DecisionError{ErrInvalidType, "nil object type"}
	}
	if err := cfg.Rows.validate(cfg.Type); err != nil {
		return nil, err
	}
	if err := cfg.Props.validate(cfg.Type); err != nil {
		return nil, err
	}
	mode := cfg.WriteMode
	if mode == 0 {
		mode = WriteRejectAll
	}
	if !mode.valid() {
		return nil, &DecisionError{ErrInvalidType, "write mode must be RejectAll or Drop"}
	}
	e := &Engine{
		ot:            cfg.Type,
		rows:          cfg.Rows,
		props:         cfg.Props,
		mode:          mode,
		db:            newStore(),
		log:           cfg.Logger,
		rowBySubject:  map[string][]int{},
		rowByGroup:    map[string][]int{},
		propBySubject: map[string][]int{},
		propByGroup:   map[string][]int{},
	}
	for i, r := range cfg.Rows.Rules {
		if r.Selector.MatchAll {
			e.rowAll = append(e.rowAll, i)
			continue
		}
		for _, u := range r.Selector.Users {
			e.rowBySubject[u] = append(e.rowBySubject[u], i)
		}
		for _, g := range r.Selector.Groups {
			e.rowByGroup[g] = append(e.rowByGroup[g], i)
		}
	}
	for i, r := range cfg.Props.Rules {
		if r.Selector.MatchAll {
			e.propAll = append(e.propAll, i)
			continue
		}
		for _, u := range r.Selector.Users {
			e.propBySubject[u] = append(e.propBySubject[u], i)
		}
		for _, g := range r.Selector.Groups {
			e.propByGroup[g] = append(e.propByGroup[g], i)
		}
	}
	return e, nil
}

// AddInstance registers an instance for adjudication.
func (e *Engine) AddInstance(inst *Instance) error {
	if inst == nil || inst.ID == "" {
		return &DecisionError{ErrInvalidType, "instance requires an id"}
	}
	e.db.put(inst.clone())
	return nil
}

// candidateRowRules returns the deduplicated indexes of row rules that
// can match the subject. Only these bodies are ever evaluated.
func (e *Engine) candidateRowRules(sub Subject) []int {
	return dedupCandidates(
		e.rowBySubject[sub.ID],
		groupCandidates(e.rowByGroup, sub.Groups),
		e.rowAll,
	)
}

func (e *Engine) candidatePropRules(sub Subject) []int {
	return dedupCandidates(
		e.propBySubject[sub.ID],
		groupCandidates(e.propByGroup, sub.Groups),
		e.propAll,
	)
}

func groupCandidates(idx map[string][]int, groups []string) []int {
	var out []int
	for _, g := range groups {
		out = append(out, idx[g]...)
	}
	return out
}

func dedupCandidates(parts ...[]int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, p := range parts {
		for _, i := range p {
			if _, ok := seen[i]; ok {
				continue
			}
			seen[i] = struct{}{}
			out = append(out, i)
		}
	}
	return out
}

// rowVerdict evaluates candidate row predicates against RAW values.
func (e *Engine) rowVerdict(sub Subject, inst *Instance) (bool, DecisionTrace, *DecisionError) {
	tr := DecisionTrace{RowMode: mergeName(e.rows.Mode), PropMode: mergeName(e.props.Mode)}
	var allow, deny bool
	for _, i := range e.candidateRowRules(sub) {
		r := e.rows.Rules[i]
		tr.RowRulesEvaluated++
		all := true
		for _, p := range r.Predicates {
			ok, derr := p.eval(e.ot, inst)
			if derr != nil {
				return false, tr, derr
			}
			if !ok {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		b := RuleBasis{RuleID: r.ID, PredicateN: len(r.Predicates)}
		if r.Effect == EffectAllow {
			allow = true
			b.Effect = "allow"
		} else {
			deny = true
			b.Effect = "deny"
		}
		tr.MatchedRowRules = append(tr.MatchedRowRules, b)
	}
	visible, outcome := mergeEffects(e.rows.Mode, allow, deny)
	tr.RowOutcome = outcome
	sortBases(tr.MatchedRowRules)
	return visible, tr, nil
}

func mergeEffects(mode MergeMode, allow, deny bool) (bool, string) {
	if allow && deny {
		if mode == AllowOverrides {
			return true, "allow"
		}
		return false, "deny"
	}
	if allow {
		return true, "allow"
	}
	if deny {
		return false, "deny"
	}
	return false, "default_deny"
}

func mergeName(m MergeMode) string {
	if m == AllowOverrides {
		return "allow_overrides"
	}
	return "deny_overrides"
}

func sortBases(bs []RuleBasis) {
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].RuleID != bs[j].RuleID {
			return bs[i].RuleID < bs[j].RuleID
		}
		return bs[i].Property < bs[j].Property
	})
}

// Read adjudicates one read. props selects the properties projected;
// nil/empty means every declared property. Output is identical for
// repeat calls at the same stored state, independent of policy
// registration order and of the requested property ordering.
func (e *Engine) Read(sub Subject, instanceID string, props []string) (*ReadResult, *DecisionError) {
	req := map[string]any{"properties": props}

	inst, exists := e.db.readView(instanceID)

	if !exists {
		err := &DecisionError{ErrInstanceNotFound, "instance not found or not visible"}
		e.record("read", sub, instanceID, req, nil, err, DecisionTrace{RowOutcome: "not_found"})
		return nil, err
	}

	visible, tr, verr := e.rowVerdict(sub, inst)
	if verr != nil {
		e.record("read", sub, instanceID, req, nil, verr, tr)
		return nil, verr
	}
	if !visible {
		err := &DecisionError{ErrInstanceNotFound, "instance not found or not visible"}
		e.record("read", sub, instanceID, req, nil, err, tr)
		return nil, err
	}

	// Property projection selection; unknown properties rejected up
	// front in declaration-stable order.
	wanted, serr := e.selectProps(props)
	if serr != nil {
		e.record("read", sub, instanceID, req, nil, serr, tr)
		return nil, serr
	}

	cand := e.candidatePropRules(sub)
	verdicts := e.props.mergeProps(sub, cand)
	tr.PropRulesEvaluated = len(cand)

	res := &ReadResult{InstanceID: instanceID, Visible: true, View: map[string]RawValue{}}
	var conflict *DecisionError

	for _, name := range wanted {
		dt := e.ot.types_[name]
		v := verdicts[name]
		cell, rawPresent := inst.cells[name]

		switch v.state {
		case PropRaw:
			e.addPropVerdictBasis(&tr, name, v)
			if rawPresent && cell.present {
				res.View[name] = cell.value
			} else {
				res.Absent = append(res.Absent, name)
			}
		case PropMasked:
			e.addPropVerdictBasis(&tr, name, v)
			if !(rawPresent && cell.present) {
				res.Absent = append(res.Absent, name)
				break
			}
			mv, merr := v.mask(sub, cell.value)
			if merr != nil {
				conflict = &DecisionError{ErrMaskedTypeViolation, "mask on " + name + " failed: " + merr.Error()}
				break
			}
			if !dt.CheckValue(mv) {
				conflict = &DecisionError{ErrMaskedTypeViolation, "masked value for " + name + " violates declared type " + dt.String()}
				break
			}
			res.View[name] = mv
		default:
			// PropHidden or PropNoPolicy: field exists but is not
			// readable. Listed by name only; no value, no presence bit.
			e.addPropVerdictBasis(&tr, name, v)
			res.Redacted = append(res.Redacted, name)
		}
		if conflict != nil {
			break
		}
	}

	sortBases(tr.MatchedPropRules)
	if conflict != nil {
		res.View = nil
		res.Absent = nil
		res.Redacted = nil
		res.Conflict = conflict
		e.record("read", sub, instanceID, req, res, conflict, tr)
		return res, conflict
	}
	res.Trace = tr
	e.record("read", sub, instanceID, req, res, nil, tr)
	return res, nil
}

func (e *Engine) addWriteBasis(tr *DecisionTrace, name string, v propVerdict) {
	if v.writeMask != nil {
		tr.MatchedPropRules = append(tr.MatchedPropRules, RuleBasis{RuleID: v.writeRuleID, Property: name, Effect: "allow", Presented: "masked"})
		return
	}
	if v.writable {
		for _, id := range v.writeAllowID {
			tr.MatchedPropRules = append(tr.MatchedPropRules, RuleBasis{RuleID: id, Property: name, Effect: "allow"})
		}
		return
	}
	for _, id := range v.writeDenyID {
		tr.MatchedPropRules = append(tr.MatchedPropRules, RuleBasis{RuleID: id, Property: name, Effect: "deny"})
	}
}

func (e *Engine) addPropVerdictBasis(tr *DecisionTrace, name string, v propVerdict) {
	switch v.state {
	case PropMasked:
		tr.MatchedPropRules = append(tr.MatchedPropRules, RuleBasis{RuleID: v.maskRuleID, Property: name, Effect: "allow", Presented: "masked"})
	case PropRaw:
		for _, id := range v.readAllowIDs {
			tr.MatchedPropRules = append(tr.MatchedPropRules, RuleBasis{RuleID: id, Property: name, Effect: "allow", Presented: "raw"})
		}
	case PropHidden:
		for _, id := range v.readDenyIDs {
			tr.MatchedPropRules = append(tr.MatchedPropRules, RuleBasis{RuleID: id, Property: name, Effect: "deny"})
		}
	}
}

func (e *Engine) selectProps(props []string) ([]string, *DecisionError) {
	if len(props) == 0 {
		out := make([]string, len(e.ot.props))
		for i, p := range e.ot.props {
			out[i] = p.Name
		}
		return out, nil
	}
	wanted := make([]string, 0, len(props))
	seen := map[string]bool{}
	// Preserve declaration order regardless of request order.
	for _, decl := range e.ot.props {
		for _, req := range props {
			if req == decl.Name && !seen[req] {
				seen[req] = true
				wanted = append(wanted, req)
			}
		}
	}
	for _, p := range props {
		if !seen[p] {
			return nil, &DecisionError{ErrUnknownProperty, "unknown property " + p}
		}
	}
	return wanted, nil
}

func (e *Engine) record(kind string, sub Subject, instID string, input any, output any, err *DecisionError, tr DecisionTrace) {
	if e.log == nil {
		return
	}
	rec := CallRecord{
		Seq:        e.seq.Add(1),
		Kind:       kind,
		SubjectID:  sub.ID,
		InstanceID: instID,
		Trace:      tr,
	}
	rec.InputJSON, _ = json.Marshal(input)
	rec.OutputJSON, _ = json.Marshal(output)
	if err != nil {
		rec.ErrorKind = err.Kind.String()
		rec.ErrorMsg = err.Msg
	}
	e.log.Log(rec)
}

// Write adjudicates and applies one multi-property write. The outcome
// is independent of the request's property ordering (properties are
// processed in declaration order), and failure priority is fixed:
// row invisibility > property not writable > masking type violation >
// invalid supplied value. On rejection nothing changes: no values, no
// Version bump, no LastWriteID update.
func (e *Engine) Write(sub Subject, instanceID string, values map[string]RawValue) (*WriteResult, *DecisionError) {
	req := map[string]any{"values": values}

	inst, exists := e.db.readView(instanceID)
	if !exists {
		err := &DecisionError{ErrInstanceNotFound, "instance not found or not visible"}
		e.record("write", sub, instanceID, req, nil, err, DecisionTrace{RowOutcome: "not_found"})
		return nil, err
	}
	visible, tr, verr := e.rowVerdict(sub, inst)
	if verr != nil {
		e.record("write", sub, instanceID, req, nil, verr, tr)
		return nil, verr
	}

	// Priority 1: row-level invisibility. Reported as the same opaque
	// kind as a missing instance so existence cannot be probed.
	if !visible {
		err := &DecisionError{ErrInstanceNotFound, "instance not found or not visible"}
		e.record("write", sub, instanceID, req, nil, err, tr)
		return nil, err
	}

	// Unknown attribute (stable, order-independent).
	names := make([]string, 0, len(values))
	for name := range values {
		if !e.ot.Has(name) {
			err := &DecisionError{ErrUnknownProperty, "unknown property " + name}
			e.record("write", sub, instanceID, req, nil, err, tr)
			return nil, err
		}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return e.ot.index[names[i]] < e.ot.index[names[j]]
	})

	cand := e.candidatePropRules(sub)
	verdicts := e.props.mergeProps(sub, cand)
	tr.PropRulesEvaluated = len(cand)

	// Priority 2: property not writable (only under RejectAll; under
	// Drop such properties are silently skipped instead).
	if e.mode == WriteRejectAll {
		for _, name := range names {
			v := verdicts[name]
			if !v.writable {
				e.addWriteBasis(&tr, name, v)
				err := &DecisionError{ErrPropertyNotWritable, "property not writable: " + name}
				sortBases(tr.MatchedPropRules)
				e.record("write", sub, instanceID, req, nil, err, tr)
				return nil, err
			}
		}
	}

	// Resolve every value first (in declaration order), so a later
	// failure aborts before any state changes. Priority across ALL
	// targeted properties is fixed: masked-type violations outrank plain
	// invalid supplied values, independent of property ordering.
	type resolved struct {
		name string
		cell rawCell
		drop bool
	}
	resolveds := make([]resolved, 0, len(names))
	var dropped []string
	var invalidSeen bool
	for _, name := range names {
		dt := e.ot.types_[name]
		incoming := values[name]
		v := verdicts[name]
		if !v.writable {
			dropped = append(dropped, name)
			e.addWriteBasis(&tr, name, v)
			continue
		}
		e.addWriteBasis(&tr, name, v)
		final := incoming
		if v.writeMask != nil {
			mv, merr := v.writeMask(sub, incoming)
			if merr != nil {
				err := &DecisionError{ErrMaskedTypeViolation, "write transform on " + name + " failed: " + merr.Error()}
				sortBases(tr.MatchedPropRules)
				e.record("write", sub, instanceID, req, nil, err, tr)
				return nil, err
			}
			// Priority 4: derived value must satisfy declared type.
			if !dt.CheckValue(mv) {
				err := &DecisionError{ErrMaskedTypeViolation, "derived value for " + name + " violates declared type " + dt.String()}
				sortBases(tr.MatchedPropRules)
				e.record("write", sub, instanceID, req, nil, err, tr)
				return nil, err
			}
			final = mv
		} else if !dt.CheckValue(incoming) {
			invalidSeen = true
		}
		resolveds = append(resolveds, resolved{name: name, cell: rawCell{present: true, value: final}})
	}
	if invalidSeen {
		err := &DecisionError{ErrInvalidValueType, "at least one supplied value violates its declared type"}
		sortBases(tr.MatchedPropRules)
		e.record("write", sub, instanceID, req, nil, err, tr)
		return nil, err
	}

	if len(resolveds) == 0 {
		sortBases(tr.MatchedPropRules)
		out := &WriteResult{InstanceID: instanceID, Applied: nil, Dropped: dropped, Version: inst.Version, Trace: tr}
		e.record("write", sub, instanceID, req, out, nil, tr)
		return out, nil
	}

	applied := make([]string, 0, len(resolveds))
	committed, merr := e.db.mutate(instanceID, func(cur *Instance) (map[string]rawCell, *DecisionError) {
		cells := cur.cells
		// Defensive copy so a panic inside never corrupts the map.
		next := make(map[string]rawCell, len(cells))
		for k, c := range cells {
			next[k] = c
		}
		for _, r := range resolveds {
			next[r.name] = r.cell
			applied = append(applied, r.name)
		}
		return next, nil
	})
	if merr != nil {
		e.record("write", sub, instanceID, req, nil, merr, tr)
		return nil, merr
	}
	sortBases(tr.MatchedPropRules)
	out := &WriteResult{InstanceID: instanceID, Applied: applied, Dropped: dropped, Version: committed.Version, Trace: tr}
	e.record("write", sub, instanceID, req, out, nil, tr)
	return out, nil
}
