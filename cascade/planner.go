package cascade

import "sort"

// Step records one planning decision for audit logging.
type Step struct {
	Kind   string // "root", "cascade", "orphan", "restrict", "undefined"
	Object string
	LinkID string
	Reason string
}

// Plan is the order-independent fixed-point result of a deletion request.
type Plan struct {
	Deleted      map[string]bool
	Normal       map[string]bool
	RemovedLinks map[string]bool
	Steps        []Step
	// DedupProbes counts probes against the "already scheduled" set that
	// guarantee termination. Its expected value is O(1) per member and is
	// independent of the total number of untouched objects/links.
	DedupProbes int
}

const (
	stepRoot      = "root"
	stepCascade   = "cascade"
	stepOrphan    = "orphan"
	stepRestrict  = "restrict"
	stepUndefined = "undefined"
)

// plan computes the least deletion set for root on the immutable snapshot.
// It never mutates the store; the engine applies the plan atomically or
// discards it, which provides atomicity without a rollback mechanism.
//
// Termination: Deleted grows monotonically and is bounded by the finite
// object set; each iteration either adds at least one object or stops.
//
// Order independence: the algorithm computes the least fixed point of two
// monotone operators (rule-driven closure and orphan closure), so any
// legal processing order converges on the same set. Ties are broken by
// sorted IDs purely so the audit log itself is also reproducible.
func plan(cfg *Config, snap snapshot, root string) (*Plan, *CascadeError) {
	if !snap.objects[root] {
		return nil, &CascadeError{Code: ErrObjectNotFound, Detail: root}
	}

	p := &Plan{
		Deleted:      map[string]bool{root: true},
		Normal:       map[string]bool{root: true},
		RemovedLinks: map[string]bool{},
	}
	p.Steps = append(p.Steps, Step{Kind: stepRoot, Object: root, Reason: "requested"})

	// affected links are precomputed once; the snapshot never changes.
	for changed := true; changed; {
		changed = false

		// Operator 1: rule-driven cascades.
		// Normal deletions (root and every cascade target) apply rules to
		// both incident directions. Orphan deletions apply rules to their
		// own outgoing links only; their incoming links simply vanish with
		// the object. If an orphan is later reached as a cascade target it
		// is promoted to Normal, and the closure below processes its
		// incoming links too, so the least fixed point stays unique.
		for _, obj := range sortedSet(p.Deleted) {
			links := outgoing(snap.links, obj)
			if p.Normal[obj] {
				links = incident(snap.links, obj)
			}
			for _, l := range links {
				if p.RemovedLinks[l.ID] {
					continue
				}
				rule, ok := cfg.ruleFor(l, obj)
				if !ok {
					continue
				}
				p.RemovedLinks[l.ID] = true
				other := opposite(l, obj)
				switch rule {
				case RuleCascade:
					p.probe()
					if !p.Deleted[other] && snap.objects[other] {
						p.Deleted[other] = true
						p.Normal[other] = true
						p.Steps = append(p.Steps, Step{Kind: stepCascade, Object: other, LinkID: l.ID, Reason: l.Type})
						changed = true
					}
				case RuleSetNull:
					// link removed, opposite object retained.
				case RuleRestrict:
					// finalized after the fixed point is reached.
				}
			}
		}

		// Operator 2: orphan cleanup. Only objects that lose keep-alive
		// support *because* of this request can become candidates: an object
		// is orphan when it has at least one keep-alive inbound link in the
		// snapshot whose source is deleted, and no keep-alive inbound link
		// from a surviving source after removals. Pre-existing orphans are
		// untouched (they existed independently of the request).
		candidates := map[string]bool{}
		for _, l := range snap.links {
			def, ok := cfg.types[l.Type]
			if !ok || !def.KeepAlive || !p.Deleted[l.Src] || p.Deleted[l.Dst] {
				continue
			}
			candidates[l.Dst] = true
		}
		for _, obj := range sortedSet(candidates) {
			if len(cfg.keepers(snap.links, obj, p.Deleted)) == 0 {
				p.probe()
				if !p.Deleted[obj] {
					p.Deleted[obj] = true
					p.Steps = append(p.Steps, Step{Kind: stepOrphan, Object: obj, Reason: "no keep-alive inbound link"})
					changed = true
				}
			}
		}
	}

	// Fixed point reached. Error precedence: restrict before undefined.
	restrictLinks := []Link{}
	undefined := []Link{}
	for _, l := range snap.links {
		if !p.Deleted[l.Src] && !p.Deleted[l.Dst] {
			continue
		}
		// Which endpoints actually invoke a rule for this link. The
		// outgoing rule applies for every deleted source (cascade targets
		// and orphans alike); the incoming rule applies only when the
		// target is deleted as a full deletion request (normal), since
		// orphan cleanup applies rules to outgoing links only.
		if p.Deleted[l.Src] {
			if rule, ok := cfg.types[l.Type]; ok {
				if rule.OutRule == RuleRestrict && !p.Deleted[l.Dst] {
					restrictLinks = append(restrictLinks, l)
				}
			}
		}
		if p.Normal[l.Dst] {
			if rule, ok := cfg.types[l.Type]; ok {
				if rule.InRule == RuleRestrict && !p.Deleted[l.Src] {
					restrictLinks = append(restrictLinks, l)
				}
			}
		}
		if _, ok := cfg.types[l.Type]; !ok {
			undefined = append(undefined, l)
		}
	}
	sortLinks(restrictLinks)
	sortLinks(undefined)
	if len(restrictLinks) > 0 {
		l := restrictLinks[0]
		p.Steps = append(p.Steps, Step{Kind: stepRestrict, LinkID: l.ID, Reason: l.Type})
		return nil, &CascadeError{Code: ErrRestricted, Detail: l.ID}
	}
	if len(undefined) > 0 {
		l := undefined[0]
		p.Steps = append(p.Steps, Step{Kind: stepUndefined, LinkID: l.ID, Reason: l.Type})
		return nil, &CascadeError{Code: ErrUndefinedLinkType, Detail: l.Type}
	}

	// Every link touching a deleted object disappears; ensure the removed
	// set is complete regardless of which operator added each object.
	for _, l := range snap.links {
		if p.Deleted[l.Src] || p.Deleted[l.Dst] {
			p.RemovedLinks[l.ID] = true
		}
	}
	return p, nil
}

func (p *Plan) probe() { p.DedupProbes++ }

func opposite(l Link, obj string) string {
	if l.Src == obj {
		return l.Dst
	}
	return l.Src
}

func incident(links []Link, obj string) []Link {
	out := []Link{}
	for _, l := range links {
		if l.Src == obj || l.Dst == obj {
			out = append(out, l)
		}
	}
	sortLinks(out)
	return out
}

func outgoing(links []Link, obj string) []Link {
	out := []Link{}
	for _, l := range links {
		if l.Src == obj {
			out = append(out, l)
		}
	}
	sortLinks(out)
	return out
}

func sortLinks(ls []Link) {
	sort.Slice(ls, func(i, j int) bool { return ls[i].ID < ls[j].ID })
}
