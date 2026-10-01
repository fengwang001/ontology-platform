package ontology

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// naiveModel is an independent, deliberately straightforward re-statement
// of the specification used to cross-check View.
type naiveModel struct {
	lower map[string]bool
	lcont map[string]string
	upper map[string]kinded
	log   *strings.Builder
}

type kinded struct {
	kind    string
	content string
}

func isValidPath(p string, allowRoot bool) bool {
	if p == "" {
		return allowRoot
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}

func anc(p string) []string {
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := []string{""}
	cur := ""
	for i := 0; i < len(parts)-1; i++ {
		if cur == "" {
			cur = parts[i]
		} else {
			cur += "/" + parts[i]
		}
		out = append(out, cur)
	}
	return out
}

func newNaive(lower map[string]string) *naiveModel {
	lm := map[string]bool{}
	lc := map[string]string{}
	for k, v := range lower {
		if strings.HasSuffix(k, "/") {
			lm[strings.TrimSuffix(k, "/")] = true
		} else {
			lm[k] = false
			lc[k] = v
		}
	}
	return &naiveModel{lower: lm, lcont: lc, upper: map[string]kinded{}, log: &strings.Builder{}}
}

func (m *naiveModel) reachable(q string) bool {
	if _, ok := m.lower[q]; !ok {
		return false
	}
	for _, a := range anc(q) {
		if a == "" {
			continue
		}
		u, hasU := m.upper[a]
		if hasU {
			if u.kind != KindDir {
				return false
			}
			if d, ok := m.lower[a]; !ok || !d {
				return false
			}
		} else if d, ok := m.lower[a]; !ok || !d {
			return false
		}
	}
	return true
}

func (m *naiveModel) resolve(q string) (string, string) {
	segs := strings.Split(q, "/")
	cur := ""
	for i, s := range segs {
		cur += s
		last := i == len(segs)-1
		u, hasU := m.upper[cur]
		if hasU {
			switch u.kind {
			case KindWhiteout:
				return "missing", ""
			case KindFile:
				if last {
					return "file", u.content
				}
				return "missing", ""
			case KindDir, KindOpaque:
				if last {
					return "dir", ""
				}
			}
		} else {
			if !m.reachable(cur) {
				return "missing", ""
			}
			if last {
				if m.lower[cur] {
					return "dir", ""
				}
				return "file", m.lcont[cur]
			}
			if !m.lower[cur] {
				return "missing", ""
			}
		}
		if i < len(segs)-1 {
			cur += "/"
		}
	}
	return "missing", ""
}

func (m *naiveModel) checkAncestors(q string) error {
	for _, a := range anc(q) {
		if a == "" {
			continue
		}
		t, _ := m.resolve(a)
		if t == "missing" {
			return ErrNotFound
		}
		if t == "file" {
			return ErrNotDirectory
		}
	}
	return nil
}

func (m *naiveModel) children(dir string) []string {
	names := map[string]struct{}{}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	for q, u := range m.upper {
		if !strings.HasPrefix(q, prefix) {
			continue
		}
		rest := q[len(prefix):]
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			if u.kind != KindWhiteout {
				names[rest] = struct{}{}
			}
		} else {
			names[rest[:i]] = struct{}{}
		}
	}
	participate := dir == ""
	if dir != "" {
		if u, ok := m.upper[dir]; ok {
			participate = u.kind == KindDir
		} else {
			participate = true
		}
		if participate && !(m.lower[dir] && m.reachable(dir)) {
			participate = false
		}
	}
	if participate {
		for q := range m.lower {
			if !strings.HasPrefix(q, prefix) {
				continue
			}
			rest := q[len(prefix):]
			i := strings.IndexByte(rest, '/')
			name := rest
			if i >= 0 {
				name = rest[:i]
			}
			if _, hidden := m.upper[prefix+name]; hidden {
				continue
			}
			names[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (m *naiveModel) ensureDirs(q string) {
	for _, a := range anc(q) {
		if a == "" {
			continue
		}
		if _, ok := m.upper[a]; !ok {
			m.upper[a] = kinded{kind: KindDir}
		}
	}
}

func (m *naiveModel) discard(q string) {
	for k := range m.upper {
		if k == q || strings.HasPrefix(k, q+"/") {
			delete(m.upper, k)
		}
	}
}

func (m *naiveModel) snapshot() map[string]UpperRecord {
	out := make(map[string]UpperRecord, len(m.upper))
	for k, v := range m.upper {
		out[k] = UpperRecord{Kind: v.kind, Content: v.content}
	}
	return out
}

func errName(e error) string {
	if e == nil {
		return "nil"
	}
	for _, s := range []error{ErrInvalidPath, ErrNotFound, ErrNotDirectory, ErrIsDirectory, ErrExist, ErrDirNotEmpty, ErrCrossLayer, ErrIntoSelf} {
		if errors.Is(e, s) {
			return s.Error()
		}
	}
	return e.Error()
}

func (m *naiveModel) mkdir(p string) error {
	if !isValidPath(p, false) {
		return ErrInvalidPath
	}
	if err := m.checkAncestors(p); err != nil {
		return err
	}
	t, _ := m.resolve(p)
	if t != "missing" {
		return ErrExist
	}
	m.ensureDirs(p)
	k := KindDir
	if u, ok := m.upper[p]; ok && u.kind == KindWhiteout {
		k = KindOpaque
	}
	m.upper[p] = kinded{kind: k}
	return nil
}

func (m *naiveModel) write(p, c string) error {
	if !isValidPath(p, false) {
		return ErrInvalidPath
	}
	if err := m.checkAncestors(p); err != nil {
		return err
	}
	t, _ := m.resolve(p)
	if t == "dir" {
		return ErrIsDirectory
	}
	m.ensureDirs(p)
	m.upper[p] = kinded{kind: KindFile, content: c}
	return nil
}

func (m *naiveModel) remove(p string) error {
	if !isValidPath(p, false) {
		return ErrInvalidPath
	}
	if err := m.checkAncestors(p); err != nil {
		return err
	}
	t, _ := m.resolve(p)
	if t == "missing" {
		return ErrNotFound
	}
	if t == "dir" && len(m.children(p)) > 0 {
		return ErrDirNotEmpty
	}
	if m.reachable(p) {
		m.ensureDirs(p)
		m.discard(p)
		m.upper[p] = kinded{kind: KindWhiteout}
	} else {
		m.discard(p)
	}
	return nil
}

func (m *naiveModel) rename(old, nw string) error {
	if !isValidPath(old, false) || !isValidPath(nw, false) {
		return ErrInvalidPath
	}
	if err := m.checkAncestors(old); err != nil {
		return err
	}
	ot, content := m.resolve(old)
	if ot == "missing" {
		return ErrNotFound
	}
	if err := m.checkAncestors(nw); err != nil {
		return err
	}
	if old == nw || strings.HasPrefix(nw, old+"/") {
		return ErrIntoSelf
	}
	if ot == "dir" {
		u, hasU := m.upper[old]
		lowerDir := m.lower[old] && m.reachable(old)
		if !hasU || (u.kind == KindDir && lowerDir) {
			return ErrCrossLayer
		}
	}
	nt, _ := m.resolve(nw)
	if nt != "missing" {
		switch {
		case ot == "file" && nt == "dir":
			return ErrIsDirectory
		case ot == "dir" && nt == "file":
			return ErrNotDirectory
		case ot == "dir" && nt == "dir":
			if len(m.children(nw)) > 0 {
				return ErrDirNotEmpty
			}
		}
	}
	oldReach := m.reachable(old)
	if ot == "file" {
		m.ensureDirs(nw)
		m.upper[nw] = kinded{kind: KindFile, content: content}
		if oldReach {
			m.ensureDirs(old)
			m.discard(old)
			m.upper[old] = kinded{kind: KindWhiteout}
		} else {
			m.discard(old)
		}
		return nil
	}
	oldRec := m.upper[old]
	uNew, newHasU := m.upper[nw]
	newLowerDir := m.lower[nw] && m.reachable(nw)
	whiteout := newHasU && uNew.kind == KindWhiteout
	m.discard(nw)
	m.ensureDirs(nw)
	sub := map[string]kinded{}
	for q, rec := range m.upper {
		if q == old || strings.HasPrefix(q, old+"/") {
			sub[q] = rec
		}
	}
	for q := range sub {
		delete(m.upper, q)
	}
	for q, rec := range sub {
		m.upper[nw+q[len(old):]] = rec
	}
	head := oldRec.kind
	if head == KindOpaque || whiteout || newLowerDir {
		head = KindOpaque
	}
	m.upper[nw] = kinded{kind: head}
	if oldReach {
		m.ensureDirs(old)
		m.upper[old] = kinded{kind: KindWhiteout}
	}
	return nil
}

var _ = fmt.Sprintf
