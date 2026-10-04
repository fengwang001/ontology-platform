// Package promote contains the leader-promotion mechanics: given a locked
// snapshot of a replication group it produces a pure promotion plan that
// fills holes in the new primary's history with term-advancing noops, rolls
// surviving in-sync replicas back to the pre-promotion global checkpoint and
// resyncs them, and reports which assigned operations are lost.
package promote

import "ontology/tracker"

// BuildPlan is the pure promotion builder.
//
// g = gcp before promotion; M = new primary's highest processed seq.
//   - Holes: each seq in 1..M absent from the new primary gets
//     (seq, newTerm, noop) on the new primary.
//   - Resync: every seq in g+1..M is installed on the other sync replicas,
//     taking the new primary's version when it has one, otherwise a noop.
//   - Lost: assigned seqs that no longer exist in the new primary's history,
//     i.e. hole positions in 1..M and every seq in M+1..maxSeq, ascending.
func BuildPlan(v tracker.PromotionView) tracker.PromotionPlan {
	p := tracker.PromotionPlan{
		NewTerm:    v.Term + 1,
		G:          v.G,
		M:          v.M,
		NewPrimary: v.Candidate,
	}

	for seq := int64(1); seq <= v.M; seq++ {
		if _, ok := v.NewPrimary[seq]; ok {
			continue
		}
		p.Holes = append(p.Holes, tracker.PlanEntry{Seq: seq, Term: p.NewTerm, Noop: true})
		p.Lost = append(p.Lost, seq)
	}
	for seq := v.G + 1; seq <= v.M; seq++ {
		if e, ok := v.NewPrimary[seq]; ok {
			p.Resync = append(p.Resync, tracker.PlanEntry{
				Seq: seq, Term: e.Term, Body: e.Body, Noop: e.Noop,
			})
		} else {
			p.Resync = append(p.Resync, tracker.PlanEntry{Seq: seq, Term: p.NewTerm, Noop: true})
		}
	}
	for seq := v.M + 1; seq <= v.MaxSeq; seq++ {
		p.Lost = append(p.Lost, seq)
	}
	return p
}

// New creates a group with the promotion builder installed; use Promote on
// such groups. The group semantics are otherwise identical to tracker.New.
func New(primary string, replicas []string) (*tracker.Group, error) {
	g, err := tracker.New(primary, replicas)
	if err != nil {
		return nil, err
	}
	g.PromotionBuilder = BuildPlan
	return g, nil
}

// Promote runs a leader promotion on a group created by New.
func Promote(g *tracker.Group, name string) error {
	return g.RunPromotion(name)
}
