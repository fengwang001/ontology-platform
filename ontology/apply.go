package ontology

import "sort"

func (s *Store) applyPlanLocked(p *plan) {
	for id := range p.deleted {
		delete(s.objects, id)
		delete(s.out, id)
		delete(s.in, id)
	}

	remainingLinks := make([]Link, 0, len(s.links))
	for _, link := range s.links {
		_, sourceDeleted := p.deleted[link.Source]
		_, targetDeleted := p.deleted[link.Target]
		_, removed := p.removed[link]
		_, nullified := p.nullified[link]
		if sourceDeleted || targetDeleted || removed || nullified {
			continue
		}
		remainingLinks = append(remainingLinks, link)
	}

	s.links = remainingLinks
	s.out = make(map[string][]Link)
	s.in = make(map[string][]Link)
	for _, link := range s.links {
		s.out[link.Source] = append(s.out[link.Source], link)
		s.in[link.Target] = append(s.in[link.Target], link)
	}
}

func (p *plan) result() DeleteResult {
	result := DeleteResult{
		DeletedObjects: sortedIDs(p.deleted),
		RemovedLinks:   sortedLinks(linkSetValues(p.removed)),
		NullifiedLinks: sortedLinks(linkSetValues(p.nullified)),
		Steps:          append([]DeleteStep(nil), p.steps...),
	}

	nullified := make(map[Link]struct{}, len(p.nullified))
	for link := range p.nullified {
		nullified[link] = struct{}{}
	}
	result.RemovedLinks = filterLinks(result.RemovedLinks, nullified)
	sort.SliceStable(result.Steps, func(i, j int) bool {
		if result.Steps[i].ObjectID != result.Steps[j].ObjectID {
			return result.Steps[i].ObjectID < result.Steps[j].ObjectID
		}
		return stepKey(result.Steps[i]) < stepKey(result.Steps[j])
	})
	return result
}

func linkSetValues(values map[Link]struct{}) []Link {
	result := make([]Link, 0, len(values))
	for link := range values {
		result = append(result, link)
	}
	return result
}

func filterLinks(links []Link, excluded map[Link]struct{}) []Link {
	result := make([]Link, 0, len(links))
	for _, link := range links {
		if _, ok := excluded[link]; !ok {
			result = append(result, link)
		}
	}
	return result
}

func stepKey(step DeleteStep) string {
	return step.Kind + "|" + step.Rule + "|" + linkKey(step.Link) + "|" + step.Reason
}
