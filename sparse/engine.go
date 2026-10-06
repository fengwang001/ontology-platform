package sparse

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Sentinel errors, checked with errors.Is. Apply reports them in this
// precedence order: ErrInvalidParam, ErrCommitNotFound,
// ErrRulesetVersionNotFound, then *BlockedError.
var (
	ErrInvalidParam           = errors.New("sparse: invalid parameter")
	ErrCommitNotFound         = errors.New("sparse: commit not found")
	ErrRulesetVersionNotFound = errors.New("sparse: ruleset version not found")
	ErrNotMaterialized        = errors.New("sparse: path is not materialized")
)

// BlockedError is returned when dematerializing a path would discard a local
// modification and the call was not forced. Paths is sorted, deduplicated.
type BlockedError struct {
	Paths []string
}

func (e *BlockedError) Error() string {
	return "sparse: blocked by local modifications: " + strings.Join(e.Paths, ", ")
}

// Stats counts internal work so tests can prove cost scaling. All fields
// are atomic; reads may race with concurrent queries by design.
type Stats struct {
	MatchSteps atomic.Int64 // rule-trie node visits during matching
	FileEvals  atomic.Int64 // file materialization evaluations
	TreeNodes  atomic.Int64 // commit-tree node visits
}

// StatsSnapshot is a point-in-time copy of Stats.
type StatsSnapshot struct {
	MatchSteps int64
	FileEvals  int64
	TreeNodes  int64
}

// ChangeResult describes one applied (or forced) change. Kept is reported
// as a count, not a list, so that Apply's cost never grows with the number
// of unaffected materialized paths; enumerate them via ListMaterialized.
type ChangeResult struct {
	Added     []string // newly materialized paths, byte order
	Removed   []string // dematerialized paths, byte order
	Discarded []string // removed paths whose local modifications were discarded (force only)
	Kept      int      // number of materialized paths that stayed materialized
}

// Engine is a sparse-checkout workspace. All methods are safe for
// concurrent use; every call is linearizable (a single RWMutex serializes
// writers, and readers never observe a partially applied change).
type Engine struct {
	mu          sync.RWMutex
	commits     map[string]*Commit
	rulesets    map[int]*Ruleset
	nextVersion int

	cur       *Commit
	curRules  *Ruleset
	dirCounts map[string]int  // dir path -> number of materialized files beneath it
	localMod  map[string]bool // materialized paths carrying local modifications
	matFiles  int             // number of materialized files

	stats Stats
}

// NewEngine returns an engine on an empty commit with an empty ruleset
// (implicit version 0, meaning "keep current" when passed to Apply).
func NewEngine() *Engine {
	return &Engine{
		commits:     map[string]*Commit{},
		rulesets:    map[int]*Ruleset{},
		nextVersion: 1,
		cur:         &Commit{root: &dirNode{}},
		curRules:    &Ruleset{root: &ruleNode{}},
		dirCounts:   map[string]int{},
		localMod:    map[string]bool{},
	}
}

// AddCommit registers a commit's file tree.
func (e *Engine) AddCommit(id string, files []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.commits[id]; dup {
		return fmt.Errorf("%w: duplicate commit id %q", ErrInvalidParam, id)
	}
	c, err := NewCommit(id, files)
	if err != nil {
		return err
	}
	e.commits[id] = c
	return nil
}

// AddRuleset validates and registers a ruleset, returning its new, strictly
// increasing version. An invalid ruleset is rejected as a whole and
// consumes no version.
func (e *Engine) AddRuleset(rules []Rule) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rs, err := NewRuleset(e.nextVersion, rules)
	if err != nil {
		return 0, err
	}
	e.rulesets[rs.Version] = rs
	e.nextVersion++
	return rs.Version, nil
}

// Apply switches commit and/or ruleset atomically. commitID == "" keeps the
// current commit; rulesetVersion == 0 keeps the current ruleset. If any
// dematerialized path carries a local modification and force is false, the
// whole change is rejected with *BlockedError and nothing changes; with
// force the modifications are discarded and listed in ChangeResult.Discarded.
func (e *Engine) Apply(commitID string, rulesetVersion int, force bool) (*ChangeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if rulesetVersion < 0 {
		return nil, fmt.Errorf("%w: negative ruleset version %d", ErrInvalidParam, rulesetVersion)
	}
	next := e.cur
	if commitID != "" {
		c, ok := e.commits[commitID]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrCommitNotFound, commitID)
		}
		next = c
	}
	nextRules := e.curRules
	if rulesetVersion != 0 {
		rs, ok := e.rulesets[rulesetVersion]
		if !ok {
			return nil, fmt.Errorf("%w: %d", ErrRulesetVersionNotFound, rulesetVersion)
		}
		nextRules = rs
	}

	// Gather candidate files: commit delta plus changed rule scopes.
	affected := map[string]struct{}{}
	if next != e.cur {
		added, removed := diffCommits(e.cur, next, &e.stats)
		for _, p := range added {
			affected[p] = struct{}{}
		}
		for _, p := range removed {
			affected[p] = struct{}{}
		}
	}
	if nextRules != e.curRules {
		for _, sc := range diffRulesets(e.curRules, nextRules) {
			e.expandScope(sc, e.cur, affected)
			e.expandScope(sc, next, affected)
		}
	}

	// Evaluate transitions for affected files only.
	var addFiles, delFiles []string
	for p := range affected {
		oldM := e.matFile(e.cur, e.curRules, p)
		newM := e.matFile(next, nextRules, p)
		switch {
		case newM && !oldM:
			addFiles = append(addFiles, p)
		case oldM && !newM:
			delFiles = append(delFiles, p)
		}
	}

	// Directory materialization follows materialized-file counts.
	delta := map[string]int{}
	for _, f := range addFiles {
		for _, d := range ancestors(f) {
			delta[d]++
		}
	}
	for _, f := range delFiles {
		for _, d := range ancestors(f) {
			delta[d]--
		}
	}
	var addDirs, delDirs []string
	for d, dl := range delta {
		oldC := e.dirCounts[d]
		newC := oldC + dl
		if oldC == 0 && newC > 0 {
			addDirs = append(addDirs, d)
		}
		if oldC > 0 && newC == 0 {
			delDirs = append(delDirs, d)
		}
	}

	added := append(append([]string{}, addFiles...), addDirs...)
	removed := append(append([]string{}, delFiles...), delDirs...)
	sort.Strings(added)
	sort.Strings(removed)

	var blocked []string
	for _, p := range removed {
		if e.localMod[p] {
			blocked = append(blocked, p)
		}
	}
	if len(blocked) > 0 && !force {
		return nil, &BlockedError{Paths: blocked}
	}

	kept := (e.matFiles - len(delFiles)) + (len(e.dirCounts) - len(delDirs))

	// Commit the change.
	for d, dl := range delta {
		nc := e.dirCounts[d] + dl
		if nc == 0 {
			delete(e.dirCounts, d)
		} else {
			e.dirCounts[d] = nc
		}
	}
	for _, p := range removed {
		delete(e.localMod, p)
	}
	e.matFiles += len(addFiles) - len(delFiles)
	e.cur = next
	e.curRules = nextRules

	return &ChangeResult{Added: added, Removed: removed, Discarded: blocked, Kept: kept}, nil
}

// expandScope adds every file a changed rule scope may affect, looking at
// one commit.
func (e *Engine) expandScope(sc ruleScope, c *Commit, affected map[string]struct{}) {
	switch sc.kind {
	case kindExact:
		p := joinSegs(sc.segs)
		if c.containsFile(p) {
			affected[p] = struct{}{}
		}
	case kindPrefix:
		dir := joinSegs(sc.segs)
		if n := c.dirNodeAt(dir); n != nil {
			var files []string
			collectFiles(n, dir, &e.stats, &files)
			for _, f := range files {
				affected[f] = struct{}{}
			}
		}
	case kindStar:
		dir := joinSegs(sc.segs)
		if n := c.dirNodeAt(dir); n != nil {
			for name := range n.files {
				affected[childPath(dir, name)] = struct{}{}
			}
		}
	}
}

// matFile reports whether path p is a materialized file under (c, rs).
func (e *Engine) matFile(c *Commit, rs *Ruleset, p string) bool {
	if !c.containsFile(p) {
		return false
	}
	e.stats.FileEvals.Add(1)
	inc, _ := rs.lastMatch(splitPath(p), &e.stats)
	return inc
}

// SetLocalModified sets or clears the local-modification mark on a
// materialized path.
func (e *Engine) SetLocalModified(path string, modified bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validPathSegments(path) {
		return fmt.Errorf("%w: malformed path %q", ErrInvalidParam, path)
	}
	if !e.materializedLocked(path) {
		return fmt.Errorf("%w: %q", ErrNotMaterialized, path)
	}
	if modified {
		e.localMod[path] = true
	} else {
		delete(e.localMod, path)
	}
	return nil
}

func (e *Engine) materializedLocked(p string) bool {
	if e.dirCounts[p] > 0 {
		return true
	}
	return e.matFile(e.cur, e.curRules, p)
}

// Stats returns a snapshot of the internal work counters.
func (e *Engine) Stats() StatsSnapshot {
	return StatsSnapshot{
		MatchSteps: e.stats.MatchSteps.Load(),
		FileEvals:  e.stats.FileEvals.Load(),
		TreeNodes:  e.stats.TreeNodes.Load(),
	}
}

// ResetStats zeroes the internal work counters.
func (e *Engine) ResetStats() {
	e.stats.MatchSteps.Store(0)
	e.stats.FileEvals.Store(0)
	e.stats.TreeNodes.Store(0)
}
