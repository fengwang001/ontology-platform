package lease

import "sort"

// chainOps owns structural operations on the sublease forest.

func createChild(leases map[LeaseID]Lease, op CreateSubleaseOp) Lease {
	parent := leases[op.Parent]
	return Lease{
		ID:       op.ID,
		Landlord: parent.Tenant,
		Tenant:   op.Tenant,
		Parent:   parent.ID,
		Start:    op.Start,
		End:      op.End,
		Rent:     op.Rent,
		Active:   true,
		Depth:    parent.Depth + 1,
		Root:     parent.Root,
	}
}

// cascadeTermination terminates l and walks its (linear, by invariant)
// descendant chain. A recognized descendant whose term continues past day is
// promoted: it becomes a direct lease under the head landlord, starts a new
// root, and its subtree shifts up one level; recognition below an already
// promoted ancestor stays latent (its chain is intact). Termination day is
// within l's term (precondition checked by the caller).
func cascadeTermination(leases map[LeaseID]Lease, headLandlord Party, l Lease, day int) {
	l.Active = false
	if day < l.End {
		l.End = day
	}
	leases[l.ID] = l

	cut := true // no surviving ancestor between the walk and the head landlord
	depth := 0
	var newRoot LeaseID
	cur, ok := activeChildDeterministic(leases, l.ID)
	for ok {
		next, hasNext := activeChildDeterministic(leases, cur.ID)
		switch {
		case cut && cur.Recognized && cur.Start <= day && cur.End > day:
			cur.Parent = 0
			cur.Depth = 1
			cur.Root = cur.ID
			cur.Landlord = headLandlord
			leases[cur.ID] = cur
			cut = false
			newRoot = cur.ID
			depth = 2
		case cut:
			cur.Active = false
			if day < cur.End {
				cur.End = day
			}
			leases[cur.ID] = cur
		default:
			cur.Depth = depth
			cur.Root = newRoot
			leases[cur.ID] = cur
			depth++
		}
		cur, ok = next, hasNext
	}
}

func activeChildCount(leases map[LeaseID]Lease, parent LeaseID) int {
	n := 0
	for _, k := range leases {
		if k.Parent == parent && k.Active {
			n++
		}
	}
	return n
}

func activeChild(leases map[LeaseID]Lease, parent LeaseID) (Lease, bool) {
	for _, k := range leases {
		if k.Parent == parent && k.Active {
			return k, true
		}
	}
	return Lease{}, false
}

// activeChildDeterministic returns the unique active child (invariant),
// breaking ties deterministically by ID so the code stays replay-identical
// even if an invariant were transiently violated during a transition.
func activeChildDeterministic(leases map[LeaseID]Lease, parent LeaseID) (Lease, bool) {
	var ids []LeaseID
	for _, k := range leases {
		if k.Parent == parent && k.Active {
			ids = append(ids, k.ID)
		}
	}
	if len(ids) == 0 {
		return Lease{}, false
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return leases[ids[0]], true
}

// ancestors returns [lease, parent, ..., root], cost O(depth).
func ancestors(leases map[LeaseID]Lease, id LeaseID) []Lease {
	var out []Lease
	for cur := id; cur != 0; {
		l, ok := leases[cur]
		if !ok {
			return nil
		}
		out = append(out, l)
		if l.Parent == 0 {
			break
		}
		cur = l.Parent
	}
	return out
}

// liabilityChain returns [debtor lease, ..., root], i.e. the chain along which
// joint liability travels. It is fixed when arrears are raised so later
// promotions/terminations never rewrite history.
func liabilityChain(leases map[LeaseID]Lease, id LeaseID) []LeaseID {
	chain := ancestors(leases, id)
	out := make([]LeaseID, len(chain))
	for i, l := range chain {
		out[i] = l.ID
	}
	return out
}
