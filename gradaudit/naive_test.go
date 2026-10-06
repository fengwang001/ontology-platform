package gradaudit

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This file is an independently written naive model. It rebuilds the countable
// set straight from the stored state and brute-forces every assignment of
// (record -> claimed course identity) to a leaf, then evaluates the tree. The
// random differential test compares its verdict and attribution with the
// engine and logs every generated input, output and decision basis.

type naiveState struct {
	courses map[string]float64
	plan    *PlanVersion
	records []*Record
	subs    []Substitution
}

type naiveOption struct {
	rec    *Record
	course string
	credit float64
}

func (n *naiveState) options() []naiveOption {
	// Pass filter and revoke.
	var pass []*Record
	for _, r := range n.records {
		if !r.Revoked && r.Score >= n.plan.PassScore-1e-9 {
			pass = append(pass, r)
		}
	}
	// Transfer cutoff in registration order.
	var tr []*Record
	for _, r := range pass {
		if r.Transfer {
			tr = append(tr, r)
		}
	}
	sort.SliceStable(tr, func(i, j int) bool { return tr[i].transferOrder < tr[j].transferOrder })
	admit := map[string]bool{}
	sum := 0.0
	for _, r := range tr {
		if sum+r.Credit <= n.plan.TransferCap+1e-9 {
			admit[r.ID] = true
			sum += r.Credit
		}
	}
	// Native repeat collapse by course identity (champions of phase 1).
	best := map[string]*Record{}
	for _, r := range pass {
		if r.Transfer && !admit[r.ID] {
			continue
		}
		b, ok := best[r.Course]
		if !ok || betterAttempt(r, b) {
			best[r.Course] = r
		}
	}
	var surv []*Record
	for _, r := range best {
		surv = append(surv, r)
	}

	// Phase 1 native identities come only from native-repeat champions;
	// phase 2 substitution identities come from every passing candidate.
	var expanded []naiveOption
	for _, r := range surv {
		expanded = append(expanded, naiveOption{r, r.Course, r.Credit})
	}
	for _, r := range pass {
		if r.Transfer && !admit[r.ID] {
			continue
		}
		for _, s := range n.subs {
			if s.PlanID == n.plan.ID && s.From == r.Course && r.Semester >= s.Effective {
				cr := r.Credit
				if n.courses[s.To] < cr {
					cr = n.courses[s.To]
				}
				expanded = append(expanded, naiveOption{r, s.To, cr})
			}
		}
	}
	byTarget := map[string][]*Record{}
	for _, o := range expanded {
		byTarget[o.course] = append(byTarget[o.course], o.rec)
	}
	winner := map[string]*Record{}
	for tgt, rs := range byTarget {
		uniq := map[string]*Record{}
		for _, r := range rs {
			uniq[r.ID] = r
		}
		var best *Record
		for _, r := range uniq {
			if best == nil || betterAttempt(r, best) {
				best = r
			}
		}
		winner[tgt] = best
	}

	var out []naiveOption
	for _, o := range expanded {
		if winner[o.course].ID == o.rec.ID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rec.ID != out[j].rec.ID {
			return out[i].rec.ID < out[j].rec.ID
		}
		return out[i].course < out[j].course
	})
	return out
}

type naiveResult struct {
	pass        bool
	tree        bool
	attrCode    string
	attrLeaf    bool
	creditGap   float64
	courseGap   int
	childrenGap int
	totalCredit float64
	gpa         float64
	additional  []string
}

func (n *naiveState) audit() naiveResult {
	opts := n.options()
	leaves := collectLeaves(n.plan.Root)
	leafSet := map[string]*Requirement{}
	elig := map[string][]naiveOption{}
	for _, l := range leaves {
		leafSet[l.Code] = l
		set := map[string]bool{}
		for _, c := range l.Courses {
			set[c] = true
		}
		for _, o := range opts {
			if set[o.course] {
				elig[l.Code] = append(elig[l.Code], o)
			}
		}
	}
	shared := map[[2]string]bool{}
	for _, p := range n.plan.SharedPairs {
		a, b := p[0], p[1]
		if a > b {
			a, b = b, a
		}
		shared[[2]string{a, b}] = true
	}

	// Brute force: for each leaf choose a subset of its eligible options;
	// validate record exclusivity/shared pairs and per-leaf course uniqueness.
	var all []map[string][]naiveOption
	cur := map[string][]naiveOption{}
	var gen func(int)
	gen = func(i int) {
		if i == len(leaves) {
			cp := map[string][]naiveOption{}
			for k, v := range cur {
				cp[k] = append([]naiveOption(nil), v...)
			}
			all = append(all, cp)
			return
		}
		code := leaves[i].Code
		choices := elig[code]
		// enumerate subsets
		for mask := 0; mask < 1<<uint(len(choices)); mask++ {
			var pick []naiveOption
			ok := true
			courses := map[string]bool{}
			recHere := map[string]bool{}
			for bit := 0; bit < len(choices); bit++ {
				if mask&(1<<uint(bit)) == 0 {
					continue
				}
				o := choices[bit]
				if courses[o.course] || recHere[o.rec.ID] {
					ok = false
					break
				}
				pick = append(pick, o)
				courses[o.course] = true
				recHere[o.rec.ID] = true
			}
			if !ok {
				continue
			}
			// record exclusivity with other leaves
			users := map[string]map[string]bool{}
			for lc, os := range cur {
				for _, x := range os {
					if users[x.rec.ID] == nil {
						users[x.rec.ID] = map[string]bool{}
					}
					users[x.rec.ID][lc] = true
				}
			}
			for _, o := range pick {
				for other := range users[o.rec.ID] {
					a, b := code, other
					if a > b {
						a, b = b, a
					}
					if !shared[[2]string{a, b}] {
						ok = false
					}
				}
			}
			if !ok {
				continue
			}
			if len(pick) > 0 {
				cur[code] = pick
			}
			gen(i + 1)
			delete(cur, code)
		}
	}
	gen(0)

	leafOK := func(a map[string][]naiveOption, l *Requirement) bool {
		cr, nu := 0.0, map[string]bool{}
		for _, o := range a[l.Code] {
			cr += o.credit
			nu[o.course] = true
		}
		return cr+1e-9 >= l.MinCredit && len(nu) >= l.MinCourses
	}
	sat := func(a map[string][]naiveOption) map[*Requirement]bool {
		m := map[*Requirement]bool{}
		for _, l := range leaves {
			m[l] = leafOK(a, l)
		}
		return m
	}

	treePass := false
	for _, a := range all {
		if n.plan.Root.Satisfied(sat(a)) {
			treePass = true
		}
	}

	res := naiveResult{tree: treePass}

	// Attribution: smallest-coded node never satisfied.
	nodes := collectAll(n.plan.Root)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Code < nodes[j].Code })
	if !treePass {
		for _, node := range nodes {
			ever := false
			for _, a := range all {
				if node.Satisfied(sat(a)) {
					ever = true
				}
			}
			if !ever {
				res.attrCode = node.Code
				res.attrLeaf = node.Leaf
				if node.Leaf {
					bestCr, bestN := 0.0, 0
					for _, a := range all {
						cr := 0.0
						nu := map[string]bool{}
						for _, o := range a[node.Code] {
							cr += o.credit
							nu[o.course] = true
						}
						g := cr
						if g > bestCr {
							bestCr = g
						}
						if len(nu) > bestN {
							bestN = len(nu)
						}
					}
					if bestCr+1e-9 < node.MinCredit {
						res.creditGap = node.MinCredit - bestCr
					}
					if bestN < node.MinCourses {
						res.courseGap = node.MinCourses - bestN
					}
				} else {
					best := 0
					for _, a := range all {
						m := sat(a)
						cnt := 0
						for _, c := range node.Children {
							if c.Satisfied(m) {
								cnt++
							}
						}
						if cnt > best {
							best = cnt
						}
					}
					need := node.MinChildren
					if need <= 0 {
						need = len(node.Children)
					}
					if best < need {
						res.childrenGap = need - best
					}
				}
				break
			}
		}
	}

	// Totals/GPA: all surviving counted records at native credit.
	tot, wsum, w := 0.0, 0.0, 0.0
	survCred := map[string]float64{}
	survScore := map[string]float64{}
	for _, o := range opts {
		if _, ok := survCred[o.rec.ID]; !ok || o.credit > survCred[o.rec.ID] {
			survCred[o.rec.ID] = o.credit
			survScore[o.rec.ID] = o.rec.Score
		}
	}
	for id, cr := range survCred {
		tot += cr
		wsum += survScore[id] * cr
		w += cr
	}
	res.totalCredit = tot
	if w > 0 {
		res.gpa = wsum / w
	}
	if tot+1e-9 < n.plan.TotalCredit {
		res.additional = append(res.additional, "total_credit")
	}
	gpaOK := (w <= 0 && n.plan.MinGPA <= 0) || (w > 0 && res.gpa+1e-9 >= n.plan.MinGPA)
	if !gpaOK {
		res.additional = append(res.additional, "min_gpa")
	}

	// Required failures: course can enter a required leaf in some assignment.
	reqLeaf := map[string]bool{}
	reqCourses := map[string]bool{}
	for _, l := range leaves {
		if l.Required {
			reqLeaf[l.Code] = true
		}
	}
	for _, a := range all {
		for code, os := range a {
			if reqLeaf[code] {
				for _, o := range os {
					reqCourses[o.course] = true
				}
			}
		}
	}
	var failCourses []string
	for c := range reqCourses {
		if hasUnresolvedFailure(c, &studentState{records: n.recordMap()}, n.plan) {
			failCourses = append(failCourses, c)
		}
	}
	sort.Strings(failCourses)
	for range failCourses {
		res.additional = append(res.additional, "required_failure")
	}
	res.pass = res.tree && len(res.additional) == 0
	return res
}

func (n *naiveState) recordMap() map[string]*Record {
	m := map[string]*Record{}
	for _, r := range n.records {
		m[r.ID] = r
	}
	return m
}

// TestNaiveDifferential generates random single-student worlds and compares
// the engine verdict against the brute-force naive model.
func TestNaiveDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Log("differential test: each case logs inputs, outputs and rationale")
	}
	seed := timeNowSeed()
	if dbgSeedEnv() != 0 {
		seed = dbgSeedEnv()
	}
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed=%d", seed)

	for iter := 0; iter < 400; iter++ {
		world := genWorld(rng)
		// Build engine.
		eng := NewEngine()
		for c, cr := range world.courses {
			if err := eng.AddCourse(Course{Code: c, Credit: cr}); err != nil {
				t.Fatal(err)
			}
		}
		if err := eng.AddPlan(world.plan, 1); err != nil {
			t.Fatal(err)
		}
		if err := eng.Enroll("s", "V1"); err != nil {
			t.Fatal(err)
		}
		n := &naiveState{courses: world.courses, plan: world.plan}
		var log strings.Builder
		fmt.Fprintf(&log, "iter=%d courses=%v pass=%.0f total=%.0f gpa>=%.0f tcap=%.0f shared=%v\n",
			iter, world.courses, world.plan.PassScore, world.plan.TotalCredit,
			world.plan.MinGPA, world.plan.TransferCap, world.plan.SharedPairs)

		// Substitutions first (so later records can use them).
		for _, s := range world.subs {
			err := eng.RegisterSubstitution("s", s.From, s.To, "V1", s.Effective)
			n.subs = append(n.subs, s)
			fmt.Fprintf(&log, "op sub %s->%s eff=%d -> %v (basis: version match + semester gate)\n",
				s.From, s.To, s.Effective, err)
			_ = err
		}
		reg := 0
		for _, in := range world.records {
			err := eng.RegisterRecord(in)
			status := "ok"
			if err != nil {
				status = "rejected:" + err.Kind.String()
			} else {
				reg++
			}
			fmt.Fprintf(&log, "op rec id=%s %s sem=%d score=%.0f transfer=%v -> %s\n",
				in.RecordID, in.Course, in.Semester, in.Score, in.Transfer, status)
		}
		// Snapshot stored state for naive model (including rejected ones that
		// never reached state).
		st := eng.students["s"]
		st.mu.Lock()
		for _, r := range sortedRecords(st) {
			cp := *r
			n.records = append(n.records, &cp)
		}
		st.mu.Unlock()

		for _, rev := range world.revokes {
			err := eng.RevokeRecord("s", rev)
			fmt.Fprintf(&log, "op revoke %s -> %v\n", rev, err)
			if err == nil {
				for _, r := range n.records {
					if r.ID == rev {
						r.Revoked = true
					}
				}
			}
		}

		got, gerr := eng.Audit("s")
		if gerr != nil {
			t.Fatalf("iter %d audit error: %v\n%s", iter, gerr, log.String())
		}
		want := n.audit()

		fmt.Fprintf(&log, "out engine pass=%v tree=%v attr=%+v total=%.2f gpa=%.3f extra=%v\n",
			got.Pass, got.TreeSatisfied, got.Attribution, got.CountedCredit, got.WeightedGPA, kindsOf(got.Additional))
		fmt.Fprintf(&log, "out naive  pass=%v tree=%v attr=%s total=%.2f gpa=%.3f extra=%v\n",
			want.pass, want.tree, want.attrCode, want.totalCredit, want.gpa, want.additional)
		fmt.Fprintf(&log, "basis: pass=root satisfiable in some assignment; attr=smallest node never satisfiable; totals=best claim credit once\n")

		mismatch := got.Pass != want.pass ||
			got.TreeSatisfied != want.tree ||
			attrCode(got) != want.attrCode ||
			!floatEq(got.CountedCredit, want.totalCredit)
		// GPA only compared when both have countable weight.
		if got.WeightedGPA > 0 || want.gpa > 0 {
			if mathAbs(got.WeightedGPA-want.gpa) > 1e-9 {
				mismatch = true
			}
		}
		if len(got.Additional) != len(want.additional) {
			mismatch = true
		} else {
			gk := kindsOf(got.Additional)
			sort.Strings(gk)
			wk := append([]string(nil), want.additional...)
			sort.Strings(wk)
			for i := range gk {
				if gk[i] != wk[i] {
					mismatch = true
				}
			}
		}
		if mismatch {
			t.Fatalf("iter %d mismatch\n%s", iter, log.String())
		}
		if testing.Verbose() || iter < 3 {
			t.Log("\n" + log.String())
		}
	}
}

func attrCode(r *AuditResult) string {
	if r.Attribution == nil {
		return ""
	}
	return r.Attribution.Code
}

func kindsOf(fs []AdditionalFailure) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Kind)
	}
	sort.Strings(out)
	return out
}

func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func timeNowSeed() int64 { return time.Now().UnixNano() }

func dbgSeedEnv() int64 {
	v, _ := strconv.ParseInt(os.Getenv("DIFF_SEED"), 10, 64)
	return v
}

type world struct {
	courses map[string]float64
	plan    *PlanVersion
	records []RegisterRecordInput
	revokes []string
	subs    []Substitution
}

func genWorld(rng *rand.Rand) *world {
	courseCodes := []string{"C1", "C2", "C3", "C4", "C5", "C6"}
	courses := map[string]float64{}
	for _, c := range courseCodes {
		courses[c] = float64(1 + rng.Intn(4))
	}
	pickCodes := func(n int) []string {
		shuf := append([]string(nil), courseCodes...)
		rng.Shuffle(len(shuf), func(i, j int) { shuf[i], shuf[j] = shuf[j], shuf[i] })
		return shuf[:n]
	}
	mkLeaf := func(code string, n int) *Requirement {
		cs := pickCodes(1 + rng.Intn(n))
		minCr := 0.0
		if rng.Intn(2) == 0 {
			minCr = courses[cs[0]]
		}
		minN := 1
		if rng.Intn(3) == 0 {
			minN = 0
		}
		return &Requirement{
			Code: code, Leaf: true, Courses: cs,
			MinCredit: minCr, MinCourses: minN, Required: rng.Intn(2) == 0,
		}
	}
	var leaves []*Requirement
	nLeaves := 2 + rng.Intn(3)
	for i := 0; i < nLeaves; i++ {
		leaves = append(leaves, mkLeaf(fmt.Sprintf("L%d", i+1), 3))
	}
	// Group leaves into 1-2 internal nodes.
	var children []*Requirement
	split := 1 + rng.Intn(len(leaves))
	if split < len(leaves) && rng.Intn(2) == 0 {
		g1 := &Requirement{Code: "G1", Children: leaves[:split], MinChildren: 1 + rng.Intn(split)}
		g2 := &Requirement{Code: "G2", Children: leaves[split:], MinChildren: 1 + rng.Intn(len(leaves)-split)}
		children = []*Requirement{g1, g2}
	} else {
		children = leaves
	}
	root := &Requirement{Code: "ROOT", Children: children, MinChildren: 1 + rng.Intn(len(children))}

	var shared [][2]string
	if len(leaves) >= 2 && rng.Intn(2) == 0 {
		shared = append(shared, [2]string{leaves[0].Code, leaves[1].Code})
	}
	plan := &PlanVersion{
		ID: "V1", PassScore: 60, Root: root,
		TotalCredit: float64(rng.Intn(12)),
		MinGPA:      55 + float64(rng.Intn(30)),
		TransferCap: float64(2 * rng.Intn(4)),
		SharedPairs: shared,
	}

	w := &world{courses: courses, plan: plan}
	// Substitutions between random courses.
	for k := 0; k < rng.Intn(3); k++ {
		a := courseCodes[rng.Intn(len(courseCodes))]
		b := courseCodes[rng.Intn(len(courseCodes))]
		if a == b {
			continue
		}
		w.subs = append(w.subs, Substitution{From: a, To: b, PlanID: "V1", Effective: rng.Intn(3) + 1})
	}
	// Records.
	nRec := rng.Intn(9)
	for i := 0; i < nRec; i++ {
		c := courseCodes[rng.Intn(len(courseCodes))]
		score := []float64{40, 55, 60, 70, 75, 90}[rng.Intn(6)]
		w.records = append(w.records, RegisterRecordInput{
			StudentID: "s", RecordID: fmt.Sprintf("rec%d", i+1),
			Course: c, Semester: 1 + rng.Intn(4), Score: score,
			Transfer: rng.Intn(3) == 0,
		})
	}
	// Revocations chosen after the fact (by id).
	if len(w.records) > 0 && rng.Intn(2) == 0 {
		w.revokes = append(w.revokes, w.records[rng.Intn(len(w.records))].RecordID)
	}
	return w
}
