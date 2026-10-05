package gate_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/gate"
	"ontology/owners"
)

// This file cross-checks the gate against a naive step-by-step model
// transcribed directly from the spec, over random operation sequences.

type simPR struct {
	author   string
	files    []string
	draft    bool
	merged   bool
	base     int
	head     int
	verdicts map[string]int         // 1 approve, 2 request-changes
	checks   map[[2]interface{}]int // (check, head) -> status 0..4
}

type naive struct {
	n             int
	dismissStale  bool
	requireOwners bool
	strict        bool
	required      []string
	patterns      []string
	ownerLists    [][]string
	perms         map[string]int // 0 none, 1 write, 2 admin
	prs           map[int]*simPR
	next          int
	version       int
}

func simMatch(pattern, path string) bool {
	switch {
	case pattern == "*":
		return true
	case strings.HasSuffix(pattern, "/"):
		return strings.HasPrefix(path, pattern)
	case strings.HasPrefix(pattern, "*."):
		return strings.HasSuffix(path, pattern[1:])
	default:
		return pattern == path
	}
}

func (s *naive) ownersOf(path string) []string {
	var out []string
	for i, p := range s.patterns {
		if simMatch(p, path) {
			out = s.ownerLists[i]
		}
	}
	return out
}

func filesInvalid(files []string) bool {
	if len(files) < 1 || len(files) > 1000 {
		return true
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if seen[f] {
			return true
		}
		seen[f] = true
	}
	return false
}

func (s *naive) open(author string, files []string, draft bool) (int, string) {
	if filesInvalid(files) {
		return 0, "invalid"
	}
	id := s.next
	s.next++
	s.prs[id] = &simPR{
		author:   author,
		files:    append([]string(nil), files...),
		draft:    draft,
		base:     s.version,
		head:     1,
		verdicts: make(map[string]int),
		checks:   make(map[[2]interface{}]int),
	}
	return id, "ok"
}

func (s *naive) push(id int, files []string) string {
	if filesInvalid(files) {
		return "invalid"
	}
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if p.merged {
		return "state"
	}
	p.head++
	p.files = append([]string(nil), files...)
	if s.dismissStale {
		for u, v := range p.verdicts {
			if v == 1 {
				delete(p.verdicts, u)
			}
		}
	}
	return "ok"
}

func (s *naive) ready(id int) string {
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if p.merged {
		return "state"
	}
	p.draft = false
	return "ok"
}

func (s *naive) updateBranch(id int) string {
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if p.merged {
		return "state"
	}
	if p.base >= s.version {
		return "state"
	}
	p.base = s.version
	p.head++
	return "ok"
}

func (s *naive) review(id int, user string, verdict int) string {
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if p.merged {
		return "state"
	}
	if user == p.author && verdict != 3 {
		return "self"
	}
	if verdict == 1 || verdict == 2 {
		p.verdicts[user] = verdict
	}
	return "ok"
}

func (s *naive) dismiss(id int, reviewer, actor string) string {
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if s.perms[actor] != 2 {
		return "permission"
	}
	if p.merged {
		return "state"
	}
	if _, ok := p.verdicts[reviewer]; !ok {
		return "state"
	}
	delete(p.verdicts, reviewer)
	return "ok"
}

func (s *naive) report(id int, check string, head, status int) string {
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if head < 1 || head > p.head {
		return "invalid"
	}
	if p.merged {
		return "state"
	}
	p.checks[[2]interface{}{check, head}] = status
	return "ok"
}

func (s *naive) mergeable(id int) string {
	p, ok := s.prs[id]
	if !ok {
		return "notfound"
	}
	if p.merged {
		return "merged"
	}
	if p.draft {
		return "draft"
	}
	users := make([]string, 0, len(p.verdicts))
	for u := range p.verdicts {
		users = append(users, u)
	}
	sort.Strings(users)
	approvals := 0
	for _, u := range users {
		if s.perms[u] < 1 {
			continue
		}
		switch p.verdicts[u] {
		case 2:
			return "changes"
		case 1:
			approvals++
		}
	}
	if approvals < s.n {
		return "approvals"
	}
	if s.requireOwners {
		worst := ""
		for _, f := range p.files {
			var rem []string
			for _, o := range s.ownersOf(f) {
				if o != p.author {
					rem = append(rem, o)
				}
			}
			if len(rem) == 0 {
				continue
			}
			ok := false
			for _, o := range rem {
				if p.verdicts[o] == 1 && s.perms[o] >= 1 {
					ok = true
					break
				}
			}
			if !ok && (worst == "" || f < worst) {
				worst = f
			}
		}
		if worst != "" {
			return "owner"
		}
	}
	for _, name := range s.required {
		if st, ok := p.checks[[2]interface{}{name, p.head}]; ok && st == 2 {
			return "checkfailed"
		}
	}
	for _, name := range s.required {
		st, ok := p.checks[[2]interface{}{name, p.head}]
		if !ok || !(st == 1 || st == 3 || st == 4) {
			return "checkpending"
		}
	}
	if s.strict && p.base < s.version {
		return "stale"
	}
	return "ok"
}

func (s *naive) merge(id int, actor string) string {
	if _, ok := s.prs[id]; !ok {
		return "notfound"
	}
	if s.perms[actor] < 1 {
		return "permission"
	}
	if tag := s.mergeable(id); tag != "ok" {
		return tag
	}
	s.prs[id].merged = true
	s.version++
	return "ok"
}

func (s *naive) setperm(user string, level int) string {
	s.perms[user] = level
	return "ok"
}

// gateTag maps a gate error to the same tag vocabulary the naive model
// returns.
func gateTag(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, gate.ErrInvalidParam):
		return "invalid"
	case errors.Is(err, gate.ErrNotFound):
		return "notfound"
	case errors.Is(err, gate.ErrPermission):
		return "permission"
	case errors.Is(err, gate.ErrState):
		return "state"
	case errors.Is(err, gate.ErrSelfReview):
		return "self"
	case errors.Is(err, gate.ErrMerged):
		return "merged"
	case errors.Is(err, gate.ErrDraft):
		return "draft"
	case errors.Is(err, gate.ErrChangesRequested):
		return "changes"
	case errors.Is(err, gate.ErrApprovals):
		return "approvals"
	case errors.Is(err, gate.ErrOwnerApproval):
		return "owner"
	case errors.Is(err, gate.ErrCheckFailed):
		return "checkfailed"
	case errors.Is(err, gate.ErrCheckPending):
		return "checkpending"
	case errors.Is(err, gate.ErrStaleBase):
		return "stale"
	default:
		return "unknown:" + err.Error()
	}
}

type operation struct {
	kind    string
	pr      int
	user    string
	files   []string
	draft   bool
	verdict int
	check   string
	head    int
	status  int
	level   int
}

func (op operation) String() string {
	switch op.kind {
	case "open":
		return fmt.Sprintf("open(user=%s files=%v draft=%v)", op.user, op.files, op.draft)
	case "push":
		return fmt.Sprintf("push(pr=%d files=%v)", op.pr, op.files)
	case "ready":
		return fmt.Sprintf("ready(pr=%d)", op.pr)
	case "updatebranch":
		return fmt.Sprintf("updatebranch(pr=%d)", op.pr)
	case "review":
		return fmt.Sprintf("review(pr=%d user=%s verdict=%d)", op.pr, op.user, op.verdict)
	case "dismiss":
		return fmt.Sprintf("dismiss(pr=%d reviewer=%s actor=%s)", op.pr, op.user, op.check)
	case "report":
		return fmt.Sprintf("report(pr=%d check=%s head=%d status=%d)", op.pr, op.check, op.head, op.status)
	case "mergeable":
		return fmt.Sprintf("mergeable(pr=%d)", op.pr)
	case "merge":
		return fmt.Sprintf("merge(pr=%d actor=%s)", op.pr, op.user)
	case "setperm":
		return fmt.Sprintf("setperm(user=%s level=%d)", op.user, op.level)
	default:
		return op.kind
	}
}

var (
	simUsers    = []string{"alice", "bob", "carol", "dave", "erin"}
	simFiles    = []string{"main.go", "api/x.go", "api/y.go", "docs/a.md", "docs/gen.go", "README", "src/util.go"}
	simPatterns = []string{"*", "docs/", "api/", "*.go", "*.md", "README", "src/"}
	simChecks   = []string{"build", "test", "lint", "scan"}
)

func randSubset(r *rand.Rand, pool []string, max int) []string {
	n := r.Intn(max + 1)
	perm := r.Perm(len(pool))
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, pool[perm[i]])
	}
	return out
}

func randFiles(r *rand.Rand) []string {
	switch roll := r.Intn(100); {
	case roll < 3:
		return nil // invalid: empty
	case roll < 8:
		f := simFiles[r.Intn(len(simFiles))]
		return []string{f, f} // invalid: duplicated
	default:
		n := 1 + r.Intn(4)
		perm := r.Perm(len(simFiles))
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, simFiles[perm[i]])
		}
		return out
	}
}

type simFixture struct {
	cfg   gate.Config
	rules []owners.Rule
	perms map[string]int
	ops   []operation
}

func genFixture(r *rand.Rand) simFixture {
	fx := simFixture{perms: make(map[string]int)}
	fx.cfg = gate.Config{
		N:             r.Intn(4),
		DismissStale:  r.Intn(2) == 0,
		RequireOwners: r.Intn(2) == 0,
		Strict:        r.Intn(2) == 0,
		Required:      randSubset(r, simChecks, 3),
	}
	nRules := 1 + r.Intn(4)
	for i := 0; i < nRules; i++ {
		fx.rules = append(fx.rules, owners.Rule{
			Pattern: simPatterns[r.Intn(len(simPatterns))],
			Owners:  randSubset(r, simUsers, 3),
		})
	}
	for _, u := range simUsers {
		fx.perms[u] = r.Intn(3)
	}
	for i := 0; i < 40; i++ {
		op := operation{pr: 1 + r.Intn(8)}
		op.user = simUsers[r.Intn(len(simUsers))]
		switch roll := r.Intn(100); {
		case roll < 15:
			op.kind = "open"
			op.files = randFiles(r)
			op.draft = r.Intn(4) == 0
		case roll < 25:
			op.kind = "push"
			op.files = randFiles(r)
		case roll < 30:
			op.kind = "ready"
		case roll < 38:
			op.kind = "updatebranch"
		case roll < 58:
			op.kind = "review"
			op.verdict = 1 + r.Intn(3)
		case roll < 63:
			op.kind = "dismiss"
			op.check = simUsers[r.Intn(len(simUsers))] // actor, reused field
		case roll < 78:
			op.kind = "report"
			op.check = simChecks[r.Intn(len(simChecks))]
			op.head = 1 + r.Intn(4)
			op.status = r.Intn(5)
		case roll < 86:
			op.kind = "mergeable"
		case roll < 94:
			op.kind = "merge"
		default:
			op.kind = "setperm"
			op.level = r.Intn(3)
		}
		fx.ops = append(fx.ops, op)
	}
	return fx
}

func newNaive(fx simFixture) *naive {
	s := &naive{
		n:             fx.cfg.N,
		dismissStale:  fx.cfg.DismissStale,
		requireOwners: fx.cfg.RequireOwners,
		strict:        fx.cfg.Strict,
		required:      fx.cfg.Required,
		perms:         make(map[string]int),
		prs:           make(map[int]*simPR),
		next:          1,
	}
	for _, rule := range fx.rules {
		s.patterns = append(s.patterns, rule.Pattern)
		s.ownerLists = append(s.ownerLists, rule.Owners)
	}
	for u, p := range fx.perms {
		s.perms[u] = p
	}
	return s
}

// applyGate runs one operation against a gate and returns its tag.
func applyGate(t *testing.T, g *gate.Gate, op operation) (string, int) {
	t.Helper()
	switch op.kind {
	case "open":
		id, err := g.Open(op.user, op.files, op.draft)
		return gateTag(err), id
	case "push":
		return gateTag(g.Push(op.pr, op.files)), 0
	case "ready":
		return gateTag(g.Ready(op.pr)), 0
	case "updatebranch":
		return gateTag(g.UpdateBranch(op.pr)), 0
	case "review":
		return gateTag(g.Review(op.pr, op.user, gate.Verdict(op.verdict))), 0
	case "dismiss":
		return gateTag(g.Dismiss(op.pr, op.user, op.check)), 0
	case "report":
		return gateTag(g.Report(op.pr, op.check, op.head, gate.Status(op.status))), 0
	case "mergeable":
		return gateTag(g.Mergeable(op.pr)), 0
	case "merge":
		return gateTag(g.Merge(op.pr, op.user)), 0
	case "setperm":
		return gateTag(g.SetPerm(op.user, gate.Perm(op.level))), 0
	default:
		t.Fatalf("unknown op kind %q", op.kind)
		return "", 0
	}
}

// applyNaive runs one operation against the naive model.
func applyNaive(s *naive, op operation) (string, int) {
	switch op.kind {
	case "open":
		id, tag := s.open(op.user, op.files, op.draft)
		return tag, id
	case "push":
		return s.push(op.pr, op.files), 0
	case "ready":
		return s.ready(op.pr), 0
	case "updatebranch":
		return s.updateBranch(op.pr), 0
	case "review":
		return s.review(op.pr, op.user, op.verdict), 0
	case "dismiss":
		return s.dismiss(op.pr, op.user, op.check), 0
	case "report":
		return s.report(op.pr, op.check, op.head, op.status), 0
	case "mergeable":
		return s.mergeable(op.pr), 0
	case "merge":
		return s.merge(op.pr, op.user), 0
	case "setperm":
		return s.setperm(op.user, op.level), 0
	default:
		return "unknown", 0
	}
}

// TestAgainstNaiveModel replays 1500 random operation sequences against
// the gate twice (replay determinism) and against the naive model.
func TestAgainstNaiveModel(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	for seq := 0; seq < 1500; seq++ {
		fx := genFixture(r)
		g1, err := gate.New(fx.cfg, fx.rules)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		g2, err := gate.New(fx.cfg, fx.rules)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		sim := newNaive(fx)
		for u, p := range fx.perms {
			perm := gate.Perm(p)
			if err := g1.SetPerm(u, perm); err != nil {
				t.Fatalf("seq=%d SetPerm: %v", seq, err)
			}
			if err := g2.SetPerm(u, perm); err != nil {
				t.Fatalf("seq=%d SetPerm: %v", seq, err)
			}
		}
		for i, op := range fx.ops {
			tag1, id1 := applyGate(t, g1, op)
			tag2, id2 := applyGate(t, g2, op)
			tagS, idS := applyNaive(sim, op)
			t.Logf("seq=%d op=%02d in=%s out=%s basis=(T=%d)", seq, i, op, tagS, sim.version)
			if tag1 != tagS || tag2 != tagS {
				t.Fatalf("seq=%d op=%02d %s: gate1=%s gate2=%s naive=%s",
					seq, i, op, tag1, tag2, tagS)
			}
			if op.kind == "open" && tagS == "ok" && (id1 != idS || id2 != idS) {
				t.Fatalf("seq=%d op=%02d %s: ids gate1=%d gate2=%d naive=%d",
					seq, i, op, id1, id2, idS)
			}
			if g1.Version() != sim.version || g2.Version() != sim.version {
				t.Fatalf("seq=%d op=%02d %s: T gate1=%d gate2=%d naive=%d",
					seq, i, op, g1.Version(), g2.Version(), sim.version)
			}
		}
	}
}
