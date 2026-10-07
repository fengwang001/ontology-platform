package ontology

import (
	"fmt"
	"sort"
)

type plan struct {
	deleted     map[string]struct{}
	expanded    map[string]struct{}
	explicit    map[string]struct{}
	orphanWake  map[string]struct{}
	removed     map[Link]struct{}
	nullified   map[Link]struct{}
	steps       []DeleteStep
	undefined   map[string]struct{}
	dedupProbes int
}

func newPlan(root string) *plan {
	p := &plan{
		deleted:    map[string]struct{}{root: {}},
		expanded:   make(map[string]struct{}),
		explicit:   map[string]struct{}{root: {}},
		orphanWake: make(map[string]struct{}),
		removed:    make(map[Link]struct{}),
		nullified:  make(map[Link]struct{}),
		undefined:  make(map[string]struct{}),
	}
	p.steps = append(p.steps, DeleteStep{
		Kind:     "delete-root",
		ObjectID: root,
		Reason:   "root delete request",
	})
	return p
}

func (s *Store) planDeleteLocked(root string) (*plan, error) {
	return s.planDeleteOrderedLocked(root, true)
}

func (s *Store) planDeleteOrderedLocked(root string, deterministic bool) (*plan, error) {
	p := newPlan(root)

	s.expandExplicit(p, []string{root}, deterministic)

	orphanWorklist := p.drainOrphanWake(deterministic)
	for len(orphanWorklist) > 0 {
		current := orphanWorklist[0]
		orphanWorklist = orphanWorklist[1:]
		p.dedupProbes++
		if _, deleting := p.deleted[current]; deleting {
			continue
		}
		if s.hasSurvivingPreservingIncoming(current, p) {
			p.steps = append(p.steps, DeleteStep{
				Kind:     "retain-orphan-candidate",
				ObjectID: current,
				Reason:   "rechecked against current fixed point and preserved",
			})
			continue
		}

		p.deleted[current] = struct{}{}
		delete(p.explicit, current)
		p.steps = append(p.steps, DeleteStep{
			Kind:     "delete-orphan",
			ObjectID: current,
			Reason:   "no surviving preserving incoming link",
		})

		explicitQueue := s.expandOrphanOutgoing(p, current, deterministic)
		s.expandExplicit(p, explicitQueue, deterministic)
		orphanWorklist = append(orphanWorklist, p.drainOrphanWake(deterministic)...)
	}

	if restriction := s.firstRestriction(p); restriction != nil {
		return p, fmt.Errorf("%w: object %q link %s", ErrDeleteRestricted, restriction.ObjectID, linkKey(restriction.Link))
	}
	if len(p.undefined) > 0 {
		return p, fmt.Errorf("%w: %q", ErrUndefinedLinkType, firstSorted(p.undefined))
	}

	s.collectTerminalLinks(p)
	return p, nil
}

func (s *Store) expandExplicit(p *plan, queue []string, deterministic bool) {
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		p.dedupProbes++
		if _, ok := p.expanded[current]; ok {
			continue
		}
		p.expanded[current] = struct{}{}
		p.explicit[current] = struct{}{}

		links := s.incidentLinks(current)
		if !deterministic {
			reverseLinks(links)
		}
		for _, link := range links {
			config, ok := s.configs[link.Type]
			if !ok {
				p.undefined[link.Type] = struct{}{}
				p.steps = append(p.steps, DeleteStep{
					Kind:     "undefined-link-type",
					ObjectID: current,
					Link:     link,
					Rule:     link.Type,
				})
				continue
			}

			peer := link.Target
			if link.Target == current {
				peer = link.Source
			}
			switch config.OnDelete {
			case Cascade:
				p.removed[link] = struct{}{}
				p.steps = append(p.steps, ruleStep("cascade-remove-link", current, link, config))
				if p.markDeleted(peer) {
					queue = append(queue, peer)
					p.steps = append(p.steps, DeleteStep{
						Kind:     "cascade-peer",
						ObjectID: peer,
						Link:     link,
						Rule:     string(Cascade),
						Reason:   "new explicit delete request",
					})
				}
			case SetNull:
				p.nullified[link] = struct{}{}
				p.removed[link] = struct{}{}
				p.steps = append(p.steps, ruleStep("set-null-link", current, link, config))
			case Restrict:
				p.steps = append(p.steps, ruleStep("defer-restrict-check", current, link, config))
			}

			if config.PreserveOnExists && link.Source == current {
				p.dedupProbes++
				if _, deleting := p.deleted[link.Target]; !deleting {
					p.orphanWake[link.Target] = struct{}{}
				}
			}
		}
	}
}

func (s *Store) expandOrphanOutgoing(p *plan, current string, deterministic bool) []string {
	explicitQueue := []string{}
	links := append([]Link(nil), s.out[current]...)
	if !deterministic {
		reverseLinks(links)
	}
	for _, link := range links {
		config, ok := s.configs[link.Type]
		if !ok {
			p.undefined[link.Type] = struct{}{}
			p.steps = append(p.steps, DeleteStep{
				Kind:     "undefined-link-type",
				ObjectID: current,
				Link:     link,
				Rule:     link.Type,
			})
			continue
		}

		switch config.OnDelete {
		case Cascade:
			p.removed[link] = struct{}{}
			p.steps = append(p.steps, ruleStep("orphan-outgoing-cascade", current, link, config))
			if p.markDeleted(link.Target) {
				explicitQueue = append(explicitQueue, link.Target)
			}
		case SetNull:
			p.nullified[link] = struct{}{}
			p.removed[link] = struct{}{}
			p.steps = append(p.steps, ruleStep("orphan-outgoing-set-null", current, link, config))
		case Restrict:
			p.steps = append(p.steps, ruleStep("defer-orphan-outgoing-restrict", current, link, config))
		}

		if config.PreserveOnExists && link.Source == current {
			p.orphanWake[link.Target] = struct{}{}
		}
	}
	return explicitQueue
}

func (p *plan) markDeleted(id string) bool {
	p.dedupProbes++
	if _, exists := p.deleted[id]; exists {
		return false
	}
	p.deleted[id] = struct{}{}
	p.explicit[id] = struct{}{}
	delete(p.orphanWake, id)
	return true
}

func (p *plan) drainOrphanWake(deterministic bool) []string {
	ids := sortedIDs(p.orphanWake)
	if !deterministic && len(ids) > 1 {
		reverseStringSlice(ids)
	}
	for _, id := range ids {
		delete(p.orphanWake, id)
	}
	return ids
}

func (s *Store) incidentLinks(id string) []Link {
	links := uniqueLinks(append(append([]Link(nil), s.out[id]...), s.in[id]...))
	return sortedLinks(links)
}

func (s *Store) hasSurvivingPreservingIncoming(target string, p *plan) bool {
	for _, link := range s.in[target] {
		config, ok := s.configs[link.Type]
		if !ok || !config.PreserveOnExists {
			continue
		}
		if _, sourceDeleted := p.deleted[link.Source]; sourceDeleted {
			continue
		}
		if _, nullified := p.nullified[link]; nullified {
			continue
		}
		return true
	}
	return false
}

func (s *Store) firstRestriction(p *plan) *DeleteStep {
	restrictions := make([]DeleteStep, 0)
	for _, deletedID := range sortedIDs(p.deleted) {
		links := s.incidentLinks(deletedID)
		if _, explicit := p.explicit[deletedID]; !explicit {
			links = append([]Link(nil), s.out[deletedID]...)
		}
		for _, link := range links {
			config, ok := s.configs[link.Type]
			if !ok || config.OnDelete != Restrict {
				continue
			}
			peer := link.Target
			if link.Target == deletedID {
				peer = link.Source
			}
			if _, peerDeleted := p.deleted[peer]; peerDeleted {
				p.steps = append(p.steps, ruleStep("restrict-peer-deleted", deletedID, link, config))
				continue
			}
			remaining := s.countSurvivingSameTypeIncoming(peer, link, p)
			p.steps = append(p.steps, DeleteStep{
				Kind:     "restrict-check",
				ObjectID: deletedID,
				Link:     link,
				Rule:     string(Restrict),
				Reason:   fmt.Sprintf("peer=%q remaining-incoming=%d", peer, remaining),
			})
			if remaining > 0 {
				restrictions = append(restrictions, DeleteStep{
					Kind:     "restrict",
					ObjectID: deletedID,
					Link:     link,
					Rule:     string(Restrict),
					Reason:   fmt.Sprintf("peer %q still has %d same-type incoming links", peer, remaining),
				})
			}
		}
	}
	if len(restrictions) == 0 {
		return nil
	}
	return &restrictions[0]
}

func (s *Store) countSurvivingSameTypeIncoming(peer string, ignored Link, p *plan) int {
	count := 0
	for _, link := range s.in[peer] {
		if link.Type != ignored.Type || link == ignored {
			continue
		}
		if _, deleted := p.deleted[link.Source]; deleted {
			continue
		}
		if _, deleted := p.deleted[link.Target]; deleted {
			continue
		}
		count++
	}
	return count
}

func (s *Store) collectTerminalLinks(p *plan) {
	for id := range p.deleted {
		links := s.incidentLinks(id)
		if _, explicit := p.explicit[id]; !explicit {
			links = append([]Link(nil), s.out[id]...)
		}
		for _, link := range links {
			if _, null := p.nullified[link]; null {
				delete(p.removed, link)
				continue
			}
			p.removed[link] = struct{}{}
		}
	}
	for id := range p.deleted {
		for _, link := range s.incidentLinks(id) {
			if _, null := p.nullified[link]; null {
				continue
			}
			if _, removed := p.removed[link]; removed {
				continue
			}
			p.removed[link] = struct{}{}
		}
	}
}

func ruleStep(kind string, objectID string, link Link, config LinkTypeConfig) DeleteStep {
	return DeleteStep{
		Kind:     kind,
		ObjectID: objectID,
		Link:     link,
		Rule:     string(config.OnDelete),
		Reason:   fmt.Sprintf("preserve=%t", config.PreserveOnExists),
	}
}

func uniqueLinks(links []Link) []Link {
	seen := make(map[Link]struct{}, len(links))
	result := make([]Link, 0, len(links))
	for _, link := range links {
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		result = append(result, link)
	}
	return result
}

func firstSorted(values map[string]struct{}) string {
	return sortedIDs(values)[0]
}

func sortedIDs(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func reverseLinks(links []Link) {
	for left, right := 0, len(links)-1; left < right; left, right = left+1, right-1 {
		links[left], links[right] = links[right], links[left]
	}
}

func reverseStringSlice(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func linkKey(link Link) string {
	return fmt.Sprintf("%s:%s->%s", link.Type, link.Source, link.Target)
}
