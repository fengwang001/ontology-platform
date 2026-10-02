package dirtyflush

import (
	"errors"
	"sort"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func rejectIs(t *testing.T, err error, want RejectReason, ctx string) {
	t.Helper()
	var oe *OpError
	if !errors.As(err, &oe) {
		t.Fatalf("%s: expected OpError, got %v", ctx, err)
	}
	if oe.Reason != want {
		t.Fatalf("%s: expected reason %d, got %d (%v)", ctx, want, oe.Reason, err)
	}
}

// assertInvariants 校验题述所有“任何时刻”不变量。
func assertInvariants(t *testing.T, m *Manager) {
	t.Helper()
	entries := m.OrderEntries()
	if len(entries) != m.DirtyCount() {
		t.Fatalf("dirty count %d != list size %d", m.DirtyCount(), len(entries))
	}
	if m.DirtyCount() > m.cap {
		t.Fatalf("dirty pages %d exceed capacity %d", m.DirtyCount(), m.cap)
	}
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1], entries[i]
		if !keyLess(a.Oldest, a.Page, b.Oldest, b.Page) {
			t.Fatalf("list not strictly ascending at %d: %+v %+v", i, a, b)
		}
	}
	for _, e := range entries {
		st := m.pages[e.Page]
		if !st.dirty {
			t.Fatalf("clean page %d in list", e.Page)
		}
		if st.oldest != e.Oldest {
			t.Fatalf("page %d oldest mismatch: state %d list %d", e.Page, st.oldest, e.Oldest)
		}
		if st.oldest > st.lsn {
			t.Fatalf("page %d oldest %d > lsn %d", e.Page, st.oldest, st.lsn)
		}
	}
	// 干净页无出边；出边目标合法；图无环。
	for p := 0; p <= MaxPage; p++ {
		st := m.pages[p]
		if !st.dirty && len(m.out[p]) > 0 {
			t.Fatalf("clean page %d has out edges %v", p, m.out[p])
		}
		for b := range m.out[p] {
			if _, ok := m.in[b]; !ok {
				t.Fatalf("edge %d->%d missing reverse edge", p, b)
			}
		}
	}
	if hasCycle(m) {
		t.Fatalf("dependency graph has a cycle")
	}
	// Checkpoint 不大于任一脏页 oldest。
	cp := m.Checkpoint()
	for _, e := range entries {
		if cp > e.Oldest {
			t.Fatalf("checkpoint %d > oldest %d of page %d", cp, e.Oldest, e.Page)
		}
	}
}

func hasCycle(m *Manager) bool {
	color := make(map[int]uint8) // 0 white, 1 gray, 2 black
	var dfs func(int) bool
	dfs = func(u int) bool {
		color[u] = 1
		outs := make([]int, 0, len(m.out[u]))
		for v := range m.out[u] {
			outs = append(outs, v)
		}
		sort.Ints(outs)
		for _, v := range outs {
			switch color[v] {
			case 1:
				return true
			case 0:
				if dfs(v) {
					return true
				}
			}
		}
		color[u] = 2
		return false
	}
	var nodes []int
	for u := range m.out {
		nodes = append(nodes, u)
	}
	sort.Ints(nodes)
	for _, u := range nodes {
		if color[u] == 0 && dfs(u) {
			return true
		}
	}
	return false
}

// TestSpecWalkthrough 复现题目给出的完整示例。
func TestSpecWalkthrough(t *testing.T) {
	m, err := New(10)
	mustOK(t, err, "New")
	mustOK(t, m.Modify(1, 10), "m1")
	mustOK(t, m.Modify(2, 20), "m2")
	mustOK(t, m.Modify(3, 30), "m3")
	if cp := m.Checkpoint(); cp != 10 {
		t.Fatalf("checkpoint = %d, want 10", cp)
	}
	mustOK(t, m.AddDep(3, 1), "AddDep(3,1)")
	rejectIs(t, m.AddDep(1, 3), ReasonCycle, "AddDep(1,3)")
	plan, err := m.Plan(25)
	mustOK(t, err, "Plan")
	if got := plan; len(got) != 3 || got[0] != 3 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("plan = %v, want [3 1 2]", got)
	}
	mustOK(t, m.SetFlushed(25), "SetFlushed 25")
	_, err = m.FlushStart(3)
	rejectIs(t, err, ReasonLogNotDurable, "FlushStart(3)@25")
	mustOK(t, m.SetFlushed(30), "SetFlushed 30")
	_, err = m.FlushStart(1)
	rejectIs(t, err, ReasonPredecessorDirty, "FlushStart(1)")
	snap, err := m.FlushStart(3)
	mustOK(t, err, "FlushStart(3)")
	if snap != 30 {
		t.Fatalf("snap = %d, want 30", snap)
	}
	mustOK(t, m.Modify(3, 40), "Modify(3,40)")
	if pg, _ := m.Snapshot(3); pg.FirstAfter != 40 {
		t.Fatalf("firstAfter = %d, want 40", pg.FirstAfter)
	}
	mustOK(t, m.FlushDone(3), "FlushDone(3)")
	pg, _ := m.Snapshot(3)
	if !pg.Dirty || pg.Oldest != 40 || pg.LSN != 40 || pg.InFlight {
		t.Fatalf("page3 = %+v, want dirty oldest=40 lsn=40 not inflight", pg)
	}
	if edges := m.OutEdges(3); len(edges) != 1 || edges[0] != 1 {
		t.Fatalf("edge 3->1 should survive, got %v", edges)
	}
	if cp := m.Checkpoint(); cp != 10 {
		t.Fatalf("checkpoint = %d, want 10", cp)
	}
	assertInvariants(t, m)
}
