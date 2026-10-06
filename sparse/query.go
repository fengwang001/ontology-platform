package sparse

import (
	"fmt"
	"sort"
	"strings"
)

// PathStatus is the mutually exclusive materialization state of one path.
type PathStatus int

const (
	StatusNotInCommit    PathStatus = iota // path does not exist in the current commit
	StatusMaterialized                     // path is materialized in the workspace
	StatusExcludedByRule                   // last matching rule is an exclude
	StatusNoMatchingRule                   // no rule matches the path
	StatusEmptyDir                         // directory matched by an include, but no descendant file is materialized
)

func (s PathStatus) String() string {
	switch s {
	case StatusNotInCommit:
		return "not-in-commit"
	case StatusMaterialized:
		return "materialized"
	case StatusExcludedByRule:
		return "excluded-by-rule"
	case StatusNoMatchingRule:
		return "no-matching-rule"
	case StatusEmptyDir:
		return "empty-dir"
	}
	return "unknown"
}

// QueryPath reports the materialization status of one path in the current
// commit under the current ruleset.
//
// A file is materialized iff its last matching rule is an include. A
// directory is materialized iff at least one descendant file is
// materialized (ancestors of materialized files are always materialized,
// even when an exclude rule hits the directory itself). For a
// non-materialized directory the reason is decided by its own last matching
// rule: exclude -> StatusExcludedByRule, none -> StatusNoMatchingRule,
// include -> StatusEmptyDir.
func (e *Engine) QueryPath(path string) (PathStatus, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !validPathSegments(path) {
		return StatusNotInCommit, fmt.Errorf("%w: malformed path %q", ErrInvalidParam, path)
	}
	segs := splitPath(path)
	if e.cur.containsFile(path) {
		inc, matched := e.curRules.lastMatch(segs, &e.stats)
		if inc {
			return StatusMaterialized, nil
		}
		if matched {
			return StatusExcludedByRule, nil
		}
		return StatusNoMatchingRule, nil
	}
	if e.cur.dirNodeAt(path) != nil {
		if e.dirCounts[path] > 0 {
			return StatusMaterialized, nil
		}
		inc, matched := e.curRules.lastMatch(segs, &e.stats)
		if matched && !inc {
			return StatusExcludedByRule, nil
		}
		if !matched {
			return StatusNoMatchingRule, nil
		}
		return StatusEmptyDir, nil
	}
	return StatusNotInCommit, nil
}

// ListMaterialized lists every materialized path (files and directories)
// strictly under prefix, in byte order. prefix may be empty (the whole
// workspace) and may carry one trailing separator.
func (e *Engine) ListMaterialized(prefix string) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix != "" && !validPathSegments(prefix) {
		return nil, fmt.Errorf("%w: malformed prefix %q", ErrInvalidParam, prefix)
	}
	n := e.cur.dirNodeAt(prefix)
	out := []string{}
	if n != nil && (prefix == "" || e.dirCounts[prefix] > 0) {
		e.collectMaterialized(n, prefix, &out)
	}
	sort.Strings(out)
	return out, nil
}

// collectMaterialized walks a subtree that is known to contain materialized
// files, emitting materialized files and directories.
func (e *Engine) collectMaterialized(n *dirNode, dir string, out *[]string) {
	e.stats.TreeNodes.Add(1)
	for name := range n.files {
		p := childPath(dir, name)
		e.stats.FileEvals.Add(1)
		if inc, _ := e.curRules.lastMatch(splitPath(p), &e.stats); inc {
			*out = append(*out, p)
		}
	}
	for dname, ch := range n.dirs {
		cp := childPath(dir, dname)
		if e.dirCounts[cp] > 0 {
			*out = append(*out, cp)
			e.collectMaterialized(ch, cp, out)
		}
	}
}
