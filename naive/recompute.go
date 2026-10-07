// Package naive implements an intentionally simple, independent reference
// model: every answer is recomputed from scratch by scanning the raw Store.
// It shares no code and no state with the incremental engine, so agreement
// between the two is a genuine cross-check.
package naive

import (
	"sort"

	"ontology/ontology"
)

type Model struct {
	view ontology.ViewDef
}

func New(v ontology.ViewDef) *Model { return &Model{view: v} }

// GroupAggregate recomputes one group by a full scan of the store.
func (m *Model) GroupAggregate(s *ontology.Store, groupID string) ontology.Aggregate {
	aggs := m.AggregatedIDs(s)
	var out ontology.Aggregate
	for _, aggID := range aggs {
		if !s.LinkExists(m.view.LinkType, aggID, groupID) {
			continue
		}
		opt, _ := s.GetProperty(m.view.AggType, aggID, m.view.ValueProperty)
		if !opt.Present {
			continue
		}
		out.Sum += opt.Value
		out.Count++
	}
	return out
}

// Memberships recomputes the groups an instance belongs to.
func (m *Model) Memberships(s *ontology.Store, aggID string) []string {
	return s.Memberships(m.view.LinkType, aggID)
}

// AllGroups recomputes aggregates for every still-existing group instance.
func (m *Model) AllGroups(s *ontology.Store) map[string]ontology.Aggregate {
	out := map[string]ontology.Aggregate{}
	aggs := m.AggregatedIDs(s)
	type part struct {
		val     float64
		present bool
	}
	byAgg := make(map[string]part, len(aggs))
	for _, id := range aggs {
		opt, _ := s.GetProperty(m.view.AggType, id, m.view.ValueProperty)
		byAgg[id] = part{val: opt.Value, present: opt.Present}
	}
	seen := map[string]bool{}
	// memberships drive which group keys exist; include existing empty groups too.
	for _, aggID := range aggs {
		for _, g := range s.Memberships(m.view.LinkType, aggID) {
			seen[g] = true
		}
	}
	for g := range seen {
		var a ontology.Aggregate
		for _, aggID := range aggs {
			if !s.LinkExists(m.view.LinkType, aggID, g) {
				continue
			}
			p := byAgg[aggID]
			if !p.present {
				continue
			}
			a.Sum += p.val
			a.Count++
		}
		out[g] = a
	}
	return out
}

// AggregatedIDs lists every currently existing instance of the view's
// aggregated object type. Because Store does not expose enumeration, the naive
// model needs the full universe of aggregated instance IDs ever created.
func (m *Model) AggregatedIDs(s *ontology.Store) []string {
	return s.ObjectIDs(m.view.AggType)
}

// SortedAggregate returns sum/count with deterministic key order helpers.
func SortedGroups(agg map[string]ontology.Aggregate) []string {
	keys := make([]string, 0, len(agg))
	for k := range agg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
