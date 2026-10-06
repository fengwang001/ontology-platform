package gradaudit

import "sort"

// option is one course identity a counted record may be assigned as.
type option struct {
	rec      *Record
	course   string
	credit   float64
	sub      bool
	semester int
	score    float64
	regOrder int
}

// countedView is the per-student input to the assignment enumeration.
type countedView struct {
	plan    *PlanVersion
	options []option
}

// buildCountedView produces the deterministic countable set; see rules in the
// design note.
func buildCountedView(plan *PlanVersion, st *studentState, courses map[string]*Course) *countedView {
	all := sortedRecords(st)

	// Passing, non-revoked records only. Equality with the pass line counts.
	passing := make([]*Record, 0, len(all))
	for _, r := range all {
		if !r.Revoked && r.Score >= plan.PassScore-1e-9 {
			passing = append(passing, r)
		}
	}

	// Transfer overflow: passing transfers are admitted in registration order;
	// later records beyond the cap never count.
	transfers := make([]*Record, 0)
	for _, r := range passing {
		if r.Transfer {
			transfers = append(transfers, r)
		}
	}
	sort.SliceStable(transfers, func(i, j int) bool {
		return transfers[i].transferOrder < transfers[j].transferOrder
	})
	admitted := map[string]bool{}
	sum := 0.0
	for _, r := range transfers {
		if sum+r.Credit <= plan.TransferCap+1e-9 {
			admitted[r.ID] = true
			sum += r.Credit
		}
	}

	// Candidate survivors after the transfer cutoff.
	cand := make([]*Record, 0, len(passing))
	for _, r := range passing {
		if r.Transfer && !admitted[r.ID] {
			continue
		}
		cand = append(cand, r)
	}

	// Repeat collapse per course identity: highest score; tie -> earliest
	// semester; tie -> earliest registration. Transfer and native attempts of
	// the same course share the identity.
	best := map[string]*Record{}
	for _, r := range cand {
		b, ok := best[r.Course]
		if !ok || betterAttempt(r, b) {
			best[r.Course] = r
		}
	}
	survivors := make([]*Record, 0, len(best))
	for _, r := range best {
		survivors = append(survivors, r)
	}
	sort.Slice(survivors, func(i, j int) bool { return survivors[i].regOrder < survivors[j].regOrder })

	view := &countedView{plan: plan}

	// Options are produced in two phases:
	//  1. Native identities come only from native-repeat champions.
	//  2. Substitution identities are added from every surviving candidate;
	//     a native-repeat loser may still substitute into another target.
	// Per target course only the best attempt survives, so a substitution can
	// neither outrank a better native attempt nor be crowded out by a weaker
	// record; the chosen record also claims exactly that identity once.
	var raw []option
	addOpt := func(r *Record, target string, credit float64, sub bool) {
		raw = append(raw, option{
			rec: r, course: target, credit: credit, sub: sub,
			semester: r.Semester, score: r.Score, regOrder: r.regOrder,
		})
	}
	for _, r := range survivors {
		addOpt(r, r.Course, r.Credit, false)
	}
	for _, r := range cand {
		for _, s := range st.subs {
			if s.PlanID != plan.ID || s.From != r.Course || r.Semester < s.Effective {
				continue
			}
			target := courses[s.To]
			if target == nil {
				continue
			}
			credit := r.Credit
			if target.Credit < credit {
				credit = target.Credit
			}
			addOpt(r, s.To, credit, true)
		}
	}
	byTarget := map[string][]option{}
	for _, o := range raw {
		byTarget[o.course] = append(byTarget[o.course], o)
	}
	for _, os := range byTarget {
		best := os[0]
		for _, o := range os[1:] {
			if betterOption(o, best) {
				best = o
			}
		}
		view.options = append(view.options, best)
	}
	sort.Slice(view.options, func(i, j int) bool {
		if view.options[i].rec.ID != view.options[j].rec.ID {
			return view.options[i].rec.ID < view.options[j].rec.ID
		}
		return view.options[i].course < view.options[j].course
	})
	return view
}

func betterAttempt(a, b *Record) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Semester != b.Semester {
		return a.Semester < b.Semester
	}
	return a.regOrder < b.regOrder
}

func betterOption(a, b option) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.semester != b.semester {
		return a.semester < b.semester
	}
	return a.regOrder < b.regOrder
}

func sortedRecords(st *studentState) []*Record {
	out := make([]*Record, 0, len(st.records))
	for _, r := range st.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].regOrder < out[j].regOrder })
	return out
}
