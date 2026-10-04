// Package alloc orchestrates the two-phase reroute and the read-only Explain.
//
// All exported operations take the cluster lock once for their whole
// duration, so concurrent calls are equivalent to some serial order.
package alloc

import (
	"sort"

	"ontology/decider"
	"ontology/node"
)

// Movement records one placement or migration in occurrence order.
type Movement struct {
	Index   string
	Shard   int
	Primary bool
	From    string // empty for a first-time assignment
	To      string
}

// Result is the Reroute outcome.
type Result struct {
	Assigned []Movement
	Moved    []Movement
}

// Verdict explains one node for one copy.
type Verdict struct {
	NodeID    string
	Reason    decider.Reason // OK pass, otherwise the first vetoing rule
	PrimaryUp bool           // requested copy is a replica whose primary is unassigned
}

// Reroute runs the two-phase allocation against cl.
func Reroute(cl *node.Cluster) Result {
	cl.Lock()
	defer cl.Unlock()
	var evals uint64
	return rerouteLocked(cl, &evals)
}

func rerouteLocked(cl *node.Cluster, evals *uint64) Result {
	res := Result{Assigned: []Movement{}, Moved: []Movement{}}
	placeUnassigned(cl, evals, &res)
	migrate(cl, evals, &res)
	return res
}

func perZone(cl *node.Cluster, copies int) int {
	z := cl.ZoneCountLocked()
	if z == 0 {
		return 0
	}
	return (copies + z - 1) / z
}

func candidateFacts(cl *node.Cluster, nv node.NodeView, k node.CopyKey, moving bool,
	pz int) decider.Facts {
	has := false
	for _, cp := range nv.Copies {
		if cp.Index == k.Index && cp.Shard == k.Shard {
			has = true
			break
		}
	}
	return decider.Facts{
		Excluded:           nv.Exclude,
		HasShardCopy:       has,
		ZoneCopies:         cl.ZoneCopiesLocked(nv.Zone, k.Index, k.Shard),
		ZoneCopiesMinusOne: false, // source already physically removed before phase-2 evaluation
		Moving:             moving,
		PerZone:            pz,
		Used:               cl.UsedLocked(nv.ID),
		Total:              nv.Total,
		Size:               k.Size,
		LowPct:             cl.L,
		HighPct:            cl.H,
		Primary:            k.Primary,
	}
}

// chooseTarget returns the best candidate node id. source == "" in phase 1;
// in phase 2 the source node is never a candidate.
// In phase 2 the moving copy has already been physically removed from the
// source, so zone counts and usage already reflect its departure and no extra
// adjustment is applied.
func chooseTarget(cl *node.Cluster, k node.CopyKey, source string, moving bool,
	pz int, evals *uint64) (string, map[string]decider.Reason) {
	verdict := map[string]decider.Reason{}
	bestID := ""
	bestCount := 0
	for _, nv := range cl.NodesLocked() {
		if nv.ID == source {
			continue
		}
		r := decider.Decide(candidateFacts(cl, nv, k, moving, pz), evals)
		verdict[nv.ID] = r
		if r != decider.OK {
			continue
		}
		cnt := cl.CopyCountLocked(nv.ID)
		if bestID == "" || cnt < bestCount || (cnt == bestCount && nv.ID < bestID) {
			bestID, bestCount = nv.ID, cnt
		}
	}
	return bestID, verdict
}

func placeUnassigned(cl *node.Cluster, evals *uint64, res *Result) {
	for _, ix := range cl.IndexesLocked() {
		copies := 1 + ix.R
		pz := perZone(cl, copies)
		for s := 0; s < ix.S; s++ {
			pk := node.CopyKey{Index: ix.Name, Shard: s, Primary: true, Size: ix.Size}
			_, primaryOn := cl.PlacementLocked(pk)
			if !primaryOn {
				target, _ := chooseTarget(cl, pk, "", false, pz, evals)
				if target == "" {
					continue // primary still unassigned: replicas skipped this round
				}
				cl.PutLocked(target, pk)
				res.Assigned = append(res.Assigned, Movement{
					Index: ix.Name, Shard: s, Primary: true, To: target,
				})
			}
			for rep := 0; rep < ix.R; rep++ {
				rk := node.CopyKey{Index: ix.Name, Shard: s, Replica: rep, Primary: false, Size: ix.Size}
				if _, on := cl.PlacementLocked(rk); on {
					continue
				}
				target, _ := chooseTarget(cl, rk, "", false, pz, evals)
				if target == "" {
					continue
				}
				cl.PutLocked(target, rk)
				res.Assigned = append(res.Assigned, Movement{
					Index: ix.Name, Shard: s, Primary: false, To: target,
				})
			}
		}
	}
}

func migrate(cl *node.Cluster, evals *uint64, res *Result) {
	for _, nv := range cl.NodesLocked() {
		if !nv.Exclude && cl.UsedLocked(nv.ID)*100 <= int64(cl.H)*nv.Total {
			continue
		}
		keys := append([]node.CopyKey(nil), nv.Copies...)
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Size != keys[j].Size {
				return keys[i].Size > keys[j].Size
			}
			if keys[i].Index != keys[j].Index {
				return keys[i].Index < keys[j].Index
			}
			if keys[i].Shard != keys[j].Shard {
				return keys[i].Shard < keys[j].Shard
			}
			if keys[i].Primary != keys[j].Primary {
				return keys[i].Primary
			}
			return keys[i].Replica < keys[j].Replica
		})
		for _, k := range keys {
			sourceID, on := cl.PlacementLocked(k)
			if !on || sourceID != nv.ID {
				continue // moved off this node by an earlier iteration
			}
			if !nv.Exclude && cl.UsedLocked(sourceID)*100 <= int64(cl.H)*nv.Total {
				break // over-high node dropped to/below H: stop moving
			}
			ix, _ := cl.IndexLocked(k.Index)
			pz := perZone(cl, 1+ix.R)
			// Conceptually take the copy off the source: candidates in the same
			// zone get one less shard copy for the D3 count.
			cl.RemoveLocked(sourceID, k)
			target, _ := chooseTarget(cl, k, sourceID, true, pz, evals)
			if target == "" {
				cl.PutLocked(sourceID, k) // cannot move: stays in place
				continue
			}
			cl.PutLocked(target, k)
			res.Moved = append(res.Moved, Movement{
				Index: k.Index, Shard: k.Shard, Primary: k.Primary,
				From: sourceID, To: target,
			})
		}
	}
}

// Explain reports every node's first veto for the requested copy. It never
// changes state.
func Explain(cl *node.Cluster, indexName string, shard int, primary bool) ([]Verdict, error) {
	cl.RLock()
	defer cl.RUnlock()

	ix, ok := cl.IndexLocked(indexName)
	if !ok {
		return nil, node.ErrNotFound
	}
	if shard < 0 || shard >= ix.S {
		return nil, node.ErrShardMissing
	}

	k := node.CopyKey{Index: indexName, Shard: shard, Primary: primary, Size: ix.Size}
	pz := perZone(cl, 1+ix.R)
	_, primaryOn := cl.PlacementLocked(node.CopyKey{Index: indexName, Shard: shard, Primary: true})
	primaryUp := !primary && !primaryOn

	out := make([]Verdict, 0)
	for _, nv := range cl.NodesLocked() {
		r := decider.Decide(candidateFacts(cl, nv, k, false, pz), nil)
		out = append(out, Verdict{NodeID: nv.ID, Reason: r, PrimaryUp: primaryUp})
	}
	return out, nil
}
