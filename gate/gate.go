// Package gate implements the merge admission gate for a protected
// branch. All methods are safe for concurrent use; the result is
// equivalent to some serial order of the calls.
package gate

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/owners"
	"ontology/review"
)

// Perm is a user's permission level; unregistered users have PermNone.
type Perm int

const (
	PermNone Perm = iota
	PermWrite
	PermAdmin
)

// Valid reports whether p is a known permission level.
func (p Perm) Valid() bool { return p >= PermNone && p <= PermAdmin }

// Re-exported review types so callers only need this package.
type (
	Verdict = review.Verdict
	Status  = review.Status
)

const (
	VerdictApprove        = review.VerdictApprove
	VerdictRequestChanges = review.VerdictRequestChanges
	VerdictComment        = review.VerdictComment
)

const (
	StatusPending = review.StatusPending
	StatusSuccess = review.StatusSuccess
	StatusFailure = review.StatusFailure
	StatusNeutral = review.StatusNeutral
	StatusSkipped = review.StatusSkipped
)

// Rejection errors, distinguishable with errors.Is. Rejections are
// reported in the order: invalid parameter, not found, permission,
// conflicting state, self review, merge block reason.
var (
	ErrInvalidParam = errors.New("gate: invalid parameter")
	ErrNotFound     = errors.New("gate: pull request not found")
	ErrPermission   = errors.New("gate: permission denied")
	ErrState        = errors.New("gate: conflicting state")
	ErrSelfReview   = errors.New("gate: self review")
)

// Merge block reasons, distinguishable with errors.Is. Mergeable
// reports the first applicable reason in the order: merged, draft,
// changes requested, approvals, owner approval, check failed, check
// incomplete, stale base.
var (
	ErrMerged           = errors.New("gate: already merged")
	ErrDraft            = errors.New("gate: pull request is a draft")
	ErrChangesRequested = errors.New("gate: changes requested")
	ErrApprovals        = errors.New("gate: not enough approvals")
	ErrOwnerApproval    = errors.New("gate: missing owner approval")
	ErrCheckFailed      = errors.New("gate: required check failed")
	ErrCheckPending     = errors.New("gate: required check incomplete")
	ErrStaleBase        = errors.New("gate: base branch is stale")
)

// Config holds the merge admission policy.
type Config struct {
	N             int      // required number of valid approvals, 0..6
	DismissStale  bool     // Push clears approve verdicts
	RequireOwners bool     // require an owner approval per owned file
	Strict        bool     // require base to equal the branch version
	Required      []string // ordered required check names, 0..16 distinct
}

type prState struct {
	author   string
	files    []string
	draft    bool
	merged   bool
	base     int
	head     int
	verdicts *review.Ledger
	checks   *review.Checks
}

// Gate is the merge admission gate of one protected branch.
type Gate struct {
	mu       sync.Mutex
	cfg      Config
	resolver *owners.Resolver
	perms    map[string]Perm
	prs      map[int]*prState
	nextID   int
	version  int // target branch version T; equals the merged count
	touched  int // records read/written by Review and Report
}

// New validates cfg and rules and returns an empty gate with T = 0.
func New(cfg Config, rules []owners.Rule) (*Gate, error) {
	if cfg.N < 0 || cfg.N > 6 {
		return nil, fmt.Errorf("%w: N=%d out of range [0,6]", ErrInvalidParam, cfg.N)
	}
	if len(cfg.Required) > 16 {
		return nil, fmt.Errorf("%w: %d required checks, max 16", ErrInvalidParam, len(cfg.Required))
	}
	seen := make(map[string]bool, len(cfg.Required))
	for _, name := range cfg.Required {
		if name == "" || seen[name] {
			return nil, fmt.Errorf("%w: required check %q empty or duplicated", ErrInvalidParam, name)
		}
		seen[name] = true
	}
	for _, rule := range rules {
		if rule.Pattern == "" {
			return nil, fmt.Errorf("%w: empty rule pattern", ErrInvalidParam)
		}
	}
	required := make([]string, len(cfg.Required))
	copy(required, cfg.Required)
	return &Gate{
		cfg:      Config{N: cfg.N, DismissStale: cfg.DismissStale, RequireOwners: cfg.RequireOwners, Strict: cfg.Strict, Required: required},
		resolver: owners.NewResolver(rules),
		perms:    make(map[string]Perm),
		prs:      make(map[int]*prState),
		nextID:   1,
	}, nil
}

// SetPerm registers the permission level of user.
func (g *Gate) SetPerm(user string, level Perm) error {
	if !level.Valid() {
		return fmt.Errorf("%w: permission level %d", ErrInvalidParam, level)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.perms[user] = level
	return nil
}

// Version returns the target branch version T.
func (g *Gate) Version() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.version
}

// Open opens a merge request and returns its number. The request is
// created with base = T and head = 1.
func (g *Gate) Open(author string, files []string, draft bool) (int, error) {
	if err := validateFiles(files); err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	id := g.nextID
	g.nextID++
	g.prs[id] = &prState{
		author:   author,
		files:    append([]string(nil), files...),
		draft:    draft,
		base:     g.version,
		head:     1,
		verdicts: review.NewLedger(),
		checks:   review.NewChecks(),
	}
	return id, nil
}

// Push replaces the file set of the request and bumps head. When
// DismissStale is set, approve verdicts are cleared; change requests
// are always kept.
func (g *Gate) Push(pr int, files []string) error {
	if err := validateFiles(files); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if p.merged {
		return fmt.Errorf("%w: %d already merged", ErrState, pr)
	}
	p.head++
	p.files = append([]string(nil), files...)
	if g.cfg.DismissStale {
		p.verdicts.ClearApprovals()
	}
	return nil
}

// Ready clears the draft flag of the request.
func (g *Gate) Ready(pr int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if p.merged {
		return fmt.Errorf("%w: %d already merged", ErrState, pr)
	}
	p.draft = false
	return nil
}

// UpdateBranch fast-forwards base to T and bumps head without touching
// verdicts. It fails when base already equals T.
func (g *Gate) UpdateBranch(pr int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if p.merged {
		return fmt.Errorf("%w: %d already merged", ErrState, pr)
	}
	if p.base >= g.version {
		return fmt.Errorf("%w: %d base already at T=%d", ErrState, pr, g.version)
	}
	p.base = g.version
	p.head++
	return nil
}

// Review records a verdict. Comment never alters the reviewer's stored
// verdict; the author may only comment.
func (g *Gate) Review(pr int, user string, verdict Verdict) error {
	if !verdict.Valid() {
		return fmt.Errorf("%w: verdict %d", ErrInvalidParam, verdict)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if p.merged {
		return fmt.Errorf("%w: %d already merged", ErrState, pr)
	}
	if user == p.author && verdict != VerdictComment {
		return fmt.Errorf("%w: %s on own request %d", ErrSelfReview, user, pr)
	}
	g.touched++ // request record
	p.verdicts.Submit(user, verdict)
	g.touched++ // verdict record
	return nil
}

// Dismiss clears the reviewer's verdict; actor must be an admin.
func (g *Gate) Dismiss(pr int, reviewer, actor string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if g.permOf(actor) != PermAdmin {
		return fmt.Errorf("%w: %s is not admin", ErrPermission, actor)
	}
	if p.merged {
		return fmt.Errorf("%w: %d already merged", ErrState, pr)
	}
	if !p.verdicts.Dismiss(reviewer) {
		return fmt.Errorf("%w: %s has no verdict on %d", ErrState, reviewer, pr)
	}
	return nil
}

// Report archives a check result for head. Results for heads older
// than the current head are archived but never used in decisions.
func (g *Gate) Report(pr int, check string, head int, status Status) error {
	if !status.Valid() {
		return fmt.Errorf("%w: status %d", ErrInvalidParam, status)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if head < 1 || head > p.head {
		return fmt.Errorf("%w: head %d, current %d", ErrInvalidParam, head, p.head)
	}
	if p.merged {
		return fmt.Errorf("%w: %d already merged", ErrState, pr)
	}
	g.touched++ // request record
	p.checks.Report(check, head, status)
	g.touched++ // check record
	return nil
}

// Mergeable returns nil when the request may merge now, otherwise the
// first block reason in the fixed order.
func (g *Gate) Mergeable(pr int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	return g.blocked(p)
}

// Merge merges the request. actor must have write or admin permission.
// On success the request is marked merged and T is incremented.
func (g *Gate) Merge(pr int, actor string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.prs[pr]
	if !ok {
		return fmt.Errorf("%w: %d", ErrNotFound, pr)
	}
	if g.permOf(actor) < PermWrite {
		return fmt.Errorf("%w: %s cannot merge", ErrPermission, actor)
	}
	if err := g.blocked(p); err != nil {
		return err
	}
	p.merged = true
	g.version++
	return nil
}

// blocked returns the first merge block reason for p, or nil. The
// caller must hold the lock.
func (g *Gate) blocked(p *prState) error {
	if p.merged {
		return ErrMerged
	}
	if p.draft {
		return ErrDraft
	}
	entries := p.verdicts.Entries()
	reviewers := make([]string, 0, len(entries))
	for user := range entries {
		reviewers = append(reviewers, user)
	}
	sort.Strings(reviewers)
	approvals := 0
	for _, user := range reviewers {
		if g.permOf(user) < PermWrite {
			continue
		}
		switch entries[user] {
		case review.VerdictRequestChanges:
			return fmt.Errorf("%w: %s", ErrChangesRequested, user)
		case review.VerdictApprove:
			approvals++
		}
	}
	if approvals < g.cfg.N {
		return fmt.Errorf("%w: %d of %d", ErrApprovals, approvals, g.cfg.N)
	}
	if g.cfg.RequireOwners {
		worst := ""
		for _, file := range p.files {
			remaining := exclude(g.resolver.OwnersOf(file), p.author)
			if len(remaining) == 0 {
				continue
			}
			approved := false
			for _, owner := range remaining {
				if entries[owner] == review.VerdictApprove && g.permOf(owner) >= PermWrite {
					approved = true
					break
				}
			}
			if !approved && (worst == "" || file < worst) {
				worst = file
			}
		}
		if worst != "" {
			return fmt.Errorf("%w: %s", ErrOwnerApproval, worst)
		}
	}
	for _, name := range g.cfg.Required {
		if st, ok := p.checks.At(name, p.head); ok && st == review.StatusFailure {
			return fmt.Errorf("%w: %s", ErrCheckFailed, name)
		}
	}
	for _, name := range g.cfg.Required {
		st, ok := p.checks.At(name, p.head)
		if !ok || !st.Passes() {
			return fmt.Errorf("%w: %s", ErrCheckPending, name)
		}
	}
	if g.cfg.Strict && p.base < g.version {
		return fmt.Errorf("%w: base %d, T %d", ErrStaleBase, p.base, g.version)
	}
	return nil
}

// permOf returns the permission of user; the caller must hold the lock.
func (g *Gate) permOf(user string) Perm {
	return g.perms[user]
}

func validateFiles(files []string) error {
	if len(files) < 1 || len(files) > 1000 {
		return fmt.Errorf("%w: %d files, want 1..1000", ErrInvalidParam, len(files))
	}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if seen[f] {
			return fmt.Errorf("%w: duplicated file %q", ErrInvalidParam, f)
		}
		seen[f] = true
	}
	return nil
}

func exclude(list []string, user string) []string {
	var out []string
	for _, item := range list {
		if item != user {
			out = append(out, item)
		}
	}
	return out
}
