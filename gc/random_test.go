package gc

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

// 本文件实现一个独立的朴素模型：同样的规则，但用全表扫描、
// 不做任何索引与计数器优化，每次操作后暴力迭代到不动点。
// 随机测试在大量随机对象图与随机操作序列上，把控制器与朴素模型
// 逐步对照（错误类别 + 全量状态），并打印每次操作的输入、
// 实际输出与判定依据。

type naiveObj struct {
	owners     map[string]bool
	finalizers []string
	deleting   bool
	policy     Policy
	reqTime    time.Time
}

type naiveModel struct {
	objs map[string]*naiveObj
}

func newNaive() *naiveModel { return &naiveModel{objs: make(map[string]*naiveObj)} }

func (m *naiveModel) sortedIDs() []string {
	ids := make([]string, 0, len(m.objs))
	for id := range m.objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// applyDelete 与控制器同义的删除请求语义；返回状态是否改变。
func (m *naiveModel) applyDelete(o *naiveObj, p Policy, now time.Time) bool {
	if !o.deleting {
		o.deleting = true
		o.policy = p
		o.reqTime = now
		return true
	}
	if o.policy == Background && p == Foreground {
		o.policy = Foreground
		return true
	}
	return false
}

// settle 暴力收敛：整表扫描应用两条规则直到不动点。
func (m *naiveModel) settle(now time.Time) {
	for {
		changed := false
		// 规则一：前台传播。
		for _, id := range m.sortedIDs() {
			d := m.objs[id]
			live, fg := 0, 0
			for oid := range d.owners {
				o, ok := m.objs[oid]
				if !ok {
					continue // 属主已不存在
				}
				switch {
				case !o.deleting:
					live++
				case o.policy == Foreground:
					fg++
				}
			}
			if live == 0 && fg > 0 {
				if m.applyDelete(d, Foreground, now) {
					changed = true
				}
			}
		}
		// 规则二：满足移除条件即移除。
		for _, id := range m.sortedIDs() {
			o := m.objs[id]
			if !o.deleting || len(o.finalizers) > 0 {
				continue
			}
			if o.policy == Foreground {
				blocked := false
				for _, d := range m.objs {
					if d.deleting {
						continue
					}
					if blk, ok := d.owners[id]; ok && blk {
						blocked = true
						break
					}
				}
				if blocked {
					continue
				}
			}
			for _, d := range m.objs {
				if _, ok := d.owners[id]; !ok {
					continue
				}
				delete(d.owners, id)
				if o.policy != Orphan && len(d.owners) == 0 {
					m.applyDelete(d, Background, now)
				}
			}
			delete(m.objs, id)
			changed = true
		}
		if !changed {
			return
		}
	}
}

// naiveCycle 自引用或沿属主链（owner -> owner 的 owner ...）能回到 target。
func (m *naiveModel) naiveCycle(target string, refs []OwnerRef) bool {
	for _, r := range refs {
		if r.OwnerID == target {
			return true
		}
		visited := map[string]bool{r.OwnerID: true}
		stack := []string{r.OwnerID}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			o, ok := m.objs[id]
			if !ok {
				continue
			}
			for oid := range o.owners {
				if oid == target {
					return true
				}
				if !visited[oid] {
					visited[oid] = true
					stack = append(stack, oid)
				}
			}
		}
	}
	return false
}

func (m *naiveModel) create(id string, refs []OwnerRef, finalizers []string) *Error {
	if id == "" {
		return newError(KindInvalidArgument, "naive: empty id")
	}
	for _, f := range finalizers {
		if f == "" {
			return newError(KindInvalidArgument, "naive: empty finalizer")
		}
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if r.OwnerID == "" || seen[r.OwnerID] {
			return newError(KindInvalidArgument, "naive: bad owner ref")
		}
		seen[r.OwnerID] = true
	}
	if _, ok := m.objs[id]; ok {
		return newError(KindConflict, "naive: %q exists", id)
	}
	for _, r := range refs {
		if o, ok := m.objs[r.OwnerID]; ok && o.deleting {
			return newError(KindConflict, "naive: owner %q deleting", r.OwnerID)
		}
	}
	if m.naiveCycle(id, refs) {
		return newError(KindCycle, "naive: cycle")
	}
	for _, r := range refs {
		if _, ok := m.objs[r.OwnerID]; !ok {
			return newError(KindOwnerMissing, "naive: owner %q missing", r.OwnerID)
		}
	}
	o := &naiveObj{owners: map[string]bool{}}
	dedup := map[string]bool{}
	for _, f := range finalizers {
		if !dedup[f] {
			dedup[f] = true
			o.finalizers = append(o.finalizers, f)
		}
	}
	for _, r := range refs {
		o.owners[r.OwnerID] = r.Block
	}
	m.objs[id] = o
	return nil
}

func (m *naiveModel) delete(id string, p Policy, now time.Time) *Error {
	if id == "" {
		return newError(KindInvalidArgument, "naive: empty id")
	}
	if !p.valid() {
		return newError(KindInvalidArgument, "naive: bad policy")
	}
	o, ok := m.objs[id]
	if !ok {
		return newError(KindNotFound, "naive: %q missing", id)
	}
	m.applyDelete(o, p, now)
	m.settle(now)
	return nil
}

func (m *naiveModel) addFinalizer(id, name string) *Error {
	if id == "" || name == "" {
		return newError(KindInvalidArgument, "naive: empty id/name")
	}
	o, ok := m.objs[id]
	if !ok {
		return newError(KindNotFound, "naive: %q missing", id)
	}
	if o.deleting {
		return newError(KindConflict, "naive: %q deleting", id)
	}
	for _, f := range o.finalizers {
		if f == name {
			return nil
		}
	}
	o.finalizers = append(o.finalizers, name)
	return nil
}

func (m *naiveModel) removeFinalizer(id, name string, now time.Time) *Error {
	if id == "" || name == "" {
		return newError(KindInvalidArgument, "naive: empty id/name")
	}
	o, ok := m.objs[id]
	if !ok {
		return newError(KindNotFound, "naive: %q missing", id)
	}
	idx := -1
	for i, f := range o.finalizers {
		if f == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return newError(KindNotFound, "naive: finalizer %q missing on %q", name, id)
	}
	o.finalizers = append(o.finalizers[:idx], o.finalizers[idx+1:]...)
	m.settle(now)
	return nil
}

func (m *naiveModel) setOwners(id string, refs []OwnerRef, now time.Time) *Error {
	if id == "" {
		return newError(KindInvalidArgument, "naive: empty id")
	}
	seen := map[string]bool{}
	for _, r := range refs {
		if r.OwnerID == "" || seen[r.OwnerID] {
			return newError(KindInvalidArgument, "naive: bad owner ref")
		}
		seen[r.OwnerID] = true
	}
	o, ok := m.objs[id]
	if !ok {
		return newError(KindNotFound, "naive: %q missing", id)
	}
	if o.deleting {
		return newError(KindConflict, "naive: %q deleting", id)
	}
	for _, r := range refs {
		if ow, ok := m.objs[r.OwnerID]; ok && ow.deleting {
			return newError(KindConflict, "naive: owner %q deleting", r.OwnerID)
		}
	}
	if m.naiveCycle(id, refs) {
		return newError(KindCycle, "naive: cycle")
	}
	for _, r := range refs {
		if _, ok := m.objs[r.OwnerID]; !ok {
			return newError(KindOwnerMissing, "naive: owner %q missing", r.OwnerID)
		}
	}
	o.owners = map[string]bool{}
	for _, r := range refs {
		o.owners[r.OwnerID] = r.Block
	}
	m.settle(now)
	return nil
}

// dump 导出与 Controller.Dump 相同形状的视图用于逐字段对照。
func (m *naiveModel) dump() []View {
	out := make([]View, 0, len(m.objs))
	for _, id := range m.sortedIDs() {
		o := m.objs[id]
		v := View{
			ID:         id,
			Owners:     make([]OwnerRef, 0, len(o.owners)),
			Finalizers: append([]string(nil), o.finalizers...),
			Deleting:   o.deleting,
			Policy:     o.policy,
			ReqTime:    o.reqTime,
		}
		for oid, blk := range o.owners {
			v.Owners = append(v.Owners, OwnerRef{OwnerID: oid, Block: blk})
		}
		sort.Slice(v.Owners, func(i, j int) bool { return v.Owners[i].OwnerID < v.Owners[j].OwnerID })
		out = append(out, v)
	}
	return out
}

// randOp 是一次随机操作。
type randOp struct {
	kind       string // create/delete/addFin/removeFin/setOwners
	id         string
	owners     []OwnerRef
	finalizers []string
	policy     Policy
	name       string
	now        time.Time
}

func (o randOp) String() string {
	switch o.kind {
	case "create":
		return fmt.Sprintf("Create(%q, owners=%v, finalizers=%v)", o.id, o.owners, o.finalizers)
	case "delete":
		return fmt.Sprintf("Delete(%q, %s, %s)", o.id, o.policy, o.now.Format(time.RFC3339))
	case "addFin":
		return fmt.Sprintf("AddFinalizer(%q, %q)", o.id, o.name)
	case "removeFin":
		return fmt.Sprintf("RemoveFinalizer(%q, %q, %s)", o.id, o.name, o.now.Format(time.RFC3339))
	case "setOwners":
		return fmt.Sprintf("SetOwners(%q, %v, %s)", o.id, o.owners, o.now.Format(time.RFC3339))
	}
	return "?"
}

func genOp(rnd *rand.Rand, seq int) randOp {
	id := fmt.Sprintf("n%d", rnd.Intn(24))
	op := randOp{id: id, now: base.Add(time.Duration(seq) * time.Second)}
	switch rnd.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
		20, 21, 22, 23, 24, 25, 26, 27, 28, 29: // 30% create
		op.kind = "create"
		op.owners = genRefs(rnd)
		op.finalizers = genFinalizers(rnd)
	case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44,
		45, 46, 47, 48, 49, 50, 51, 52, 53, 54: // 25% delete
		op.kind = "delete"
		op.policy = []Policy{Background, Foreground, Orphan}[rnd.Intn(3)]
	case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64: // 10% addFinalizer
		op.kind = "addFin"
		op.name = fmt.Sprintf("f%d", rnd.Intn(3))
	case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79: // 15% removeFinalizer
		op.kind = "removeFin"
		op.name = fmt.Sprintf("f%d", rnd.Intn(3))
	default: // 20% setOwners
		op.kind = "setOwners"
		op.owners = genRefs(rnd)
	}
	return op
}

func genRefs(rnd *rand.Rand) []OwnerRef {
	n := rnd.Intn(4)
	refs := make([]OwnerRef, 0, n)
	for i := 0; i < n; i++ {
		refs = append(refs, OwnerRef{
			OwnerID: fmt.Sprintf("n%d", rnd.Intn(24)),
			Block:   rnd.Intn(2) == 0,
		})
	}
	return refs
}

func genFinalizers(rnd *rand.Rand) []string {
	n := rnd.Intn(3)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("f%d", rnd.Intn(3)))
	}
	return out
}

func applyToController(c *Controller, op randOp) *Error {
	switch op.kind {
	case "create":
		return c.Create(op.id, op.owners, op.finalizers)
	case "delete":
		return c.Delete(op.id, op.policy, op.now)
	case "addFin":
		return c.AddFinalizer(op.id, op.name)
	case "removeFin":
		return c.RemoveFinalizer(op.id, op.name, op.now)
	case "setOwners":
		return c.SetOwners(op.id, op.owners, op.now)
	}
	panic("bad op")
}

func applyToNaive(m *naiveModel, op randOp) *Error {
	switch op.kind {
	case "create":
		return m.create(op.id, op.owners, op.finalizers)
	case "delete":
		return m.delete(op.id, op.policy, op.now)
	case "addFin":
		return m.addFinalizer(op.id, op.name)
	case "removeFin":
		return m.removeFinalizer(op.id, op.name, op.now)
	case "setOwners":
		return m.setOwners(op.id, op.owners, op.now)
	}
	panic("bad op")
}

func errKind(err *Error) string {
	if err == nil {
		return "ok"
	}
	return err.Kind.String()
}

// checkInvariants 从控制器内部状态出发，验证可由视图推导出的不变量，
// 并从头重算全部计数器与反向索引进行交叉验证。
func checkInvariants(t *testing.T, c *Controller) {
	t.Helper()
	views := map[string]View{}
	for _, v := range c.Dump() {
		views[v.ID] = v
	}
	for _, v := range views {
		// 属主必须存在、不得自引用、不得重复（map 保证不重复）。
		seen := map[string]bool{}
		for _, r := range v.Owners {
			if r.OwnerID == v.ID {
				t.Fatalf("invariant: %q references itself", v.ID)
			}
			if _, ok := views[r.OwnerID]; !ok {
				t.Fatalf("invariant: %q references missing owner %q", v.ID, r.OwnerID)
			}
			if seen[r.OwnerID] {
				t.Fatalf("invariant: %q duplicates owner %q", v.ID, r.OwnerID)
			}
			seen[r.OwnerID] = true
		}
		// 满足移除条件的对象必须已被移除，不允许延迟：
		// 删除中 => 有终结器，或（前台且有存活阻塞依赖者）。
		if v.Deleting {
			blocked := false
			if v.Policy == Foreground {
				for _, d := range views {
					if d.Deleting {
						continue
					}
					for _, r := range d.Owners {
						if r.OwnerID == v.ID && r.Block {
							blocked = true
						}
					}
				}
			}
			if len(v.Finalizers) == 0 && !blocked {
				t.Fatalf("invariant: %q is deleting and removable but still present", v.ID)
			}
		}
	}
	// 无环（DFS 三色标记）。
	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		for _, r := range views[id].Owners {
			switch color[r.OwnerID] {
			case gray:
				return true
			case white:
				if visit(r.OwnerID) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for id := range views {
		if color[id] == white && visit(id) {
			t.Fatalf("invariant: owner cycle detected involving %q", id)
		}
	}
	// 从头重算计数器与反向索引，与控制器内部状态交叉验证。
	type counters struct{ live, fg, blk int }
	want := map[string]*counters{}
	depIdx := map[string]map[string]bool{}
	for id := range c.objects {
		want[id] = &counters{}
	}
	for id, o := range c.objects {
		for oid, blk := range o.owners {
			owner := c.objects[oid]
			if !owner.deleting {
				want[id].live++
			} else if owner.policy == Foreground {
				want[id].fg++
			}
			if blk && !o.deleting {
				want[oid].blk++
			}
			if depIdx[oid] == nil {
				depIdx[oid] = map[string]bool{}
			}
			depIdx[oid][id] = true
		}
	}
	for id, o := range c.objects {
		w := want[id]
		if o.liveOwners != w.live || o.fgOwners != w.fg || o.blockingDeps != w.blk {
			t.Fatalf("invariant: counters of %q = (%d,%d,%d), want (%d,%d,%d)",
				id, o.liveOwners, o.fgOwners, o.blockingDeps, w.live, w.fg, w.blk)
		}
	}
	if len(c.dependents) != len(depIdx) {
		t.Fatalf("invariant: dependents index size %d, want %d", len(c.dependents), len(depIdx))
	}
	for oid, set := range c.dependents {
		if len(set) != len(depIdx[oid]) {
			t.Fatalf("invariant: dependents[%q] size %d, want %d", oid, len(set), len(depIdx[oid]))
		}
		for id := range set {
			if !depIdx[oid][id] {
				t.Fatalf("invariant: dependents[%q] contains stale %q", oid, id)
			}
		}
	}
}

// TestRandomDifferential 在大量随机对象图与随机操作序列上，
// 把控制器与独立朴素模型逐步对照。
func TestRandomDifferential(t *testing.T) {
	const seeds = 50
	const opsPerSeed = 400
	for seed := int64(1); seed <= seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			c := New()
			m := newNaive()
			rnd := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerSeed; i++ {
				op := genOp(rnd, i)
				errC := applyToController(c, op)
				errM := applyToNaive(m, op)

				basis := "错误类别一致"
				if errKind(errC) != errKind(errM) {
					t.Fatalf("op %d %s: controller err=%v, naive err=%v", i, op, errC, errM)
				}
				got, want := c.Dump(), m.dump()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("op %d %s: state mismatch\ncontroller: %v\nnaive: %v", i, op, got, want)
				}
				basis += fmt.Sprintf("；全量状态一致（%d 个对象）", len(got))
				checkInvariants(t, c)
				basis += "；不变量与计数器交叉验证通过"
				t.Logf("op %d: input=%s output=%s 判定依据: %s", i, op, errKind(errC), basis)
			}
		})
	}
}
