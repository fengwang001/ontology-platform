package ontology

// ReadMany returns a single consistent view of several instances. All reads in
// one call share the same tick: a reader can never observe two instances from
// different points in commit history.
func (p *Platform) ReadMany(ids ...InstanceID) []Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		s := p.readLocked(id)
		s.Tick = p.tick
		out = append(out, s)
	}
	return out
}

// LinkSnapshot is one existing link, as exported for verification/replay.
type LinkSnapshot struct {
	Link TypeName
	A, B InstanceID
}

// ExportLinks returns all links whose type is in types (or all links if types
// is empty). It is intended for tests and journal replay verification.
func (p *Platform) ExportLinks(types ...TypeName) []LinkSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	want := make(map[TypeName]struct{}, len(types))
	for _, t := range types {
		want[t] = struct{}{}
	}
	out := make([]LinkSnapshot, 0, len(p.links))
	for k := range p.links {
		if len(want) > 0 {
			if _, ok := want[k.link]; !ok {
				continue
			}
		}
		out = append(out, LinkSnapshot{Link: k.link, A: k.a, B: k.b})
	}
	return out
}

// InstanceCount returns the number of live instances.
func (p *Platform) InstanceCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.instances)
}
