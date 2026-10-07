package ontology

import "sort"

// naiveResolve is an independently written reference implementation of the
// two-layer overlay rules. It iterates every membership explicitly and never
// shares helper functions with resolver, so test cross-checks can catch
// implementation mistakes in either side.
func naiveResolve(snap *snapshot, subject SubjectID, lt LinkType, fromType, toType ObjectType) VerdictResult {
	out := VerdictResult{Subject: subject, LinkType: lt, FromType: fromType, ToType: toType}
	if subject == "" {
		out.Verdict = VerdictInvalid
		out.Reason = "invalid-subject"
		return out
	}

	type entry struct {
		group GroupID
		prio  int
		dec   Decision
	}
	collect := func(table map[GroupID]map[string]Decision, key string) []entry {
		var entries []entry
		members, ok := snap.membership[subject]
		if !ok {
			return entries
		}
		for g := range members {
			grp, ok := snap.groups[g]
			if !ok {
				continue
			}
			if decs, ok := table[g]; ok {
				if d, ok := decs[key]; ok {
					entries = append(entries, entry{group: g, prio: grp.Priority, dec: d})
				}
			}
		}
		return entries
	}
	win := func(entries []entry) layerVote {
		if len(entries) == 0 {
			return layerVote{}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].prio > entries[j].prio })
		top := entries[0].prio
		vote := layerVote{declared: true, priority: top, decision: Allow}
		anyDeny := false
		anyAllow := false
		var gids []GroupID
		for _, e := range entries {
			if e.prio != top {
				break
			}
			gids = append(gids, e.group)
			if e.dec == Deny {
				anyDeny = true
			}
			if e.dec == Allow {
				anyAllow = true
			}
		}
		sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
		if anyDeny {
			vote.decision = Deny
		}
		vote.conflict = anyAllow && anyDeny
		vote.groups = gids
		return vote
	}

	linkTable := map[GroupID]map[string]Decision{}
	for g, d := range snap.linkDecls {
		m := map[string]Decision{}
		for k, v := range d {
			m[string(k)] = v
		}
		linkTable[g] = m
	}
	objTable := map[GroupID]map[string]Decision{}
	for g, d := range snap.objectDecls {
		m := map[string]Decision{}
		for k, v := range d {
			m[string(k)] = v
		}
		objTable[g] = m
	}

	out.Link = win(collect(linkTable, string(lt)))
	out.FromObjectVote = win(collect(objTable, string(fromType)))
	out.ToObjectVote = win(collect(objTable, string(toType)))

	if out.Link.declared {
		otherDeny := func(v layerVote) bool {
			if !v.declared || v.decision != Deny || v.priority != out.Link.priority {
				return false
			}
			for _, g := range v.groups {
				found := false
				for _, lg := range out.Link.groups {
					if lg == g {
						found = true
					}
				}
				if !found {
					return true
				}
			}
			return false
		}
		if out.Link.decision == Allow && (otherDeny(out.FromObjectVote) || otherDeny(out.ToObjectVote)) {
			out.ObjectMerged = objectFallback(out.FromObjectVote, out.ToObjectVote)
			out.Verdict = VerdictAmbiguous
			out.Reason = "ambiguous:equal-priority link-allow vs object-deny"
			return out
		}
		if out.Link.decision == Allow {
			out.Verdict = VerdictAllow
			out.Reason = "link-layer-allow"
		} else {
			out.Verdict = VerdictDeny
			out.Reason = "link-layer-deny"
		}
		if out.Link.conflict {
			out.Reason += ";tier-deny-wins"
		}
		return out
	}

	out.ObjectMerged = objectFallback(out.FromObjectVote, out.ToObjectVote)
	if out.ObjectMerged == Unset {
		out.Verdict = VerdictDeny
		out.Reason = "default-deny"
		return out
	}
	if out.ObjectMerged == Allow {
		out.Verdict = VerdictAllow
		out.Reason = "object-layer-allow"
	} else {
		out.Verdict = VerdictDeny
		out.Reason = "object-layer-deny"
	}
	if out.FromObjectVote.conflict || out.ToObjectVote.conflict {
		out.Reason += ";tier-deny-wins"
	}
	return out
}
