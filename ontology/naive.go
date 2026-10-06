package ontology

import "sort"

// This file is an intentionally simple, independently written reference
// model. Unlike Engine it keeps flat structs and recomputes loads, pairs and
// constraints by full scans for every decision. It shares no allocation,
// grid or finalization code with Engine; only the View shape is shared so the
// tests can compare both implementations byte-for-byte.

type nTask struct {
	id, sheet, reviewer, role string
	score                     *int
	withdrawn                 bool
}

type nSheet struct {
	id, question, student string
	tasks                 []*nTask
	final                 *int
	pending               *nTask
}

type nRev struct {
	id, group string
	quota     int
	active    bool
	avoids    map[string]bool
}

// NaiveEngine is the brute-force reference implementation.
type NaiveEngine struct {
	groups map[string]bool
	qs     map[string]Question
	revs   map[string]*nRev
	sheets map[string]*nSheet
	sorder []string
	pairs  map[string]bool
	queue  []*nTask
	events []Event
	seq    int
	taskN  int
}

// NewNaive constructs the reference engine.
func NewNaive() *NaiveEngine {
	return &NaiveEngine{
		groups: map[string]bool{},
		qs:     map[string]Question{},
		revs:   map[string]*nRev{},
		sheets: map[string]*nSheet{},
		pairs:  map[string]bool{},
	}
}

func (n *NaiveEngine) log(k EventKind, sheet, task, rev, role string, sc *int, basis string) {
	n.seq++
	n.events = append(n.events, Event{Seq: n.seq, Kind: k, Sheet: sheet,
		TaskID: task, Reviewer: rev, Role: role, Score: sc, Basis: basis})
}

func nItoa(x int) string {
	if x == 0 {
		return "0"
	}
	var b []byte
	for x > 0 {
		b = append([]byte{byte('0' + x%10)}, b...)
		x /= 10
	}
	return string(b)
}

func (n *NaiveEngine) AddGroup(g Group) error {
	if g.ID == "" {
		return mkErr(ErrInvalidArgument, "empty group id")
	}
	if n.groups[g.ID] {
		return mkErr(ErrInvalidArgument, "duplicate group: "+g.ID)
	}
	n.groups[g.ID] = true
	return nil
}

func (n *NaiveEngine) AddQuestion(q Question) error {
	if q.ID == "" {
		return mkErr(ErrInvalidArgument, "empty question id")
	}
	if _, ok := n.qs[q.ID]; ok {
		return mkErr(ErrInvalidArgument, "duplicate question: "+q.ID)
	}
	if q.MaxScore <= 0 || q.Step <= 0 || q.MaxScore%q.Step != 0 {
		return mkErr(ErrInvalidArgument, "max score must be a positive multiple of step")
	}
	if q.Threshold < 0 {
		return mkErr(ErrInvalidArgument, "negative threshold")
	}
	n.qs[q.ID] = q
	return nil
}

func (n *NaiveEngine) AddReviewer(r Reviewer, avoids []string) error {
	if r.ID == "" {
		return mkErr(ErrInvalidArgument, "empty reviewer id")
	}
	if _, ok := n.revs[r.ID]; ok {
		return mkErr(ErrInvalidArgument, "duplicate reviewer: "+r.ID)
	}
	if !n.groups[r.GroupID] {
		return mkErr(ErrNotFound, "unknown group: "+r.GroupID)
	}
	if r.Quota < 0 {
		return mkErr(ErrInvalidArgument, "negative quota")
	}
	av := map[string]bool{}
	for _, s := range avoids {
		if s == "" {
			return mkErr(ErrInvalidArgument, "empty avoided student")
		}
		av[s] = true
	}
	n.revs[r.ID] = &nRev{id: r.ID, group: r.GroupID, quota: r.Quota, active: r.Active, avoids: av}
	n.pump()
	return nil
}

func (n *NaiveEngine) AddSheet(s Sheet) error {
	if s.ID == "" {
		return mkErr(ErrInvalidArgument, "empty sheet id")
	}
	if _, ok := n.sheets[s.ID]; ok {
		return mkErr(ErrInvalidArgument, "duplicate sheet: "+s.ID)
	}
	if _, ok := n.qs[s.Question]; !ok {
		return mkErr(ErrNotFound, "unknown question: "+s.Question)
	}
	if s.Student == "" {
		return mkErr(ErrInvalidArgument, "empty student")
	}
	sh := &nSheet{id: s.ID, question: s.Question, student: s.Student}
	n.sheets[s.ID] = sh
	n.sorder = append(n.sorder, s.ID)
	for range 2 {
		n.taskN++
		t := &nTask{id: "task-" + nItoa(n.taskN), sheet: sh.id, role: "initial"}
		sh.tasks = append(sh.tasks, t)
		n.tryOrQueueBan(sh, t, "")
	}
	n.pump()
	return nil
}

func (n *NaiveEngine) TriggerAllocation() { n.pump() }

// ---- brute-force selection ----

func (n *NaiveEngine) load(rev string) int {
	c := 0
	for _, sh := range n.sheets {
		for _, t := range sh.tasks {
			if t.reviewer == rev && !t.withdrawn && t.score == nil {
				c++
			}
		}
	}
	return c
}

func (n *NaiveEngine) sortedRevIDs() []string {
	ids := make([]string, 0, len(n.revs))
	for id := range n.revs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (n *NaiveEngine) allowedGroups(sh *nSheet, t *nTask) map[string]bool {
	ok := map[string]bool{}
	if t.role == "arbitrator" {
		busy := map[string]bool{}
		for _, o := range sh.tasks {
			if o.role == "initial" && !o.withdrawn && o.reviewer != "" {
				busy[n.revs[o.reviewer].group] = true
			}
		}
		for g := range n.groups {
			if !busy[g] {
				ok[g] = true
			}
		}
		return ok
	}
	other := ""
	for _, o := range sh.tasks {
		if o != t && o.role == "initial" && !o.withdrawn && o.reviewer != "" {
			other = n.revs[o.reviewer].group
		}
	}
	for g := range n.groups {
		if g != other {
			ok[g] = true
		}
	}
	return ok
}

func (n *NaiveEngine) pick(sh *nSheet, t *nTask, extraBan string) (string, map[string]int) {
	groups := n.allowedGroups(sh, t)
	bannedSheet := map[string]bool{extraBan: extraBan != ""}
	for _, o := range sh.tasks {
		if o == t || o.withdrawn {
			continue
		}
		if o.score != nil && o.reviewer != "" {
			bannedSheet[o.reviewer] = true
		}
	}
	var best string
	bestLoad, tied := 0, 0
	for _, id := range n.sortedRevIDs() {
		r := n.revs[id]
		if !r.active || !groups[r.group] || n.load(id) >= r.quota ||
			r.avoids[sh.student] || n.pairs[id+"\x00"+sh.student] || bannedSheet[id] {
			continue
		}
		l := n.load(id)
		switch {
		case best == "":
			best, bestLoad = id, l
		case l < bestLoad:
			best, bestLoad, tied = id, l, 0
		case l == bestLoad:
			tied++
			if id < best {
				best = id
			}
		}
	}
	if best == "" {
		return "", nil
	}
	return best, map[string]int{"tied_at_min_load": tied, "selected_load": bestLoad}
}

func (n *NaiveEngine) tryOrQueue(sh *nSheet, t *nTask) {
	n.tryOrQueueBan(sh, t, "")
}

func (n *NaiveEngine) tryOrQueueBan(sh *nSheet, t *nTask, ban string) {
	id, tie := n.pick(sh, t, ban)
	if id == "" {
		t.reviewer = ""
		sh.pending = t
		n.queue = append(n.queue, t)
		return
	}
	t.reviewer = id
	t.withdrawn = false
	n.pairs[id+"\x00"+sh.student] = true
	if sh.pending == t {
		sh.pending = nil
	}
	basis := "min_load=" + nItoa(tie["selected_load"]) +
		";tied_candidates=" + nItoa(tie["tied_at_min_load"]) + ";tie_break=min_id"
	n.log(EventAssign, sh.id, t.id, id, t.role, nil, basis)
}

func (n *NaiveEngine) pump() {
	i := 0
	for i < len(n.queue) {
		t := n.queue[i]
		sh := n.sheets[t.sheet]
		id, _ := n.pick(sh, t, "")
		if id == "" {
			i++
			continue
		}
		n.queue = append(n.queue[:i], n.queue[i+1:]...)
		n.tryOrQueue(sh, t)
	}
}

func (n *NaiveEngine) find(id string) (*nSheet, *nTask) {
	for _, sid := range n.sorder {
		sh := n.sheets[sid]
		for _, t := range sh.tasks {
			if t.id == id {
				return sh, t
			}
		}
	}
	return nil, nil
}

// ---- scoring, with independent grid/arithmetic implementations ----

// nSnap walks grid points upward until g >= num/den (instead of the index
// formula used by Engine).
func nSnap(q Question, num, den int) int {
	for g := 0; g < q.MaxScore; g += q.Step {
		if g*den >= num {
			return g
		}
	}
	return q.MaxScore
}

func nAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func cp(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (n *NaiveEngine) initials(sh *nSheet) []*nTask {
	var in []*nTask
	for _, o := range sh.tasks {
		if o.role == "initial" && !o.withdrawn && o.score != nil {
			in = append(in, o)
		}
	}
	return in
}

func (n *NaiveEngine) Submit(taskID string, score int) error {
	if taskID == "" {
		return mkErr(ErrInvalidArgument, "empty task id")
	}
	sh, t := n.find(taskID)
	if sh == nil {
		return mkErr(ErrNotFound, "unknown task: "+taskID)
	}
	if t.reviewer == "" {
		return mkErr(ErrTaskState, "task pending assignment: "+taskID)
	}
	if !n.revs[t.reviewer].active {
		return mkErr(ErrDeactivated, "reviewer deactivated: "+t.reviewer)
	}
	if sh.final != nil {
		return mkErr(ErrTaskState, "sheet already finalized: "+sh.id)
	}
	if t.withdrawn {
		return mkErr(ErrTaskState, "task withdrawn: "+taskID)
	}
	if t.score != nil {
		return mkErr(ErrTaskState, "duplicate submission: "+taskID)
	}
	q := n.qs[sh.question]
	if score < 0 || score > q.MaxScore || score%q.Step != 0 {
		return mkErr(ErrScore, "score off grid or out of range")
	}

	v := score
	t.score = &v
	n.log(EventSubmit, sh.id, t.id, t.reviewer, t.role, &v, "on_grid")

	if t.role == "arbitrator" {
		in := n.initials(sh)
		a, b := in[0], in[1]
		da, db := nAbs(v-*a.score), nAbs(v-*b.score)
		if da > q.Threshold && db > q.Threshold {
			f := v
			sh.final = &f
			n.log(EventFinal, sh.id, t.id, t.reviewer, "arbitrator", &f,
				"arbitrator_far_from_both;d_a="+nItoa(da)+";d_b="+nItoa(db))
		} else {
			chosen := a
			if db < da || (db == da && *b.score > *a.score) {
				chosen = b
			}
			f := nSnap(q, v+*chosen.score, 2)
			sh.final = &f
			n.log(EventFinal, sh.id, t.id, t.reviewer, "arbitrator", &f,
				"closer_side="+chosen.reviewer+";d_a="+nItoa(da)+
					";d_b="+nItoa(db)+";average_grid_up")
		}
	} else if len(n.initials(sh)) == 2 {
		in := n.initials(sh)
		a, b := in[0], in[1]
		gap := nAbs(*a.score - *b.score)
		if gap <= q.Threshold {
			f := nSnap(q, *a.score+*b.score, 2)
			sh.final = &f
			hi := a.reviewer
			if *b.score > *a.score {
				hi = b.reviewer
			}
			n.log(EventFinal, sh.id, "", "", "", &f,
				"agreement;gap="+nItoa(gap)+";average_grid_up;higher_side="+hi)
		} else {
			n.taskN++
			at := &nTask{id: "task-" + nItoa(n.taskN), sheet: sh.id, role: "arbitrator"}
			sh.tasks = append(sh.tasks, at)
			before := len(n.queue)
			n.tryOrQueue(sh, at)
			basis := "gap=" + nItoa(gap) + ">threshold=" + nItoa(q.Threshold)
			if len(n.queue) > before {
				basis += ";no_candidate_pending"
			} else {
				basis += ";arbitrator=" + at.reviewer
			}
			n.log(EventArbitration, sh.id, at.id, at.reviewer, "arbitrator", nil, basis)
		}
	}
	n.pump()
	return nil
}

func (n *NaiveEngine) Withdraw(taskID string) error {
	if taskID == "" {
		return mkErr(ErrInvalidArgument, "empty task id")
	}
	sh, t := n.find(taskID)
	if sh == nil {
		return mkErr(ErrNotFound, "unknown task: "+taskID)
	}
	if t.reviewer == "" {
		return mkErr(ErrTaskState, "task pending assignment: "+taskID)
	}
	if !n.revs[t.reviewer].active {
		return mkErr(ErrDeactivated, "reviewer deactivated: "+t.reviewer)
	}
	if sh.final != nil {
		return mkErr(ErrTaskState, "sheet already finalized: "+sh.id)
	}
	if t.withdrawn {
		return mkErr(ErrTaskState, "task already withdrawn: "+taskID)
	}
	if t.score != nil {
		return mkErr(ErrTaskState, "task already submitted: "+taskID)
	}

	rev := t.reviewer
	t.withdrawn = true
	t.reviewer = ""
	n.log(EventWithdraw, sh.id, taskID, rev, t.role, nil,
		"pre_submission;reviewer_banned_from_reselection")
	n.taskN++
	nt := &nTask{id: "task-" + nItoa(n.taskN), sheet: sh.id, role: t.role}
	sh.tasks = append(sh.tasks, nt)
	n.tryOrQueueBan(sh, nt, rev)
	n.pump()
	return nil
}

func (n *NaiveEngine) Deactivate(reviewerID string) error {
	if reviewerID == "" {
		return mkErr(ErrInvalidArgument, "empty reviewer id")
	}
	r, ok := n.revs[reviewerID]
	if !ok {
		return mkErr(ErrNotFound, "unknown reviewer: "+reviewerID)
	}
	if !r.active {
		return mkErr(ErrDeactivated, "reviewer already deactivated: "+reviewerID)
	}
	r.active = false

	type h struct {
		sh *nSheet
		t  *nTask
	}
	var hs []h
	for _, sid := range n.sorder {
		sh := n.sheets[sid]
		if sh.final != nil {
			continue
		}
		for _, t := range sh.tasks {
			if t.reviewer == reviewerID && !t.withdrawn && t.score == nil {
				hs = append(hs, h{sh, t})
			}
		}
	}
	for _, x := range hs {
		x.t.withdrawn = true
		x.t.reviewer = ""
		n.log(EventWithdraw, x.sh.id, x.t.id, reviewerID, x.t.role, nil,
			"reviewer_deactivated;reviewer_banned_from_reselection")
	}
	for _, x := range hs {
		n.taskN++
		nt := &nTask{id: "task-" + nItoa(n.taskN), sheet: x.sh.id, role: x.t.role}
		x.sh.tasks = append(x.sh.tasks, nt)
		n.tryOrQueueBan(x.sh, nt, reviewerID)
	}
	n.pump()
	return nil
}

// Snapshot renders the same View shape as Engine.Snapshot.
func (n *NaiveEngine) Snapshot() View {
	v := View{
		Reviewers: map[string]Reviewer{},
		Questions: map[string]Question{},
		Groups:    map[string]Group{},
		Sheets:    make([]SheetView, 0, len(n.sorder)),
		Events:    append([]Event(nil), n.events...),
	}
	for id := range n.groups {
		v.Groups[id] = Group{ID: id}
	}
	for id, q := range n.qs {
		v.Questions[id] = q
	}
	for _, id := range n.sortedRevIDs() {
		r := n.revs[id]
		v.Reviewers[id] = Reviewer{ID: r.id, GroupID: r.group, Quota: r.quota, Active: r.active}
	}
	for _, sid := range n.sorder {
		sh := n.sheets[sid]
		sv := SheetView{
			ID:          sh.id,
			Question:    sh.question,
			Student:     sh.student,
			FinalScore:  cp(sh.final),
			PendingTask: sh.pending != nil,
			Tasks:       []TaskView{},
		}
		for _, t := range sh.tasks {
			sv.Tasks = append(sv.Tasks, TaskView{
				ID: t.id, SheetID: sh.id, ReviewerID: t.reviewer, Role: t.role,
				Score: cp(t.score), Withdrawn: t.withdrawn,
			})
		}
		v.Sheets = append(v.Sheets, sv)
	}
	return v
}
