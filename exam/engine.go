package exam

import (
	"fmt"
	"sort"
	"sync"
)

// Engine 题库版本管理与组卷约束引擎。
// 全部公开操作由同一把互斥锁串行化，并发调用等价于某个串行顺序。
type Engine struct {
	mu        sync.Mutex
	questions map[string]*Question
	papers    map[string]*Paper
	mi        *MutexIndex
}

func NewEngine() *Engine {
	return &Engine{
		questions: map[string]*Question{},
		papers:    map[string]*Paper{},
		mi:        newMutexIndex(),
	}
}

// CreateQuestion 新建题目（初始为可用态、版本 1）。
func (e *Engine) CreateQuestion(id string, score, difficulty int, kps, groups []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "CreateQuestion"
	if err := checkQuestionParams(op, id, score, difficulty); err != nil {
		return err
	}
	if _, dup := e.questions[id]; dup {
		return newErr(CatInvalidParam, op, fmt.Sprintf("question %q already exists", id))
	}
	e.questions[id] = newQuestion(id, score, difficulty, kps)
	e.mi.setGroups(id, groups)
	return nil
}

// CreatePaper 新建草稿试卷并配置组卷约束。
func (e *Engine) CreatePaper(id string, c Constraints) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "CreatePaper"
	if id == "" {
		return newErr(CatInvalidParam, op, "paper id must not be empty")
	}
	if c.TargetScore <= 0 {
		return newErr(CatInvalidParam, op, fmt.Sprintf("target score must be positive, got %d", c.TargetScore))
	}
	for kp, n := range c.Coverage {
		if kp == "" || n < 1 {
			return newErr(CatInvalidParam, op, fmt.Sprintf("invalid coverage requirement %q: %d", kp, n))
		}
	}
	for level, r := range c.Difficulty {
		if r.Min < 0 || r.Max < r.Min {
			return newErr(CatInvalidParam, op, fmt.Sprintf("invalid difficulty range for level %d: [%d,%d]", level, r.Min, r.Max))
		}
	}
	if _, dup := e.papers[id]; dup {
		return newErr(CatInvalidParam, op, fmt.Sprintf("paper %q already exists", id))
	}
	e.papers[id] = &Paper{ID: id, State: Draft, Cons: c}
	return nil
}

// AddToPaper 向草稿试卷加入一道题（选用其最新版本）。
func (e *Engine) AddToPaper(paperID, qID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "AddToPaper"
	if paperID == "" || qID == "" {
		return newErr(CatInvalidParam, op, "paper id and question id must not be empty")
	}
	p, ok := e.papers[paperID]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("paper %q not found", paperID))
	}
	q, ok := e.questions[qID]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not found", qID))
	}
	if p.State != Draft {
		return newErr(CatStateNotAllowed, op, fmt.Sprintf("paper %q is %s, not draft", paperID, p.State))
	}
	if q.Life != Available {
		return newErr(CatNotSelectable, op, fmt.Sprintf("question %q is %s", qID, q.Life), qID)
	}
	if p.inDraft(qID) {
		return newErr(CatInvalidParam, op, fmt.Sprintf("question %q already in draft", qID), qID)
	}
	p.draft = append(p.draft, qID)
	return nil
}

// RemoveFromPaper 从草稿试卷移除一道题。
func (e *Engine) RemoveFromPaper(paperID, qID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "RemoveFromPaper"
	if paperID == "" || qID == "" {
		return newErr(CatInvalidParam, op, "paper id and question id must not be empty")
	}
	p, ok := e.papers[paperID]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("paper %q not found", paperID))
	}
	if _, ok := e.questions[qID]; !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not found", qID))
	}
	if p.State != Draft {
		return newErr(CatStateNotAllowed, op, fmt.Sprintf("paper %q is %s, not draft", paperID, p.State))
	}
	for i, id := range p.draft {
		if id == qID {
			p.draft = append(p.draft[:i], p.draft[i+1:]...)
			return nil
		}
	}
	return newErr(CatNotFound, op, fmt.Sprintf("question %q not in draft %q", qID, paperID), qID)
}

// ValidateDraft 对草稿做整卷校验，返回首个不满足的约束类别。
func (e *Engine) ValidateDraft(paperID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "ValidateDraft"
	p, err := e.draftForValidate(op, paperID)
	if err != nil {
		return err
	}
	entries, err := e.draftEntries(op, p)
	if err != nil {
		return err
	}
	if verr := validateEntries(op, entries, p.Cons, e.mi); verr != nil {
		return verr
	}
	return nil
}

// Publish 一次性校验全部约束；全部满足则冻结为已发布试卷，
// 绑定各题当前最新版本；任一不满足则草稿不变并报出首个失败类别。
func (e *Engine) Publish(paperID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Publish"
	p, err := e.draftForValidate(op, paperID)
	if err != nil {
		return err
	}
	entries, err := e.draftEntries(op, p)
	if err != nil {
		return err
	}
	if err := validateEntries(op, entries, p.Cons, e.mi); err != nil {
		return err
	}
	p.entries = entries
	p.draft = nil
	p.State = Published
	return nil
}

func (e *Engine) draftForValidate(op, paperID string) (*Paper, error) {
	if paperID == "" {
		return nil, newErr(CatInvalidParam, op, "paper id must not be empty")
	}
	p, ok := e.papers[paperID]
	if !ok {
		return nil, newErr(CatNotFound, op, fmt.Sprintf("paper %q not found", paperID))
	}
	if p.State != Draft {
		return nil, newErr(CatStateNotAllowed, op, fmt.Sprintf("paper %q is %s, not draft", paperID, p.State))
	}
	return p, nil
}

// draftEntries 以各题最新版本构建条目；含停用/下架题时报 CatNotSelectable。
func (e *Engine) draftEntries(op string, p *Paper) ([]Entry, error) {
	entries := make([]Entry, 0, len(p.draft))
	for _, qID := range p.draft {
		q := e.questions[qID]
		if q.Life != Available {
			return nil, newErr(CatNotSelectable, op,
				fmt.Sprintf("question %q is %s", qID, q.Life), qID)
		}
		entries = append(entries, entryOf(q))
	}
	return entries, nil
}

// Replace 用一道题替换试卷中的另一道题，是已发布/失效试卷唯一允许的改动。
// 替换后须仍满足全部约束；失败则试卷不变。
// 失效试卷只允许以下架题为被替换对象；替换生效后若不再含下架题且
// 满足全部约束则恢复为已发布，否则保持失效。
func (e *Engine) Replace(paperID, oldQID, newQID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Replace"
	if paperID == "" || oldQID == "" || newQID == "" {
		return newErr(CatInvalidParam, op, "paper id, old and new question ids must not be empty")
	}
	if oldQID == newQID {
		return newErr(CatInvalidParam, op, "old and new question must differ", oldQID)
	}
	p, ok := e.papers[paperID]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("paper %q not found", paperID))
	}
	oldQ, ok := e.questions[oldQID]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not found", oldQID))
	}
	newQ, ok := e.questions[newQID]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not found", newQID))
	}
	if p.State == Draft {
		return newErr(CatStateNotAllowed, op, "cannot replace in a draft paper; use add/remove")
	}
	idx := p.entryIndex(oldQID)
	if idx < 0 {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not in paper %q", oldQID, paperID), oldQID)
	}
	if p.State == Invalid && oldQ.Life != Withdrawn {
		return newErr(CatStateNotAllowed, op,
			fmt.Sprintf("paper %q is invalid: replacement target must be a withdrawn question, %q is %s",
				paperID, oldQID, oldQ.Life), oldQID)
	}
	if p.entryIndex(newQID) >= 0 {
		return newErr(CatInvalidParam, op, fmt.Sprintf("question %q already in paper %q", newQID, paperID), newQID)
	}
	if newQ.Life != Available {
		return newErr(CatNotSelectable, op, fmt.Sprintf("question %q is %s", newQID, newQ.Life), newQID)
	}
	candidate := make([]Entry, len(p.entries))
	copy(candidate, p.entries)
	candidate[idx] = entryOf(newQ)
	if err := validateEntries(op, candidate, p.Cons, e.mi); err != nil {
		return err
	}
	p.entries = candidate
	if p.State == Invalid && !p.hasWithdrawn(e.questions) {
		p.State = Published
	}
	return nil
}

func (p *Paper) hasWithdrawn(questions map[string]*Question) bool {
	for _, en := range p.entries {
		if questions[en.QuestionID].Life == Withdrawn {
			return true
		}
	}
	return false
}

// QuestionInfo 返回题目的生命周期、全部版本与互斥组归属（只读副本）。
func (e *Engine) QuestionInfo(id string) (Lifecycle, []Version, []string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, ok := e.questions[id]
	if !ok {
		return 0, nil, nil, false
	}
	versions := make([]Version, len(q.Versions))
	copy(versions, q.Versions)
	return q.Life, versions, e.mi.groupsOf(id), true
}

// PaperInfo 返回试卷状态、冻结条目与草稿题目列表（只读副本）。
func (e *Engine) PaperInfo(id string) (PaperState, []Entry, []string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.papers[id]
	if !ok {
		return 0, nil, nil, false
	}
	entries := make([]Entry, len(p.entries))
	copy(entries, p.entries)
	draft := make([]string, len(p.draft))
	copy(draft, p.draft)
	return p.State, entries, draft, true
}

// CheckConflict 判定候选题与题目集合是否互斥，返回命中题目与 BFS 访问顶点数。
// 访问顶点数用于验证开销与题库题目总数、互斥组总数无关。
func (e *Engine) CheckConflict(candidate string, set []string) (string, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := map[string]bool{}
	for _, id := range set {
		s[id] = true
	}
	return e.mi.conflictWithSet(candidate, s)
}

func entryOf(q *Question) Entry {
	v := q.latest()
	return Entry{
		QuestionID:      q.ID,
		Version:         v.Number,
		Score:           v.Score,
		Difficulty:      v.Difficulty,
		KnowledgePoints: sortedCopy(v.KnowledgePoints),
	}
}

func checkQuestionParams(op, id string, score, difficulty int) error {
	if id == "" {
		return newErr(CatInvalidParam, op, "question id must not be empty")
	}
	if score <= 0 {
		return newErr(CatInvalidParam, op, fmt.Sprintf("score must be positive, got %d", score))
	}
	if difficulty < 1 {
		return newErr(CatInvalidParam, op, fmt.Sprintf("difficulty must be >= 1, got %d", difficulty))
	}
	return nil
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[int]DifficultyRange) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ReviseQuestion 修订题目，产生一个新版本并返回版本号。
// 被拒绝的修订不消耗版本号；修订不改变题目标识与生命周期。
func (e *Engine) ReviseQuestion(id string, score, difficulty int, kps []string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "ReviseQuestion"
	if err := checkQuestionParams(op, id, score, difficulty); err != nil {
		return 0, err
	}
	q, ok := e.questions[id]
	if !ok {
		return 0, newErr(CatNotFound, op, fmt.Sprintf("question %q not found", id))
	}
	return q.revise(score, difficulty, kps), nil
}

// SuspendQuestion 停用题目：可用 -> 停用。
func (e *Engine) SuspendQuestion(id string) error {
	return e.transition(id, Suspended, "SuspendQuestion")
}

// ResumeQuestion 恢复题目：停用 -> 可用。
func (e *Engine) ResumeQuestion(id string) error {
	return e.transition(id, Available, "ResumeQuestion")
}

// WithdrawQuestion 下架题目：可用/停用 -> 下架（不可恢复）。
// 含该题的已发布试卷立即转为失效，返回受影响试卷清单（排序后）。
func (e *Engine) WithdrawQuestion(id string) ([]string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "WithdrawQuestion"
	q, ok := e.questions[id]
	if !ok {
		return nil, newErr(CatNotFound, op, fmt.Sprintf("question %q not found", id))
	}
	if !canTransition(q.Life, Withdrawn) {
		return nil, newErr(CatStateNotAllowed, op,
			fmt.Sprintf("illegal lifecycle transition %s -> %s", q.Life, Withdrawn), id)
	}
	q.Life = Withdrawn
	affected := []string{}
	for _, p := range e.papers {
		if p.State == Published && p.entryIndex(id) >= 0 {
			p.State = Invalid
			affected = append(affected, p.ID)
		}
	}
	sort.Strings(affected)
	return affected, nil
}

func (e *Engine) transition(id string, to Lifecycle, op string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, ok := e.questions[id]
	if !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not found", id))
	}
	if !canTransition(q.Life, to) {
		return newErr(CatStateNotAllowed, op,
			fmt.Sprintf("illegal lifecycle transition %s -> %s", q.Life, to), id)
	}
	q.Life = to
	return nil
}

// SetQuestionGroups 全量变更题目的互斥组归属。
// 只影响此后的组卷与替换判定，不改变已发布试卷状态。
func (e *Engine) SetQuestionGroups(id string, groups []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "SetQuestionGroups"
	if _, ok := e.questions[id]; !ok {
		return newErr(CatNotFound, op, fmt.Sprintf("question %q not found", id))
	}
	e.mi.setGroups(id, groups)
	return nil
}
