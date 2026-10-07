package lifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel 是独立实现的朴素生命周期模型：刻意用无索引的线性历史扫描
// 回答所有问题（与 Coordinator 的 O(1) 字段判定形成不同实现路径），
// 作为随机差分测试的对照预言机。
type naiveModel struct {
	perms  map[string]*PermissionEntry
	ltypes map[string]LinkBehavior
	// obj -> ordered events; 事件用简单结构体记录
	objEvents map[string][]naiveEvent
	links     map[string]*naiveLink
	clock     int64
}

type naiveEvent struct {
	kind     EventKind
	at       int64
	interval int64
	actor    string
	target   int64 // revive -> 删除时点
	restore  bool
}

type naiveLink struct {
	id, typ, src, dst string
	available         bool
	invalidatedAt     int64
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		perms:     map[string]*PermissionEntry{},
		ltypes:    map[string]LinkBehavior{},
		objEvents: map[string][]naiveEvent{},
		links:     map[string]*naiveLink{},
	}
}

func (m *naiveModel) tick() int64 { m.clock++; return m.clock }

func (m *naiveModel) alive(obj string) bool {
	evs := m.objEvents[obj]
	if len(evs) == 0 {
		return false
	}
	return evs[len(evs)-1].kind != EventDelete
}

func (m *naiveModel) latestDelete(obj string) int64 {
	for i := len(m.objEvents[obj]) - 1; i >= 0; i-- {
		if m.objEvents[obj][i].kind == EventDelete {
			return m.objEvents[obj][i].at
		}
	}
	return 0
}

func (m *naiveModel) intervalCount(obj string) int64 {
	var n int64
	for _, ev := range m.objEvents[obj] {
		if ev.kind == EventBirth {
			n++
		}
		if ev.kind == EventRevive {
			n++
		}
	}
	return n
}

// linkEligible 用朴素全扫描判定：与正式实现走完全不同的代码路径。
func (m *naiveModel) linkEligible(obj string, link *naiveLink, deleteAt int64) bool {
	if link.typ == "" || m.ltypes[link.typ] != LinkInvalidatesWithEndpoint {
		return false
	}
	if link.src != obj && link.dst != obj {
		return false
	}
	// 朴素方式：扫描该链接本应只在删除事件时失效，核对其失效时点。
	if link.available {
		return false
	}
	return link.invalidatedAt == deleteAt
}

type opResult struct {
	errCategory string // "" 表示成功
}

func errCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrPermissionDenied):
		return "permission"
	case errors.Is(err, ErrStateMismatch):
		return "state"
	case errors.Is(err, ErrObjectDeleted):
		return "deleted"
	case errors.Is(err, ErrStaleDeleteTarget):
		return "stale"
	case errors.Is(err, ErrLinkCondition):
		return "link"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	default:
		return "other"
	}
}

func (m *naiveModel) apply(op randomOp) opResult {
	switch op.kind {
	case "create":
		if _, ok := m.objEvents[op.obj]; ok {
			return opResult{"state"}
		}
		t := m.tick()
		m.objEvents[op.obj] = []naiveEvent{{kind: EventBirth, at: t, interval: 1, actor: op.actor}}
		return opResult{}
	case "write":
		if _, ok := m.objEvents[op.obj]; !ok {
			return opResult{"notfound"}
		}
		if !m.alive(op.obj) {
			return opResult{"deleted"}
		}
		m.tick()
		iv := m.intervalCount(op.obj)
		m.objEvents[op.obj] = append(m.objEvents[op.obj], naiveEvent{kind: EventWrite, at: m.clock, interval: iv, actor: op.actor})
		return opResult{}
	case "addlink":
		if _, ok := m.ltypes[op.linkType]; !ok {
			return opResult{"notfound"}
		}
		if _, ok := m.objEvents[op.src]; !ok {
			return opResult{"notfound"}
		}
		if _, ok := m.objEvents[op.dst]; !ok {
			return opResult{"notfound"}
		}
		if _, ok := m.links[op.link]; ok {
			return opResult{"state"}
		}
		m.links[op.link] = &naiveLink{id: op.link, typ: op.linkType, src: op.src, dst: op.dst, available: true}
		return opResult{}
	case "delete":
		entry, ok := m.perms[op.perm]
		if !ok || entry.Revoked || !containsGrant(entry.Grants, ActionDelete) {
			return opResult{"permission"}
		}
		if _, ok := m.objEvents[op.obj]; !ok {
			return opResult{"notfound"}
		}
		if !m.alive(op.obj) {
			return opResult{"state"}
		}
		t := m.tick()
		iv := m.intervalCount(op.obj)
		for _, l := range m.links {
			if (l.src == op.obj || l.dst == op.obj) && m.ltypes[l.typ] == LinkInvalidatesWithEndpoint && l.available {
				l.available = false
				l.invalidatedAt = t
			}
		}
		m.objEvents[op.obj] = append(m.objEvents[op.obj], naiveEvent{kind: EventDelete, at: t, interval: iv, actor: op.actor})
		return opResult{}
	case "revive":
		entry, ok := m.perms[op.perm]
		if !ok || entry.Revoked || !containsGrant(entry.Grants, ActionRevive) {
			return opResult{"permission"}
		}
		if _, ok := m.objEvents[op.obj]; !ok {
			return opResult{"notfound"}
		}
		if m.alive(op.obj) {
			return opResult{"state"}
		}
		latest := m.latestDelete(op.obj)
		if op.target != latest {
			return opResult{"stale"}
		}
		if op.restore {
			// 朴素模型同样先全量校验后变更，保证原子性。
			var chosen []*naiveLink
			if len(op.linkIDs) > 0 {
				for _, lid := range op.linkIDs {
					l, ok := m.links[lid]
					if !ok {
						return opResult{"notfound"}
					}
					if !m.linkEligible(op.obj, l, latest) {
						return opResult{"link"}
					}
					chosen = append(chosen, l)
				}
			} else {
				ids := make([]string, 0)
				for id := range m.links {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				for _, id := range ids {
					l := m.links[id]
					if !l.available && (l.src == op.obj || l.dst == op.obj) {
						if !m.linkEligible(op.obj, l, latest) {
							return opResult{"link"}
						}
						chosen = append(chosen, l)
					}
				}
			}
			t := m.tick()
			iv := m.intervalCount(op.obj) + 1
			for _, l := range chosen {
				l.available = true
				l.invalidatedAt = 0
			}
			m.objEvents[op.obj] = append(m.objEvents[op.obj], naiveEvent{
				kind: EventRevive, at: t, interval: iv, actor: op.actor, target: latest, restore: true,
			})
			return opResult{}
		}
		t := m.tick()
		iv := m.intervalCount(op.obj) + 1
		m.objEvents[op.obj] = append(m.objEvents[op.obj], naiveEvent{
			kind: EventRevive, at: t, interval: iv, actor: op.actor, target: latest, restore: false,
		})
		return opResult{}
	}
	return opResult{"other"}
}

type randomOp struct {
	kind     string
	obj      string
	actor    string
	perm     string
	target   int64
	restore  bool
	link     string
	linkType string
	src, dst string
	linkIDs  []string
}

// TestRandomDifferential 随机生成操作序列，在正式实现与朴素模型上同步执行，
// 逐步比对错误类别、存活状态、区间数、事件归属与链接状态。
func TestRandomDifferential(t *testing.T) {
	const iterations = 60
	const seqLen = 150
	rng := rand.New(rand.NewSource(20261007))
	objects := []string{"o1", "o2", "o3"}
	fragileLinks := []string{"f1", "f2", "f3", "f4"}
	independentLinks := []string{"i1", "i2"}

	for iter := 0; iter < iterations; iter++ {
		var buf bytes.Buffer
		c := New(WithLogWriter(&buf))
		m := newNaiveModel()

		// 权限：部分条目带/不带授权，部分在序列中途吊销。
		perms := []string{"p-both", "p-del", "p-rev", "p-none", "p-ghost"}
		must(t, c.UpsertPermission(&PermissionEntry{ID: "p-both", Grants: []string{ActionDelete, ActionRevive}, Version: 1}))
		must(t, c.UpsertPermission(&PermissionEntry{ID: "p-del", Grants: []string{ActionDelete}, Version: 1}))
		must(t, c.UpsertPermission(&PermissionEntry{ID: "p-rev", Grants: []string{ActionRevive}, Version: 1}))
		must(t, c.UpsertPermission(&PermissionEntry{ID: "p-none", Grants: nil, Version: 1}))
		m.perms["p-both"] = &PermissionEntry{ID: "p-both", Grants: []string{ActionDelete, ActionRevive}}
		m.perms["p-del"] = &PermissionEntry{ID: "p-del", Grants: []string{ActionDelete}}
		m.perms["p-rev"] = &PermissionEntry{ID: "p-rev", Grants: []string{ActionRevive}}
		m.perms["p-none"] = &PermissionEntry{ID: "p-none"}
		must(t, c.UpsertLinkType(&LinkType{ID: "lt-f", Behavior: LinkInvalidatesWithEndpoint}))
		must(t, c.UpsertLinkType(&LinkType{ID: "lt-i", Behavior: LinkKeepsIndependent}))
		m.ltypes["lt-f"] = LinkInvalidatesWithEndpoint
		m.ltypes["lt-i"] = LinkKeepsIndependent

		var ops []randomOp
		for _, obj := range objects {
			ops = append(ops, randomOp{kind: "create", obj: obj, actor: "seed"})
		}

		linkCounter := 0
		addKnownLinks := func() {
			for k, src := range objects {
				dst := objects[(k+1)%len(objects)]
				fid := fragileLinks[linkCounter%len(fragileLinks)]
				iid := independentLinks[linkCounter%len(independentLinks)]
				linkCounter++
				ops = append(ops, randomOp{kind: "addlink", link: fid, linkType: "lt-f", src: src, dst: dst})
				ops = append(ops, randomOp{kind: "addlink", link: iid, linkType: "lt-i", src: src, dst: dst})
			}
		}
		addKnownLinks()

		for step := 0; step < seqLen; step++ {
			obj := objects[rng.Intn(len(objects))]
			perm := perms[rng.Intn(len(perms))]
			switch rng.Intn(6) {
			case 0, 1:
				ops = append(ops, randomOp{kind: "write", obj: obj, actor: "w", perm: perm})
			case 2:
				ops = append(ops, randomOp{kind: "delete", obj: obj, actor: "d", perm: perm})
			case 3, 4:
				// 复活目标有时故意取错（1 或历史删除时点），触发 stale 分支。
				target := int64(0)
				rep, _ := c.Audit(obj)
				if rep != nil {
					for _, ev := range rep.Events {
						if ev.Kind == EventDelete {
							target = ev.At
						}
					}
					if rng.Intn(3) == 0 && len(rep.Events) > 1 {
						for _, ev := range rep.Events {
							if ev.Kind == EventDelete {
								target = ev.At
								break
							}
						}
					}
				}
				restore := rng.Intn(2) == 0
				var lids []string
				if restore && rng.Intn(2) == 0 {
					name := fragileLinks[rng.Intn(len(fragileLinks))]
					lids = []string{name}
					if rng.Intn(3) == 0 {
						lids = append(lids, independentLinks[rng.Intn(len(independentLinks))])
					}
				}
				ops = append(ops, randomOp{kind: "revive", obj: obj, actor: "r", perm: perm, target: target, restore: restore, linkIDs: lids})
			case 5:
				// 偶发再登记一批链接，让不同删除时点上的失效链接共存。
				addKnownLinks()
			}
		}

		for idx, op := range ops {
			got := opResult{errCategory(c.execRandom(op))}
			want := m.apply(op)
			if got.errCategory != want.errCategory {
				t.Fatalf("iter=%d step=%d op=%+v\nwant category=%q got=%q",
					iter, idx, op, want.errCategory, got.errCategory)
			}
			compareState(t, c, m, objects, fragileLinks, independentLinks, iter, idx, op)
		}
	}
}

func (c *Coordinator) execRandom(op randomOp) error {
	switch op.kind {
	case "create":
		return c.CreateObject(op.obj, op.actor)
	case "write":
		return c.Write(op.obj, op.actor, "random-write")
	case "addlink":
		return c.AddLink(&LinkRecord{ID: op.link, TypeID: op.linkType, SourceID: op.src, TargetID: op.dst})
	case "delete":
		return c.DeleteObject(op.obj, op.actor, op.perm)
	case "revive":
		return c.ReviveObject(op.obj, op.actor, op.perm, op.target, op.restore, op.linkIDs)
	}
	return fmt.Errorf("unknown op")
}

func compareState(t *testing.T, c *Coordinator, m *naiveModel, objects, fragile, independent []string, iter, idx int, op randomOp) {
	t.Helper()
	for _, obj := range objects {
		if _, exists := m.objEvents[obj]; !exists {
			continue
		}
		rep, err := c.Audit(obj)
		if err != nil {
			t.Fatalf("audit must always work: iter=%d step=%d obj=%s err=%v", iter, idx, obj, err)
		}
		if len(rep.Intervals) != int(m.intervalCount(obj)) {
			t.Fatalf("interval count mismatch iter=%d step=%d obj=%s op=%+v coord=%d naive=%d",
				iter, idx, obj, op, len(rep.Intervals), m.intervalCount(obj))
		}
		coordAlive := rep.Intervals[len(rep.Intervals)-1].EndedAt == 0
		if coordAlive != m.alive(obj) {
			t.Fatalf("alive mismatch iter=%d step=%d obj=%s coord=%v naive=%v", iter, idx, obj, coordAlive, m.alive(obj))
		}
		if len(rep.Events) != len(m.objEvents[obj]) {
			t.Fatalf("event count mismatch iter=%d step=%d obj=%s coord=%d naive=%d",
				iter, idx, obj, len(rep.Events), len(m.objEvents[obj]))
		}
		for i, ev := range rep.Events {
			nv := m.objEvents[obj][i]
			if ev.Kind != nv.kind || ev.At != nv.at || ev.At == 0 {
				t.Fatalf("event %d mismatch iter=%d step=%d obj=%s coord=%+v naive=%+v", i, iter, idx, obj, ev, nv)
			}
			var ivSeq int64
			for _, iv := range rep.Intervals {
				if iv.ID == ev.IntervalID {
					ivSeq = iv.Seq
				}
			}
			if ivSeq != nv.interval {
				t.Fatalf("event %d interval attribution mismatch iter=%d step=%d coord=%d naive=%d",
					i, iter, idx, ivSeq, nv.interval)
			}
			if nv.kind == EventRevive && ev.TargetDeleteAt != nv.target {
				t.Fatalf("revive target mismatch iter=%d step=%d coord=%d naive=%d", iter, idx, ev.TargetDeleteAt, nv.target)
			}
		}
	}
	for _, names := range [][]string{fragile, independent} {
		for _, name := range names {
			cl, cok := c.links[name]
			nl, nok := m.links[name]
			if cok != nok {
				t.Fatalf("link presence mismatch %s: coord=%v naive=%v", name, cok, nok)
			}
			if !cok {
				continue
			}
			coordAvail := cl.Status == LinkAvailable
			if coordAvail != nl.available || cl.InvalidatedAt != nl.invalidatedAt {
				t.Fatalf("link %s state mismatch iter=%d step=%d op=%+v coord={avail=%v at=%d} naive={avail=%v at=%d}",
					name, iter, idx, op, coordAvail, cl.InvalidatedAt, nl.available, nl.invalidatedAt)
			}
		}
	}
}
