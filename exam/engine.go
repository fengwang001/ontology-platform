package exam

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Engine is the question-bank / paper-assembly service. All public methods
// take the same mutex, so concurrent calls are linearizable: the result is
// equivalent to some serial order, and every invariant is checked and
// committed while holding the lock. A rejected operation changes no state
// and consumes no version number.
type Engine struct {
	mu          sync.Mutex
	questions   map[string]*Question
	papers      map[string]*Paper
	groups      map[string]map[string]bool // group id -> member questions
	qgroups     map[string]map[string]bool // question id -> its groups
	comp        map[string]int             // question id -> mutex component id
	compMembers map[int]map[string]bool    // component id -> member questions
	nextComp    int
	checkOps    int // instrumented reads inside conflict checks (for tests)
}

func NewEngine() *Engine {
	return &Engine{
		questions:   map[string]*Question{},
		papers:      map[string]*Paper{},
		groups:      map[string]map[string]bool{},
		qgroups:     map[string]map[string]bool{},
		comp:        map[string]int{},
		compMembers: map[int]map[string]bool{},
	}
}

func validAttrs(score, difficulty int) bool { return score >= 0 && difficulty >= 0 }

func toSet(knowledge []string) map[string]bool {
	out := make(map[string]bool, len(knowledge))
	for _, k := range knowledge {
		out[k] = true
	}
	return out
}

// AddQuestion creates an available question with version 1.
func (e *Engine) AddQuestion(id string, score, difficulty int, knowledge []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || !validAttrs(score, difficulty) {
		return newError(ErrInvalidParam, "", nil, "bad question id or attributes")
	}
	if _, ok := e.questions[id]; ok {
		return newError(ErrInvalidParam, "", nil, "question %q already exists", id)
	}
	e.questions[id] = &Question{ID: id, Life: Available,
		Versions: []Version{{Number: 1, Score: score, Difficulty: difficulty, Knowledge: toSet(knowledge)}}}
	return nil
}

// ReviseQuestion appends a new immutable version and returns its number.
func (e *Engine) ReviseQuestion(id string, score, difficulty int, knowledge []string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || !validAttrs(score, difficulty) {
		return 0, newError(ErrInvalidParam, "", nil, "bad question id or attributes")
	}
	q, ok := e.questions[id]
	if !ok {
		return 0, newError(ErrNotFound, "", nil, "question %q not found", id)
	}
	n := len(q.Versions) + 1
	q.Versions = append(q.Versions, Version{Number: n, Score: score, Difficulty: difficulty, Knowledge: toSet(knowledge)})
	return n, nil
}

// SuspendQuestion moves Available -> Suspended. Papers already containing
// the question stay valid; new assemblies may no longer select it.
func (e *Engine) SuspendQuestion(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, ok := e.questions[id]
	if !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", id)
	}
	if q.Life != Available {
		return newError(ErrInvalidState, "", nil,
			"cannot suspend question %q in state %s", id, q.Life)
	}
	q.Life = Suspended
	return nil
}

// ResumeQuestion moves Suspended -> Available.
func (e *Engine) ResumeQuestion(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, ok := e.questions[id]
	if !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", id)
	}
	if q.Life != Suspended {
		return newError(ErrInvalidState, "", nil,
			"cannot resume question %q in state %s", id, q.Life)
	}
	q.Life = Available
	return nil
}

// RetireQuestion moves Available/Suspended -> Retired (terminal). Every
// published paper containing the question immediately becomes Invalid; the
// sorted list of those papers is returned.
func (e *Engine) RetireQuestion(id string) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, ok := e.questions[id]
	if !ok {
		return nil, newError(ErrNotFound, "", nil, "question %q not found", id)
	}
	if q.Life == Retired {
		return nil, newError(ErrInvalidState, "", nil, "question %q already retired", id)
	}
	q.Life = Retired
	var affected []string
	for pid, p := range e.papers {
		if p.State != Published {
			continue
		}
		if _, ok := p.Items[id]; ok {
			p.State = Invalid
			affected = append(affected, pid)
		}
	}
	sort.Strings(affected)
	return affected, nil
}

// AddToGroup adds a question to a mutex group. Membership changes apply to
// future assembly and replacement decisions only; published papers are not
// revalidated retroactively.
func (e *Engine) AddToGroup(questionID, groupID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if questionID == "" || groupID == "" {
		return newError(ErrInvalidParam, "", nil, "empty question or group id")
	}
	if _, ok := e.questions[questionID]; !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", questionID)
	}
	if e.groups[groupID][questionID] {
		return newError(ErrInvalidParam, "", nil,
			"question %q already in group %q", questionID, groupID)
	}
	e.addMembership(questionID, groupID)
	return nil
}

// RemoveFromGroup removes a question from a mutex group.
func (e *Engine) RemoveFromGroup(questionID, groupID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if questionID == "" || groupID == "" {
		return newError(ErrInvalidParam, "", nil, "empty question or group id")
	}
	if _, ok := e.questions[questionID]; !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", questionID)
	}
	if !e.groups[groupID][questionID] {
		return newError(ErrInvalidParam, "", nil,
			"question %q is not in group %q", questionID, groupID)
	}
	e.removeMembership(questionID, groupID)
	return nil
}

// CreatePaper creates a draft paper with its assembly constraints.
func (e *Engine) CreatePaper(id string, targetScore int, knowledgeReq map[string]int, difficultyReq map[int]DifficultyRange) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || targetScore < 0 {
		return newError(ErrInvalidParam, "", nil, "bad paper id or negative target score")
	}
	for kp, n := range knowledgeReq {
		if kp == "" || n <= 0 {
			return newError(ErrInvalidParam, "", nil, "bad knowledge requirement for %q", kp)
		}
	}
	for level, r := range difficultyReq {
		if level < 0 || r.Min < 0 || r.Max < r.Min {
			return newError(ErrInvalidParam, "", nil, "bad difficulty range for level %d", level)
		}
	}
	if _, ok := e.papers[id]; ok {
		return newError(ErrInvalidParam, "", nil, "paper %q already exists", id)
	}
	kreq := make(map[string]int, len(knowledgeReq))
	for k, v := range knowledgeReq {
		kreq[k] = v
	}
	dreq := make(map[int]DifficultyRange, len(difficultyReq))
	for k, v := range difficultyReq {
		dreq[k] = v
	}
	e.papers[id] = &Paper{ID: id, State: Draft,
		Config: PaperConfig{TargetScore: targetScore, KnowledgeReq: kreq, DifficultyReq: dreq},
		Items:  map[string]*BoundQuestion{}}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// attrsOf resolves the effective attributes of a paper item: the latest
// version for drafts, the frozen bound version once published.
func (e *Engine) attrsOf(p *Paper, qid string) attrs {
	if p.State == Draft {
		v := e.questions[qid].latest()
		return attrs{score: v.Score, diff: v.Difficulty, know: v.Knowledge}
	}
	it := p.Items[qid]
	return attrs{score: it.Score, diff: it.Difficulty, know: it.Knowledge}
}

// DraftAdd adds a question to a draft. The question must be available and
// must not conflict (via mutex components) with the current draft items.
// Whole-paper constraints (score, knowledge, difficulty) are only checked
// by ValidatePaper / Publish.
func (e *Engine) DraftAdd(paperID, questionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if paperID == "" || questionID == "" {
		return newError(ErrInvalidParam, "", nil, "empty paper or question id")
	}
	p, ok := e.papers[paperID]
	if !ok {
		return newError(ErrNotFound, "", nil, "paper %q not found", paperID)
	}
	q, ok := e.questions[questionID]
	if !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", questionID)
	}
	if _, dup := p.Items[questionID]; dup {
		return newError(ErrInvalidParam, paperID, nil, "question %q already in paper %q", questionID, paperID)
	}
	if p.State != Draft {
		return newError(ErrInvalidState, paperID, nil, "paper %q is %s, not a draft", paperID, p.State)
	}
	if q.Life != Available {
		return newError(ErrNotSelectable, paperID, []string{questionID},
			"question %q is %s", questionID, q.Life)
	}
	if other, ok := e.conflictsWith(questionID, sortedKeys(p.Items)); ok {
		return newError(ErrMutexConflict, paperID, sortedPair(other, questionID),
			"questions %s and %s are mutually exclusive", other, questionID)
	}
	p.Items[questionID] = &BoundQuestion{QuestionID: questionID}
	return nil
}

// DraftRemove removes a question from a draft.
func (e *Engine) DraftRemove(paperID, questionID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if paperID == "" || questionID == "" {
		return newError(ErrInvalidParam, "", nil, "empty paper or question id")
	}
	p, ok := e.papers[paperID]
	if !ok {
		return newError(ErrNotFound, "", nil, "paper %q not found", paperID)
	}
	if _, ok := p.Items[questionID]; !ok {
		return newError(ErrNotFound, paperID, nil, "question %q not in paper %q", questionID, paperID)
	}
	if p.State != Draft {
		return newError(ErrInvalidState, paperID, nil, "paper %q is %s, not a draft", paperID, p.State)
	}
	delete(p.Items, questionID)
	return nil
}

// validateAll runs the publish-time constraint battery in fixed priority
// order over the given item set. requireAvailable lists the questions that
// must currently be selectable.
func (e *Engine) validateAll(p *Paper, ids []string, requireAvailable bool) *Error {
	if requireAvailable {
		var bad []string
		for _, id := range ids {
			if e.questions[id].Life != Available {
				bad = append(bad, id)
			}
		}
		if len(bad) > 0 {
			return newError(ErrNotSelectable, p.ID, bad, "questions not selectable: %v", bad)
		}
	}
	if err := e.mutexAmong(p.ID, ids); err != nil {
		return err
	}
	vals := make([]attrs, len(ids))
	for i, id := range ids {
		vals[i] = e.attrsOf(p, id)
	}
	if err := checkTotalScore(p.ID, ids, vals, p.Config.TargetScore); err != nil {
		return err
	}
	if err := checkKnowledge(p.ID, ids, vals, p.Config.KnowledgeReq); err != nil {
		return err
	}
	return checkDifficulty(p.ID, ids, vals, p.Config.DifficultyReq)
}

// ValidatePaper fully validates a draft without changing anything.
func (e *Engine) ValidatePaper(paperID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.papers[paperID]
	if !ok {
		return newError(ErrNotFound, "", nil, "paper %q not found", paperID)
	}
	if p.State != Draft {
		return newError(ErrInvalidState, paperID, nil, "paper %q is %s, not a draft", paperID, p.State)
	}
	if err := e.validateAll(p, sortedKeys(p.Items), true); err != nil {
		return err
	}
	return nil
}

// Publish validates all constraints at once and, on success, freezes the
// paper by binding every question's current latest version. On any failure
// the draft is left untouched.
func (e *Engine) Publish(paperID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.papers[paperID]
	if !ok {
		return newError(ErrNotFound, "", nil, "paper %q not found", paperID)
	}
	if p.State != Draft {
		return newError(ErrInvalidState, paperID, nil, "paper %q is %s, not a draft", paperID, p.State)
	}
	ids := sortedKeys(p.Items)
	if err := e.validateAll(p, ids, true); err != nil {
		return err
	}
	for _, id := range ids {
		v := e.questions[id].latest()
		p.Items[id] = &BoundQuestion{QuestionID: id, Version: v.Number,
			Score: v.Score, Difficulty: v.Difficulty, Knowledge: v.cloneKnowledge()}
	}
	p.State = Published
	return nil
}

func (e *Engine) hasRetired(p *Paper) bool {
	for id := range p.Items {
		if e.questions[id].Life == Retired {
			return true
		}
	}
	return false
}

// Replace swaps one question of a published or invalid paper for another,
// atomically: any failure leaves the paper untouched. The replacement binds
// the new question's latest version at replacement time and the result must
// still satisfy every publish-time constraint (an invalid paper may keep
// its other retired questions). An invalid paper recovers to Published once
// it satisfies all constraints and contains no retired question.
func (e *Engine) Replace(paperID, oldID, newID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if paperID == "" || oldID == "" || newID == "" || oldID == newID {
		return newError(ErrInvalidParam, "", nil, "bad replace arguments")
	}
	p, ok := e.papers[paperID]
	if !ok {
		return newError(ErrNotFound, "", nil, "paper %q not found", paperID)
	}
	if _, ok := e.questions[oldID]; !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", oldID)
	}
	nq, ok := e.questions[newID]
	if !ok {
		return newError(ErrNotFound, "", nil, "question %q not found", newID)
	}
	if _, ok := p.Items[oldID]; !ok {
		return newError(ErrNotFound, paperID, nil, "question %q not in paper %q", oldID, paperID)
	}
	if _, dup := p.Items[newID]; dup {
		return newError(ErrInvalidParam, paperID, nil, "question %q already in paper %q", newID, paperID)
	}
	if p.State == Draft {
		return newError(ErrInvalidState, paperID, nil, "cannot replace in draft paper %q", paperID)
	}
	if p.State == Invalid && e.questions[oldID].Life != Retired {
		return newError(ErrInvalidState, paperID, []string{oldID},
			"paper %q is invalid: only retired questions may be replaced", paperID)
	}
	if nq.Life != Available {
		return newError(ErrNotSelectable, paperID, []string{newID},
			"question %q is %s", newID, nq.Life)
	}
	var remaining []string
	for id := range p.Items {
		if id != oldID {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	if other, ok := e.conflictsWith(newID, remaining); ok {
		return newError(ErrMutexConflict, paperID, sortedPair(other, newID),
			"questions %s and %s are mutually exclusive", other, newID)
	}
	ids := append(append([]string{}, remaining...), newID)
	vals := make([]attrs, len(ids))
	for i, id := range ids {
		if id == newID {
			v := nq.latest()
			vals[i] = attrs{score: v.Score, diff: v.Difficulty, know: v.Knowledge}
		} else {
			vals[i] = e.attrsOf(p, id)
		}
	}
	if err := checkTotalScore(paperID, ids, vals, p.Config.TargetScore); err != nil {
		return err
	}
	if err := checkKnowledge(paperID, ids, vals, p.Config.KnowledgeReq); err != nil {
		return err
	}
	if err := checkDifficulty(paperID, ids, vals, p.Config.DifficultyReq); err != nil {
		return err
	}
	delete(p.Items, oldID)
	v := nq.latest()
	p.Items[newID] = &BoundQuestion{QuestionID: newID, Version: v.Number,
		Score: v.Score, Difficulty: v.Difficulty, Knowledge: v.cloneKnowledge()}
	if p.State == Invalid && !e.hasRetired(p) {
		p.State = Published
	}
	return nil
}

// --- read-only queries -------------------------------------------------

// LifecycleOf returns the lifecycle of a question (Retired if unknown).
func (e *Engine) LifecycleOf(id string) Lifecycle {
	e.mu.Lock()
	defer e.mu.Unlock()
	if q, ok := e.questions[id]; ok {
		return q.Life
	}
	return Retired
}

// VersionCount returns how many versions a question has.
func (e *Engine) VersionCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if q, ok := e.questions[id]; ok {
		return len(q.Versions)
	}
	return 0
}

// PaperStateOf returns the state of a paper.
func (e *Engine) PaperStateOf(id string) (PaperState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.papers[id]; ok {
		return p.State, true
	}
	return Draft, false
}

// PaperItems returns the paper's questions with their bound version
// numbers (0 in a draft, where the latest version applies at publish).
func (e *Engine) PaperItems(id string) map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]int{}
	if p, ok := e.papers[id]; ok {
		for qid, it := range p.Items {
			out[qid] = it.Version
		}
	}
	return out
}

// ItemAttrs returns the effective (frozen for published papers) attributes
// of one paper item.
func (e *Engine) ItemAttrs(paperID, questionID string) (score, difficulty int, knowledge []string, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, pok := e.papers[paperID]
	if !pok {
		return 0, 0, nil, false
	}
	if _, ok := p.Items[questionID]; !ok {
		return 0, 0, nil, false
	}
	a := e.attrsOf(p, questionID)
	return a.score, a.diff, sortedKeys(a.know), true
}

// Conflicts reports whether candidate is mutually exclusive with any
// question currently in the paper, in O(paper size) map reads.
func (e *Engine) Conflicts(paperID, candidate string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.papers[paperID]
	if !ok {
		return false
	}
	_, conflict := e.conflictsWith(candidate, sortedKeys(p.Items))
	return conflict
}

// CheckOps returns the number of map reads spent inside conflict checks
// since the last ResetCheckOps; tests use it to prove the check cost does
// not depend on the question-bank or group-table sizes.
func (e *Engine) CheckOps() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkOps
}

// ResetCheckOps resets the conflict-check read counter.
func (e *Engine) ResetCheckOps() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checkOps = 0
}

// Digest renders the full logical state deterministically; the naive model
// in the tests produces the same format for differential comparison.
func (e *Engine) Digest() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var b strings.Builder
	for _, id := range sortedKeys(e.questions) {
		q := e.questions[id]
		fmt.Fprintf(&b, "Q %s %s", q.ID, q.Life)
		for _, v := range q.Versions {
			fmt.Fprintf(&b, " v%d:%d:%d:%s", v.Number, v.Score, v.Difficulty, strings.Join(sortedKeys(v.Knowledge), ","))
		}
		b.WriteByte('\n')
	}
	for _, id := range sortedKeys(e.papers) {
		p := e.papers[id]
		fmt.Fprintf(&b, "P %s %s target=%d", p.ID, p.State, p.Config.TargetScore)
		for _, qid := range sortedKeys(p.Items) {
			it := p.Items[qid]
			fmt.Fprintf(&b, " %s@v%d:%d:%d:%s", qid, it.Version, it.Score, it.Difficulty,
				strings.Join(sortedKeys(it.Knowledge), ","))
		}
		b.WriteByte('\n')
	}
	for _, g := range sortedKeys(e.groups) {
		fmt.Fprintf(&b, "G %s %s\n", g, strings.Join(sortedKeys(e.groups[g]), ","))
	}
	return b.String()
}
