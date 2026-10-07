package ontology

import "sort"

// stageOps applies every operation to the shadow world. Schema violations
// (unknown link type or endpoint type mismatch) are reported through the
// cardinality gate, which is the first stage that inspects the schema.
func (s *Store) stageOps(b Batch, shadows map[ID]*instance) []CardinalityFailure {
	var violations []CardinalityFailure
	for _, op := range b.Ops {
		target := shadows[op.Instance]
		switch op.Kind {
		case OpSetAttr:
			target.attrs[op.Attr] = op.Value
		case OpAddLink, OpRemoveLink:
			other := shadows[op.Other]
			lt, ok := s.linkTypes[op.LinkType]
			if !ok {
				violations = append(violations, CardinalityFailure{
					LinkType: op.LinkType, Endpoint: "unknown_link_type", Instance: op.Instance,
				})
				continue
			}
			if target.typ != lt.SourceType || other.typ != lt.TargetType {
				violations = append(violations, CardinalityFailure{
					LinkType: op.LinkType, Endpoint: "endpoint_type",
					Instance: op.Instance, Count: -1,
				})
				continue
			}
			if op.Kind == OpAddLink {
				addEdge(target.out, other.in, op.LinkType, op.Other, op.Instance)
			} else {
				removeEdge(target.out, other.in, op.LinkType, op.Other, op.Instance)
			}
		}
	}
	sortFailures(violations)
	return violations
}

// checkCardinality validates the bounds of every degree this batch changes.
// Work is proportional to the number of distinct (instance, link type,
// endpoint) triples in the batch, never to the store size.
func (s *Store) checkCardinality(b Batch, shadows map[ID]*instance) []CardinalityFailure {
	changed := map[degreeKey]struct{}{}
	for _, op := range b.Ops {
		if op.Kind != OpAddLink && op.Kind != OpRemoveLink {
			continue
		}
		if _, ok := s.linkTypes[op.LinkType]; !ok {
			continue
		}
		changed[degreeKey{op.Instance, op.LinkType, "source"}] = struct{}{}
		changed[degreeKey{op.Other, op.LinkType, "target"}] = struct{}{}
	}
	var violations []CardinalityFailure
	for k := range changed {
		lt := s.linkTypes[k.link]
		shadow := shadows[k.id]
		if k.endpoint == "source" {
			count := len(shadow.out[lt.Name])
			if count < lt.MinSource || (lt.MaxSource >= 0 && count > lt.MaxSource) {
				violations = append(violations, boundFailure(lt.Name, "source", k.id, count, lt.MinSource, lt.MaxSource))
			}
		} else {
			count := len(shadow.in[lt.Name])
			if count < lt.MinTarget || (lt.MaxTarget >= 0 && count > lt.MaxTarget) {
				violations = append(violations, boundFailure(lt.Name, "target", k.id, count, lt.MinTarget, lt.MaxTarget))
			}
		}
	}
	sortFailures(violations)
	return violations
}

type degreeKey struct {
	id       ID
	link     string
	endpoint string
}

func sortFailures(v []CardinalityFailure) {
	sort.Slice(v, func(i, j int) bool {
		if v[i].LinkType != v[j].LinkType {
			return v[i].LinkType < v[j].LinkType
		}
		if v[i].Endpoint != v[j].Endpoint {
			return v[i].Endpoint < v[j].Endpoint
		}
		return v[i].Instance < v[j].Instance
	})
}

func boundFailure(linkType, endpoint string, id ID, count, min, max int) CardinalityFailure {
	return CardinalityFailure{
		LinkType: linkType, Endpoint: endpoint, Instance: id,
		Count: count, Min: min, Max: max,
	}
}

func addEdge(out, in map[string]map[ID]struct{}, link string, target, source ID) {
	if out[link] == nil {
		out[link] = map[ID]struct{}{}
	}
	out[link][target] = struct{}{}
	if in[link] == nil {
		in[link] = map[ID]struct{}{}
	}
	in[link][source] = struct{}{}
}

func removeEdge(out, in map[string]map[ID]struct{}, link string, target, source ID) {
	if peers, ok := out[link]; ok {
		delete(peers, target)
	}
	if peers, ok := in[link]; ok {
		delete(peers, source)
	}
}

// cloneInstance deep-copies the mutable maps of one instance. The mutex is
// intentionally not copied.
func cloneInstance(it *instance) *instance {
	attrs := make(map[Attr]Value, len(it.attrs))
	for k, v := range it.attrs {
		attrs[k] = v
	}
	cloneLinks := func(m map[string]map[ID]struct{}) map[string]map[ID]struct{} {
		out := make(map[string]map[ID]struct{}, len(m))
		for link, peers := range m {
			peersCopy := make(map[ID]struct{}, len(peers))
			for peer := range peers {
				peersCopy[peer] = struct{}{}
			}
			out[link] = peersCopy
		}
		return out
	}
	return &instance{
		id:      it.id,
		typ:     it.typ,
		version: it.version,
		step:    it.step,
		attrs:   attrs,
		out:     cloneLinks(it.out),
		in:      cloneLinks(it.in),
	}
}

// publishInstance copies the shadow state onto the live instance. Version
// is already bumped on the shadow; the per-instance step remains owned by
// the live instance so independent promotion rules survive.
func publishInstance(live, shadow *instance) {
	live.version = shadow.version
	live.attrs = shadow.attrs
	live.out = shadow.out
	live.in = shadow.in
}
