// Package naive provides an independent reference implementation of the
// joint-version-batch semantics: every Commit runs under one global lock,
// performs the staged checks and applies the batch in a single critical
// section. It keeps its own instance representation and decision logic and
// is used as the oracle that the concurrent ontology.Store is fuzzed
// against: every interleaved history must equal some serial order of these
// fully-serial commits.
package naive

import (
	"sort"
	"sync"

	"ontology/ontology"
)

// nInstance is the reference engine's own state representation.
type nInstance struct {
	typ     string
	version uint64
	step    uint64
	attrs   map[ontology.Attr]ontology.Value
	out     map[string]map[ontology.ID]struct{}
	in      map[string]map[ontology.ID]struct{}
}

// Engine is the global-lock reference model.
type Engine struct {
	mu          sync.Mutex
	objectTypes map[string]ontology.ObjectType
	linkTypes   map[string]ontology.LinkType
	instances   map[ontology.ID]*nInstance
}

// New creates an empty reference engine.
func New(cfg ontology.Config) *Engine {
	objectTypes := map[string]ontology.ObjectType{}
	for k, v := range cfg.ObjectTypes {
		objectTypes[k] = v
	}
	linkTypes := map[string]ontology.LinkType{}
	for k, v := range cfg.LinkTypes {
		linkTypes[k] = v
	}
	return &Engine{
		objectTypes: objectTypes,
		linkTypes:   linkTypes,
		instances:   map[ontology.ID]*nInstance{},
	}
}

// CreateInstance inserts a new instance at version 1.
func (e *Engine) CreateInstance(id ontology.ID, typ string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.objectTypes[typ]; !ok {
		return ontology.ErrUnknownType
	}
	if _, ok := e.instances[id]; ok {
		return ontology.ErrExists
	}
	e.instances[id] = &nInstance{
		typ:     typ,
		version: 1,
		step:    1,
		attrs:   map[ontology.Attr]ontology.Value{},
		out:     map[string]map[ontology.ID]struct{}{},
		in:      map[string]map[ontology.ID]struct{}{},
	}
	return nil
}

// Get returns a snapshot of one instance.
func (e *Engine) Get(id ontology.ID) (ontology.Snapshot, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.instances[id]
	if !ok {
		return ontology.Snapshot{}, false
	}
	attrs := map[ontology.Attr]ontology.Value{}
	for k, v := range it.attrs {
		attrs[k] = v
	}
	return ontology.Snapshot{ID: id, Type: it.typ, Version: it.version, Step: it.step, Attrs: attrs}, true
}

// Commit runs one batch inside the single global critical section. The
// algorithm deliberately re-derives every gate independently of the
// production implementation.
func (e *Engine) Commit(b ontology.Batch) ontology.Result {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Gate 1: duplicate precondition.
	declared := map[ontology.ID]uint64{}
	for _, p := range b.Preconditions {
		if _, dup := declared[p.Instance]; dup {
			return ontology.Result{
				BatchID:   b.ID,
				Status:    ontology.StatusDuplicatePrecondition,
				Duplicate: p.Instance,
			}
		}
		declared[p.Instance] = p.ExpectedVersion
	}

	// Touched instance set, sorted only to make the failure lists stable.
	touched := map[ontology.ID]struct{}{}
	for _, p := range b.Preconditions {
		touched[p.Instance] = struct{}{}
	}
	for _, op := range b.Ops {
		touched[op.Instance] = struct{}{}
		if op.Kind == ontology.OpAddLink || op.Kind == ontology.OpRemoveLink {
			touched[op.Other] = struct{}{}
		}
	}

	// Gate 2: joint version predicate over one consistent snapshot.
	var mismatches []ontology.Mismatch
	observed := map[ontology.ID]uint64{}
	for _, p := range b.Preconditions {
		it, ok := e.instances[p.Instance]
		if !ok {
			mismatches = append(mismatches, ontology.Mismatch{
				Instance: p.Instance, Expected: p.ExpectedVersion, Missing: true,
			})
			continue
		}
		observed[p.Instance] = it.version
		if it.version != p.ExpectedVersion {
			mismatches = append(mismatches, ontology.Mismatch{
				Instance: p.Instance, Expected: p.ExpectedVersion, Actual: it.version,
			})
		}
	}
	for id := range touched {
		if _, ok := e.instances[id]; !ok {
			found := false
			for _, m := range mismatches {
				if m.Instance == id && m.Missing {
					found = true
				}
			}
			if !found {
				mismatches = append(mismatches, ontology.Mismatch{Instance: id, Missing: true})
			}
		}
	}
	if len(mismatches) > 0 {
		sortMismatches(mismatches)
		return ontology.Result{
			BatchID:    b.ID,
			Status:     ontology.StatusVersionMismatch,
			Mismatches: mismatches,
			Observed:   observed,
		}
	}

	// Build the would-be post state by deep-copying touched instances.
	next := map[ontology.ID]*nInstance{}
	for id := range touched {
		next[id] = cloneN(e.instances[id])
	}

	var violations []ontology.CardinalityFailure
	changed := map[triple]struct{}{}
	for _, op := range b.Ops {
		target := next[op.Instance]
		switch op.Kind {
		case ontology.OpSetAttr:
			target.attrs[op.Attr] = op.Value
		case ontology.OpAddLink, ontology.OpRemoveLink:
			other := next[op.Other]
			lt, ok := e.linkTypes[op.LinkType]
			if !ok {
				violations = append(violations, ontology.CardinalityFailure{
					LinkType: op.LinkType, Endpoint: "unknown_link_type", Instance: op.Instance,
				})
				continue
			}
			if target.typ != lt.SourceType || other.typ != lt.TargetType {
				violations = append(violations, ontology.CardinalityFailure{
					LinkType: op.LinkType, Endpoint: "endpoint_type", Instance: op.Instance, Count: -1,
				})
				continue
			}
			if op.Kind == ontology.OpAddLink {
				edge(target.out, other.in, op.LinkType, op.Other, op.Instance, true)
			} else {
				edge(target.out, other.in, op.LinkType, op.Other, op.Instance, false)
			}
			changed[triple{op.Instance, op.LinkType, "source"}] = struct{}{}
			changed[triple{op.Other, op.LinkType, "target"}] = struct{}{}
		}
	}

	// Gate 3: cardinality over the hypothetical post state.
	for t := range changed {
		lt := e.linkTypes[t.link]
		shadow := next[t.id]
		if t.endpoint == "source" {
			count := len(shadow.out[lt.Name])
			if count < lt.MinSource || (lt.MaxSource >= 0 && count > lt.MaxSource) {
				violations = append(violations, ontology.CardinalityFailure{
					LinkType: lt.Name, Endpoint: "source", Instance: t.id,
					Count: count, Min: lt.MinSource, Max: lt.MaxSource,
				})
			}
		} else {
			count := len(shadow.in[lt.Name])
			if count < lt.MinTarget || (lt.MaxTarget >= 0 && count > lt.MaxTarget) {
				violations = append(violations, ontology.CardinalityFailure{
					LinkType: lt.Name, Endpoint: "target", Instance: t.id,
					Count: count, Min: lt.MinTarget, Max: lt.MaxTarget,
				})
			}
		}
	}
	if len(violations) > 0 {
		sortViolations(violations)
		return ontology.Result{
			BatchID:     b.ID,
			Status:      ontology.StatusCardinalityViolation,
			Cardinality: violations,
		}
	}

	// Gate 4: commit — install post state and bump each version by its step.
	changes := map[ontology.ID]ontology.VersionChange{}
	for id := range touched {
		live := e.instances[id]
		from := live.version
		to := from + live.step
		shadow := next[id]
		shadow.version = to
		changes[id] = ontology.VersionChange{From: from, To: to, Step: live.step}
		live.attrs = shadow.attrs
		live.out = shadow.out
		live.in = shadow.in
		live.version = to
	}
	return ontology.Result{
		BatchID:  b.ID,
		Status:   ontology.StatusCommitted,
		Versions: changes,
		Observed: observed,
	}
}

type triple struct {
	id       ontology.ID
	link     string
	endpoint string
}

func edge(out, in map[string]map[ontology.ID]struct{}, link string, target, source ontology.ID, add bool) {
	put := func(m map[string]map[ontology.ID]struct{}, peer ontology.ID) {
		set, ok := m[link]
		if !ok {
			set = map[ontology.ID]struct{}{}
			m[link] = set
		}
		if add {
			set[peer] = struct{}{}
		} else {
			delete(set, peer)
		}
	}
	put(out, target)
	put(in, source)
}

func cloneN(it *nInstance) *nInstance {
	attrs := map[ontology.Attr]ontology.Value{}
	for k, v := range it.attrs {
		attrs[k] = v
	}
	clone := func(m map[string]map[ontology.ID]struct{}) map[string]map[ontology.ID]struct{} {
		out := map[string]map[ontology.ID]struct{}{}
		for link, peers := range m {
			set := map[ontology.ID]struct{}{}
			for peer := range peers {
				set[peer] = struct{}{}
			}
			out[link] = set
		}
		return out
	}
	return &nInstance{
		typ:     it.typ,
		version: it.version,
		step:    it.step,
		attrs:   attrs,
		out:     clone(it.out),
		in:      clone(it.in),
	}
}

func sortMismatches(ms []ontology.Mismatch) {
	sort.Slice(ms, func(i, j int) bool { return ms[i].Instance < ms[j].Instance })
}

func sortViolations(vs []ontology.CardinalityFailure) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].LinkType != vs[j].LinkType {
			return vs[i].LinkType < vs[j].LinkType
		}
		if vs[i].Endpoint != vs[j].Endpoint {
			return vs[i].Endpoint < vs[j].Endpoint
		}
		return vs[i].Instance < vs[j].Instance
	})
}
