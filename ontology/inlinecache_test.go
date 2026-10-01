package ontology

import (
	"reflect"
	"testing"
)

// ---- 朴素参考模型：独立按题目规则实现，用于与 Manager 逐步对照 ----

type naiveEntry struct {
	shape  int
	target any
}

type naiveSite struct {
	entries []naiveEntry
	mega    bool
	hits    int64
	misses  int64
	megaOps int64
}

type naiveManager struct {
	limit int
	table map[int]any
	sites map[string]*naiveSite
}

func newNaive(m int) *naiveManager {
	return &naiveManager{limit: m, table: map[int]any{}, sites: map[string]*naiveSite{}}
}

func (n *naiveManager) create(name string) ErrorCode {
	if _, ok := n.sites[name]; ok {
		return ErrSiteExists
	}
	n.sites[name] = &naiveSite{}
	return ""
}

func (n *naiveManager) define(shape int, target any) ErrorCode {
	if shape <= 0 {
		return ErrInvalidShape
	}
	old, defined := n.table[shape]
	if defined && reflect.DeepEqual(old, target) {
		return ""
	}
	n.table[shape] = target
	if !defined {
		return ""
	}
	for _, st := range n.sites {
		if st.mega {
			continue
		}
		for i, e := range st.entries {
			if e.shape == shape {
				st.entries = append(st.entries[:i], st.entries[i+1:]...)
				break
			}
		}
	}
	return ""
}

func (n *naiveManager) access(name string, shape int) (any, ErrorCode) {
	if shape <= 0 {
		return nil, ErrInvalidShape
	}
	st, ok := n.sites[name]
	if !ok {
		return nil, ErrSiteNotFound
	}
	target, defined := n.table[shape]
	if !defined {
		return nil, ErrShapeUndefined
	}
	if st.mega {
		st.megaOps++
		return target, ""
	}
	for _, e := range st.entries {
		if e.shape == shape {
			st.hits++
			return e.target, ""
		}
	}
	st.misses++
	if len(st.entries) >= n.limit {
		st.entries = nil
		st.mega = true
	} else {
		st.entries = append(st.entries, naiveEntry{shape, target})
	}
	return target, ""
}

type modelState struct {
	table map[int]any
	sites map[string]SiteSnapshot
}

func dumpNaive(n *naiveManager) modelState {
	ms := modelState{table: map[int]any{}, sites: map[string]SiteSnapshot{}}
	for k, v := range n.table {
		ms.table[k] = v
	}
	for name, st := range n.sites {
		state := StateEmpty
		switch {
		case st.mega:
			state = StateMegamorphic
		case len(st.entries) == 1:
			state = StateMonomorphic
		case len(st.entries) >= 2:
			state = StatePolymorphic
		}
		snap := SiteSnapshot{
			Name:  name,
			State: state,
			Stats: Stats{Hits: st.hits, Misses: st.misses, Megamorphic: st.megaOps},
		}
		for _, e := range st.entries {
			snap.Entries = append(snap.Entries, Entry{Shape: e.shape, Target: e.target})
		}
		ms.sites[name] = snap
	}
	return ms
}

func dumpManager(t *testing.T, m *Manager) modelState {
	ms := modelState{table: m.TableSnapshot(), sites: map[string]SiteSnapshot{}}
	for _, name := range m.Sites() {
		snap, err := m.Snapshot(name)
		if err != nil {
			if t != nil {
				t.Fatalf("snapshot %s: %v", name, err)
			}
			panic(err)
		}
		ms.sites[name] = snap
	}
	return ms
}

// ---- 操作脚本（同时驱动 Manager 与朴素模型） ----

type scriptOp struct {
	kind   string // "define" | "create" | "access"
	name   string
	shape  int
	target any
}

func runOnManager(m *Manager, op scriptOp) (any, ErrorCode) {
	switch op.kind {
	case "define":
		return nil, ErrorCodeOf(m.Define(op.shape, op.target))
	case "create":
		return nil, ErrorCodeOf(m.CreateSite(op.name))
	default:
		v, err := m.Access(op.name, op.shape)
		return v, ErrorCodeOf(err)
	}
}

func runOnNaive(n *naiveManager, op scriptOp) (any, ErrorCode) {
	switch op.kind {
	case "define":
		return nil, n.define(op.shape, op.target)
	case "create":
		return nil, n.create(op.name)
	default:
		return n.access(op.name, op.shape)
	}
}
