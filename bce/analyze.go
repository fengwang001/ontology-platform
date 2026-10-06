package bce

// Stats counts the fact operations of one analysis run, making the
// complexity claims verifiable: MeetOps scales with the number of relevant
// facts (not unrelated variables) and ProveOps with the facts on the
// checked path (not the program length). A Stats value must not be shared
// between concurrent analyses.
type Stats struct {
	MeetOps  int64
	ProveOps int64
}

// Analyze decides every bounds check of the program. Invalid input is
// rejected with an *Error and no partial result. Analyze has no shared
// state: independent programs may be analyzed concurrently, and repeated
// analysis of the same program renders a byte-identical report.
func Analyze(p *Program) (*Report, error) {
	return AnalyzeWithStats(p, nil)
}

// AnalyzeWithStats is Analyze with operation counting.
func AnalyzeWithStats(p *Program, stats *Stats) (*Report, error) {
	c, err := validate(p)
	if err != nil {
		return nil, err
	}
	rel := computeRelevance(c)
	in, out, pre := computeFacts(c, rel, stats)
	doms := dominators(c, c.index[p.Entry])
	loops := findLoops(c, doms, out, rel, stats)
	pv := &prover{c: c, doms: doms, loops: loops, in: in, out: out, pre: pre, rel: rel, stats: stats}
	rep := &Report{}
	for _, site := range c.checks {
		rep.Decisions = append(rep.Decisions, pv.decide(site))
	}
	for _, d := range rep.Decisions {
		if d.Removed {
			rep.Removed++
		} else {
			rep.Kept++
		}
	}
	return rep, nil
}
