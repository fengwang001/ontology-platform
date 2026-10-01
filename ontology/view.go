// Package ontology implements a read-only lower layer plus a writable
// upper layer union directory view with whiteouts and opaque directories.
package ontology

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Record kinds stored in the upper layer.
const (
	KindFile     = "file"
	KindDir      = "dir"
	KindOpaque   = "opaque-dir"
	KindWhiteout = "whiteout"
)

// Sentinel errors. Rejected operations return an error wrapping one of
// these, so callers can use errors.Is.
var (
	ErrInvalidPath  = errors.New("invalid path")
	ErrNotFound     = errors.New("not found")
	ErrNotDirectory = errors.New("not a directory")
	ErrIsDirectory  = errors.New("is a directory")
	ErrExist        = errors.New("already exists")
	ErrDirNotEmpty  = errors.New("directory not empty")
	ErrCrossLayer   = errors.New("cross-layer rename")
	ErrIntoSelf     = errors.New("rename into self")
)

// UpperRecord is one upper layer entry. Content is meaningful only for
// KindFile.
type UpperRecord struct {
	Kind    string
	Content string
}

// NodeType identifies how a path resolves in the merged view.
type NodeType int

const (
	TypeMissing NodeType = iota
	TypeFile
	TypeDirectory
)

// LookupResult is the result of Lookup.
type LookupResult struct {
	Type    NodeType
	Content string
}

// View is a union directory view over one immutable lower layer and one
// mutable upper layer. The zero value is not usable; use New.
type View struct {
	mu            sync.RWMutex
	lower         map[string]bool   // true => directory, false => file
	lowerContents map[string]string // contents of lower files
	upper         map[string]UpperRecord
}

// New validates lower and returns an empty-upper union view. Keys ending
// in "/" are directories; other keys are files. Every parent of a key must
// itself exist as a directory key, and a path may not be both file and
// directory. Any violation rejects the whole construction.
func New(lower map[string]string) (*View, error) {
	lm := make(map[string]bool, len(lower))
	for key := range lower {
		dir := strings.HasSuffix(key, "/")
		p := key
		if dir {
			p = strings.TrimSuffix(key, "/")
		}
		if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
			return nil, fmt.Errorf("%w: lower key %q", ErrInvalidPath, key)
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return nil, fmt.Errorf("%w: lower key %q", ErrInvalidPath, key)
			}
		}
		if prev, ok := lm[p]; ok && prev != dir {
			return nil, fmt.Errorf("%w: %q is both file and directory", ErrInvalidPath, p)
		}
		lm[p] = dir
	}
	for p := range lm {
		for _, a := range ancestors(p) {
			if a == "" {
				continue
			}
			d, ok := lm[a]
			if !ok || !d {
				return nil, fmt.Errorf("%w: missing directory parent %q of %q", ErrInvalidPath, a, p)
			}
		}
	}
	contents := make(map[string]string, len(lower))
	for key, val := range lower {
		if !strings.HasSuffix(key, "/") {
			contents[key] = val
		}
	}
	return &View{lower: lm, lowerContents: contents, upper: make(map[string]UpperRecord)}, nil
}

// lowerReachable reports whether q is lower-reachable as of the state
// captured by the caller's walk semantics: L(q) exists and every strict
// ancestor of q is a directory that admits lower content (no upper
// whiteout/file, and no opaque directory on the path).
func (v *View) lowerReachable(q string) bool {
	if _, ok := v.lower[q]; !ok {
		return false
	}
	for _, a := range ancestors(q) {
		if a == "" {
			continue
		}
		u, has := v.upper[a]
		if !has {
			if _, lok := v.lower[a]; !lok {
				return false
			}
			continue
		}
		if u.Kind != KindDir {
			return false
		}
		// KindDir admits lower content only when the lower ancestor is a
		// directory; otherwise the merged node is a pure upper directory.
		if ld, lok := v.lower[a]; !lok || !ld {
			return false
		}
	}
	return true
}

// stat resolves path against lower+upper using walk semantics. Returns the
// merged node type and, for files, the content. Caller holds v.mu.
func (v *View) stat(path string) (NodeType, string) {
	segs := strings.Split(path, "/")
	cur := ""
	for i, seg := range segs {
		if cur == "" {
			cur = seg
		} else {
			cur = cur + "/" + seg
		}
		last := i == len(segs)-1
		if rec, ok := v.upper[cur]; ok {
			switch rec.Kind {
			case KindWhiteout:
				return TypeMissing, ""
			case KindFile:
				if last {
					return TypeFile, rec.Content
				}
				return TypeMissing, ""
			case KindDir, KindOpaque:
				if last {
					return TypeDirectory, ""
				}
			}
			continue
		}
		if !v.lowerReachable(cur) {
			return TypeMissing, ""
		}
		if last {
			if v.lower[cur] {
				return TypeDirectory, ""
			}
			return TypeFile, v.lowerContents[cur]
		}
		if !v.lower[cur] {
			return TypeMissing, ""
		}
	}
	return TypeMissing, ""
}

// ensureUpperDirs fills missing upper ancestors of path as plain dirs,
// walking root down. Caller has validated the ancestors as merged
// directories and holds v.mu (for mutation).
func (v *View) ensureUpperDirs(path string) {
	for _, a := range ancestors(path) {
		if a == "" {
			continue
		}
		if _, ok := v.upper[a]; !ok {
			v.upper[a] = UpperRecord{Kind: KindDir}
		}
	}
}

// discardSubtree removes all upper records at p and below it.
func (v *View) discardSubtree(p string) {
	for q := range v.upper {
		if q == p || strings.HasPrefix(q, p+"/") {
			delete(v.upper, q)
		}
	}
}

// Lookup resolves path in the merged view.
func (v *View) Lookup(path string) (LookupResult, error) {
	if !validateOpPath(path, true) {
		return LookupResult{}, fmt.Errorf("lookup %q: %w", path, ErrInvalidPath)
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if path == "" {
		return LookupResult{Type: TypeDirectory}, nil
	}
	if err := v.checkAncestors(path, false); err != nil {
		return LookupResult{}, err
	}
	t, content := v.stat(path)
	if t == TypeMissing {
		return LookupResult{}, fmt.Errorf("lookup %q: %w", path, ErrNotFound)
	}
	return LookupResult{Type: t, Content: content}, nil
}

// ReadDir lists immediate child names of path in byte order.
func (v *View) ReadDir(path string) ([]string, error) {
	if !validateOpPath(path, true) {
		return nil, fmt.Errorf("readdir %q: %w", path, ErrInvalidPath)
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if path == "" {
		return v.children(""), nil
	}
	if err := v.checkAncestors(path, false); err != nil {
		return nil, err
	}
	t, _ := v.stat(path)
	switch t {
	case TypeMissing:
		return nil, fmt.Errorf("readdir %q: %w", path, ErrNotFound)
	case TypeFile:
		return nil, fmt.Errorf("readdir %q: %w", path, ErrNotDirectory)
	}
	return v.children(path), nil
}

// children lists merged child names of the merged directory at dir (""
// is root). Caller holds mu and has established dir is a merged dir.
func (v *View) children(dir string) []string {
	seen := make(map[string]struct{})
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	for q, rec := range v.upper {
		if !strings.HasPrefix(q, prefix) || q == dir {
			continue
		}
		rest := q[len(prefix):]
		i := strings.IndexByte(rest, '/')
		name := rest
		if i >= 0 {
			name = rest[:i]
		}
		if i < 0 && rec.Kind == KindWhiteout {
			continue // whiteout only hides a lower name, never appears itself
		}
		seen[name] = struct{}{}
	}
	// Lower participates unless the dir resolves via an opaque record or
	// is not a lower-reachable lower directory.
	participate := false
	if dir == "" {
		participate = true
	} else if rec, ok := v.upper[dir]; ok {
		participate = rec.Kind == KindDir && v.lowerReachable(dir) && v.lower[dir]
	} else {
		participate = v.lowerReachable(dir) && v.lower[dir]
	}
	if participate {
		for q := range v.lower {
			if !strings.HasPrefix(q, prefix) || q == dir {
				continue
			}
			rest := q[len(prefix):]
			i := strings.IndexByte(rest, '/')
			name := rest
			if i >= 0 {
				name = rest[:i]
			}
			if _, hidden := v.upper[prefix+name]; hidden {
				continue // any upper record (incl. whiteout) hides lower name
			}
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// checkAncestors walks strict ancestors root down. A missing ancestor
// yields ErrNotFound; a file ancestor yields ErrNotDirectory. Caller
// holds at least RLock.
func (v *View) checkAncestors(path string, _ bool) error {
	for _, a := range ancestors(path) {
		if a == "" {
			continue
		}
		t, _ := v.stat(a)
		switch t {
		case TypeMissing:
			return fmt.Errorf("ancestor %q: %w", a, ErrNotFound)
		case TypeFile:
			return fmt.Errorf("ancestor %q: %w", a, ErrNotDirectory)
		}
	}
	return nil
}

// Mkdir creates path as a directory record in the upper layer.
func (v *View) Mkdir(path string) error {
	if !validateOpPath(path, false) {
		return fmt.Errorf("mkdir %q: %w", path, ErrInvalidPath)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkAncestors(path, true); err != nil {
		return err
	}
	t, _ := v.stat(path)
	if t != TypeMissing {
		return fmt.Errorf("mkdir %q: %w", path, ErrExist)
	}
	v.ensureUpperDirs(path)
	kind := KindDir
	if rec, ok := v.upper[path]; ok && rec.Kind == KindWhiteout {
		kind = KindOpaque
	}
	v.upper[path] = UpperRecord{Kind: kind}
	return nil
}

// Write stores a file record at path in the upper layer.
func (v *View) Write(path, content string) error {
	if !validateOpPath(path, false) {
		return fmt.Errorf("write %q: %w", path, ErrInvalidPath)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkAncestors(path, true); err != nil {
		return err
	}
	t, _ := v.stat(path)
	if t == TypeDirectory {
		return fmt.Errorf("write %q: %w", path, ErrIsDirectory)
	}
	v.ensureUpperDirs(path)
	v.upper[path] = UpperRecord{Kind: KindFile, Content: content}
	return nil
}

// Remove deletes path, recording a whiteout when lower content is hidden.
func (v *View) Remove(path string) error {
	if !validateOpPath(path, false) {
		return fmt.Errorf("remove %q: %w", path, ErrInvalidPath)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkAncestors(path, true); err != nil {
		return err
	}
	t, _ := v.stat(path)
	if t == TypeMissing {
		return fmt.Errorf("remove %q: %w", path, ErrNotFound)
	}
	if t == TypeDirectory && len(v.children(path)) > 0 {
		return fmt.Errorf("remove %q: %w", path, ErrDirNotEmpty)
	}
	if v.lowerReachable(path) {
		v.ensureUpperDirs(path)
		v.discardSubtree(path)
		v.upper[path] = UpperRecord{Kind: KindWhiteout}
	} else {
		v.discardSubtree(path)
	}
	return nil
}

// Rename moves old to new, rejecting cross-layer directory moves.
func (v *View) Rename(old, new string) error {
	if !validateOpPath(old, false) {
		return fmt.Errorf("rename old %q: %w", old, ErrInvalidPath)
	}
	if !validateOpPath(new, false) {
		return fmt.Errorf("rename new %q: %w", new, ErrInvalidPath)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkAncestors(old, true); err != nil {
		return err
	}
	oldType, oldContent := v.stat(old)
	if oldType == TypeMissing {
		return fmt.Errorf("rename old %q: %w", old, ErrNotFound)
	}
	if err := v.checkAncestors(new, true); err != nil {
		return err
	}
	if old == new || isUnder(new, old) {
		return fmt.Errorf("rename %q -> %q: %w", old, new, ErrIntoSelf)
	}
	// Cross-layer check: any directory whose contents come (partly) from
	// the lower layer cannot be renamed.
	if oldType == TypeDirectory {
		ur, hasUpper := v.upper[old]
		lowerDir := v.lower[old] && v.lowerReachable(old)
		if !hasUpper || (ur.Kind == KindDir && lowerDir) {
			return fmt.Errorf("rename %q -> %q: %w", old, new, ErrCrossLayer)
		}
	}
	newRec, newHasUpper := v.upper[new]
	newType, _ := v.stat(new)
	// Conflict resolution (whiteout counts as missing).
	if newType != TypeMissing {
		switch {
		case oldType == TypeFile && newType == TypeFile:
			// replace file
		case oldType == TypeFile && newType == TypeDirectory:
			return fmt.Errorf("rename %q -> %q: %w", old, new, ErrIsDirectory)
		case oldType == TypeDirectory && newType == TypeFile:
			return fmt.Errorf("rename %q -> %q: %w", old, new, ErrNotDirectory)
		default: // both directories
			if len(v.children(new)) > 0 {
				return fmt.Errorf("rename %q -> %q: %w", old, new, ErrDirNotEmpty)
			}
		}
	}

	// All reachability/type decisions below use the pre-mutation state.
	oldLowerReachable := v.lowerReachable(old)

	if oldType == TypeFile {
		v.ensureUpperDirs(new)
		v.upper[new] = UpperRecord{Kind: KindFile, Content: oldContent}
		if oldLowerReachable {
			v.ensureUpperDirs(old)
			v.discardSubtree(old)
			v.upper[old] = UpperRecord{Kind: KindWhiteout}
		} else {
			v.discardSubtree(old)
		}
		return nil
	}

	// Pure upper directory move.
	oldUpperRec := v.upper[old]
	newLowerDir := v.lower[new] && v.lowerReachable(new)
	newWasWhiteout := newHasUpper && newRec.Kind == KindWhiteout

	v.discardSubtree(new)
	v.ensureUpperDirs(new)

	// Snapshot the old subtree, then drop it from the upper layer.
	sub := make(map[string]UpperRecord)
	for q, rec := range v.upper {
		if isUnder(q, old) {
			sub[q] = rec
		}
	}
	for q := range sub {
		delete(v.upper, q)
	}
	for q, rec := range sub {
		nq := new + q[len(old):]
		v.upper[nq] = rec
	}
	headKind := oldUpperRec.Kind
	if headKind == KindOpaque || newWasWhiteout || newLowerDir {
		headKind = KindOpaque
	}
	v.upper[new] = UpperRecord{Kind: headKind}

	if oldLowerReachable {
		v.ensureUpperDirs(old)
		v.upper[old] = UpperRecord{Kind: KindWhiteout}
	}
	return nil
}

// Upper returns all upper records keyed by path.
func (v *View) Upper() map[string]UpperRecord {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]UpperRecord, len(v.upper))
	for k, rec := range v.upper {
		out[k] = rec
	}
	return out
}
