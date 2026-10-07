package ontology

import (
	"fmt"
	"sort"
)

// ApplyBatch executes one atomic batch update and returns its result.
//
// The batch goes through five strictly ordered phases. A failure in an
// earlier phase suppresses every later phase, so the four failure kinds are
// mutually exclusive:
//
//  0. duplicate declaration (before any locking or state inspection),
//  1. per-instance baseline/version check,
//  2. per-item validation hooks,
//  3. link-cardinality evaluation on the final image,
//  4. single-critical-section publish.
//
// Until phase 4 all work happens on private shadow copies, so a failure in
// phases 0-3 changes no version, no link and no commit-clock state.
func (s *Store) ApplyBatch(in BatchInput) (*BatchResult, error) {
	rec := &JournalRecord{
		Input:           cloneInput(in),
		InstanceTouches: map[InstanceID]int{},
	}

	// Phase 0: duplicate write declarations, checked from items alone so no
	// store state is consulted.
	firstIndex := map[InstanceID]int{}
	for i, item := range in.Items {
		if prior, ok := firstIndex[item.ID]; ok {
			s.abortBeforeLock(rec, "duplicate", duplicateError(i, prior, item.ID))
			return nil, rec.Failure
		}
		firstIndex[item.ID] = i
	}

	// The lock working set contains every written instance and every
	// endpoint of every link delta: link publication touches all of them, so
	// serializability requires holding all of them until publish.
	lockIDs := make([]InstanceID, 0, len(in.Items)*2)
	for _, item := range in.Items {
		lockIDs = append(lockIDs, item.ID)
		for _, d := range item.LinkDeltas {
			lockIDs = append(lockIDs, d.Edge.Source, d.Edge.Target)
		}
	}
	ordered, unlock := s.lockAll(lockIDs)
	rec.LockOrder = append(rec.LockOrder, ordered...)
	defer unlock()

	// All state reads and the final publish take one table critical section.
	// Readers and publishers synchronize on tabMu, removing every externally
	// observable intermediate state.
	s.tabMu.Lock()

	// Snapshot everything the batch may see into a private shadow.
	view := &shadowView{
		instances: map[InstanceID]*Instance{},
		edges:     map[edgeKey]bool{},
	}
	for _, id := range ordered {
		if inst, ok := s.instances[id]; ok {
			view.instances[id] = inst.clone()
			rec.InstanceTouches[id]++
		}
	}

	// Phase 1: per-item baseline checks. Every item carries its own baseline
	// and every item is checked against its own instance.
	for i, item := range in.Items {
		current := view.instances[item.ID]
		var observed Version
		if current != nil {
			observed = current.Version
		}
		rec.Baselines = append(rec.Baselines, BaselineObservation{
			ID:       item.ID,
			Declared: item.Baseline,
			Observed: observed,
			Match:    observed == item.Baseline,
		})
		if observed != item.Baseline {
			s.abortLocked(rec, "version", versionConflictError(i, item.ID, item.Baseline, observed))
			return nil, rec.Failure
		}
	}

	// Structural item validation against the schema.
	proposed := make([]*Instance, len(in.Items))
	for i, item := range in.Items {
		objType, ok := s.registry.object(item.Type)
		if !ok {
			s.rejectValidation(rec, i, "object type %q is not registered", item.Type)
			return nil, rec.Failure
		}
		if item.Create {
			if view.instances[item.ID] != nil {
				s.rejectValidation(rec, i, "instance %q already exists", item.ID)
				return nil, rec.Failure
			}
			proposed[i] = &Instance{
				ID:         item.ID,
				Type:       item.Type,
				Version:    1,
				Properties: cloneProperties(item.Properties),
			}
			continue
		}
		current := view.instances[item.ID]
		if current == nil {
			s.rejectValidation(rec, i, "instance %q does not exist", item.ID)
			return nil, rec.Failure
		}
		if current.Type != objType.Name {
			s.rejectValidation(rec, i,
				"instance %q has type %q, not %q", item.ID, current.Type, item.Type)
			return nil, rec.Failure
		}
		next := current.clone()
		next.Properties = cloneProperties(item.Properties)
		next.Version = current.Version + 1
		proposed[i] = next
	}

	// Fold edge deltas into the shadow edge set. Contradictory declarations
	// on the same edge within one batch are a validation failure.
	edgeOrder := make([]edgeKey, 0)
	for _, item := range in.Items {
		for _, d := range item.LinkDeltas {
			key := edgeKey{d.Edge.Link, d.Edge.Source, d.Edge.Target}
			if prev, seen := view.edges[key]; seen && prev != d.Add {
				s.rejectValidation(rec, indexOfItem(in, item.ID),
					"edge %s:%s->%s is both added and removed in the batch",
					d.Edge.Link, d.Edge.Source, d.Edge.Target)
				return nil, rec.Failure
			}
			if _, seen := view.edges[key]; !seen {
				edgeOrder = append(edgeOrder, key)
			}
			view.edges[key] = d.Add
		}
	}

	// Install proposed images so link typing, hooks and cardinality see the
	// final image rather than any intermediate per-item state.
	for i, item := range in.Items {
		view.instances[item.ID] = proposed[i]
		rec.InstanceTouches[item.ID]++
	}

	// Structural link validation against the schema and the final image.
	for i, item := range in.Items {
		for _, d := range item.LinkDeltas {
			if !d.Add {
				continue
			}
			linkType, ok := s.registry.link(d.Edge.Link)
			if !ok {
				s.rejectValidation(rec, i, "link type %q is not registered", d.Edge.Link)
				return nil, rec.Failure
			}
			src := view.instances[d.Edge.Source]
			dst := view.instances[d.Edge.Target]
			if src == nil || dst == nil {
				s.rejectValidation(rec, i,
					"edge %s:%s->%s references a missing endpoint",
					d.Edge.Link, d.Edge.Source, d.Edge.Target)
				return nil, rec.Failure
			}
			if src.Type != linkType.Source || dst.Type != linkType.Target {
				s.rejectValidation(rec, i,
					"edge %s:%s->%s violates link typing (%s->%s)",
					d.Edge.Link, d.Edge.Source, d.Edge.Target, src.Type, dst.Type)
				return nil, rec.Failure
			}
		}
	}

	// Phase 2: validation hooks, one per item, in item order. Each hook sees
	// the whole post-batch image, never an intermediate per-item state.
	for i, item := range in.Items {
		objType, _ := s.registry.object(item.Type)
		obs := HookObservation{Instance: item.ID, Type: item.Type}
		if objType.Validate.Run != nil {
			reason := objType.Validate.Run(view.Get(item.ID), view)
			obs.Rejected = reason != ""
			obs.Reason = reason
			rec.Hooks = append(rec.Hooks, obs)
			if reason != "" {
				s.rejectValidation(rec, i,
					"hook for type %q rejected instance %q: %s",
					item.Type, item.ID, reason)
				return nil, rec.Failure
			}
			continue
		}
		rec.Hooks = append(rec.Hooks, obs)
	}

	// Phase 3: cardinality on the final image. Degrees are derived per
	// endpoint from that endpoint's adjacency lists plus this batch's own
	// deltas; nothing ever scans the global edge table.
	checks, cardErr := s.checkCardinality(in, view, edgeOrder)
	rec.CardinalityChecks = checks
	if cardErr != nil {
		s.abortLocked(rec, "cardinality", cardErr)
		return nil, cardErr
	}

	// Phase 4: publish. The unlock of tabMu is the single commit point.
	newVersions := s.publish(in, proposed, view, edgeOrder)
	commitSeq := s.commitClock
	s.tabMu.Unlock()

	rec.FinalVersions = newVersions
	s.finishCommit(rec, commitSeq)
	return &BatchResult{
		CommitSeq: commitSeq,
		Versions:  newVersions,
		Record:    cloneRecord(rec),
	}, nil
}

func (s *Store) rejectValidation(rec *JournalRecord, index int, format string, args ...any) {
	s.abortLocked(rec, "validation", validationError(index, fmt.Sprintf(format, args...)))
}

func indexOfItem(in BatchInput, id InstanceID) int {
	for i := range in.Items {
		if in.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// indexOfAnyItemFor attributes an endpoint-level cardinality failure to a
// batch item that mentions that endpoint, preferring the item that writes
// the endpoint itself.
func indexOfAnyItemFor(in BatchInput, endpoint InstanceID) int {
	for i := range in.Items {
		if in.Items[i].ID == endpoint {
			return i
		}
	}
	for i := range in.Items {
		for _, d := range in.Items[i].LinkDeltas {
			if d.Edge.Source == endpoint || d.Edge.Target == endpoint {
				return i
			}
		}
	}
	return 0
}

// checkCardinality evaluates every touched link endpoint against the final
// image. Only endpoints touched by deltas can have changed degrees, and each
// degree is computed from that endpoint's own adjacency list plus the
// batch's deltas — O(touched endpoints and their degrees), never O(total
// instances or O(total edges).
func (s *Store) checkCardinality(in BatchInput, view *shadowView, edgeOrder []edgeKey) ([]CardinalityObservation, *BatchError) {
	checks := []CardinalityObservation{}

	touchedLinks := map[LinkTypeName]struct{}{}
	touchedEndpoints := map[InstanceID]struct{}{}
	for _, key := range edgeOrder {
		touchedLinks[key.link] = struct{}{}
		touchedEndpoints[key.source] = struct{}{}
		touchedEndpoints[key.target] = struct{}{}
	}

	endpointIDs := make([]InstanceID, 0, len(touchedEndpoints))
	for id := range touchedEndpoints {
		endpointIDs = append(endpointIDs, id)
	}
	sort.Slice(endpointIDs, func(i, j int) bool { return endpointIDs[i] < endpointIDs[j] })

	linkNames := make([]LinkTypeName, 0, len(touchedLinks))
	for name := range touchedLinks {
		linkNames = append(linkNames, name)
	}
	sort.Slice(linkNames, func(i, j int) bool { return linkNames[i] < linkNames[j] })

	for _, name := range linkNames {
		linkType, ok := s.registry.link(name)
		if !ok {
			continue
		}
		for _, endpoint := range endpointIDs {
			if linkType.SrcMax > 0 {
				degree := s.finalDegree(view, name, endpoint, true)
				obs := CardinalityObservation{
					Link: name, Endpoint: endpoint, Side: "source",
					Degree: degree, Bound: linkType.SrcMax,
					Satisfied: degree <= int(linkType.SrcMax),
				}
				checks = append(checks, obs)
				if !obs.Satisfied {
					return checks, cardinalityError(indexOfAnyItemFor(in, endpoint),
						fmt.Sprintf("source cardinality of %q at %q is %d > max %d",
							name, endpoint, degree, linkType.SrcMax))
				}
			}
			if linkType.DstMax > 0 {
				degree := s.finalDegree(view, name, endpoint, false)
				obs := CardinalityObservation{
					Link: name, Endpoint: endpoint, Side: "target",
					Degree: degree, Bound: linkType.DstMax,
					Satisfied: degree <= int(linkType.DstMax),
				}
				checks = append(checks, obs)
				if !obs.Satisfied {
					return checks, cardinalityError(indexOfAnyItemFor(in, endpoint),
						fmt.Sprintf("target cardinality of %q at %q is %d > max %d",
							name, endpoint, degree, linkType.DstMax))
				}
			}
		}
	}
	return checks, nil
}

// finalDegree computes one endpoint's degree of one link type in the final
// image. The base degree comes from the endpoint's own adjacency list; batch
// deltas are overlaid on top.
func (s *Store) finalDegree(view *shadowView, link LinkTypeName, endpoint InstanceID, outgoing bool) int {
	degree := 0
	var base map[InstanceID]struct{}
	if outgoing {
		if byLink := s.outAdj[endpoint]; byLink != nil {
			base = byLink[link]
		}
	} else {
		if byLink := s.inAdj[endpoint]; byLink != nil {
			base = byLink[link]
		}
	}
	for other := range base {
		key := edgeKey{link, endpoint, other}
		if !outgoing {
			key = edgeKey{link, other, endpoint}
		}
		if present, overlay := view.edges[key]; overlay {
			if present {
				degree++
			}
			continue
		}
		degree++
	}
	// Batch deltas that add edges previously absent from the base set.
	for key, present := range view.edges {
		if !present || key.link != link {
			continue
		}
		if outgoing && key.source == endpoint {
			if _, inBase := base[key.target]; !inBase {
				degree++
			}
		}
		if !outgoing && key.target == endpoint {
			if _, inBase := base[key.source]; !inBase {
				degree++
			}
		}
	}
	return degree
}
