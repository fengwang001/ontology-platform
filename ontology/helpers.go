package ontology

import "sort"

func (p *Platform) addAdj(id InstanceID, k linkKey) {
	m, ok := p.adj[id]
	if !ok {
		m = map[linkKey]struct{}{}
		p.adj[id] = m
	}
	m[k] = struct{}{}
}

func (p *Platform) delAdj(id InstanceID, k linkKey) {
	if m, ok := p.adj[id]; ok {
		delete(m, k)
		if len(m) == 0 {
			delete(p.adj, id)
		}
	}
}

// recordJournal appends an entry; the caller must hold p.mu for Seq ordering.
func (p *Platform) recordJournal(e JournalEntry) {
	if p.journal == nil {
		return
	}
	e.Seq = int64(len(p.journal.entries)) + 1
	p.journal.Append(e)
}

func finalLinkRecords(final map[linkKey]struct{}, touched map[TypeName]struct{}) []LinkRecord {
	out := make([]LinkRecord, 0)
	for k := range final {
		if _, ok := touched[k.link]; !ok {
			continue
		}
		out = append(out, LinkRecord{Link: string(k.link), A: string(k.a), B: string(k.b)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Link != out[j].Link {
			return out[i].Link < out[j].Link
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}
