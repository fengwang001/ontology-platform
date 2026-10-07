package validate

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// naiveModel 是独立编写的朴素串行参考实现：一把大锁，状态就地修改。
// 它刻意不共享生产实现的任何调度/拷贝代码路径，只接受同一套“操作流”。
//
// 在并发测试中，所有对生产 Registry 的成功变更都在 linearizer 临界区内
// 同步作用到 naiveModel，因此两者共享同一个显式全序；生产 Validate 在
// 临界区外任意时刻读到的快照版本，必为该全序中某个已存在时刻。
type naiveModel struct {
	groups map[string]GroupSpec
	hooks  map[string][]Hook[target]
	ids    map[string]string
	opSeq  uint64
	// versionedSnaps[version] 是该全序时刻冻结的结构化快照（值拷贝）。
	// 只保存存活钩子，不含墓碑；活动状态后续变更绝不影响旧快照。
	versionedSnaps map[uint64][]snapshotGroupLite
}

type hookLite struct {
	id    string
	group string
	cond  byte
}

type snapshotGroupLite struct {
	name         string
	shortCircuit bool
	hooks        []hookLite
}

func newNaiveModel(groups ...GroupSpec) *naiveModel {
	n := &naiveModel{
		groups:         map[string]GroupSpec{},
		hooks:          map[string][]Hook[target]{},
		ids:            map[string]string{},
		versionedSnaps: map[uint64][]snapshotGroupLite{},
	}
	for _, g := range groups {
		n.groups[g.Name] = g
		n.hooks[g.Name] = nil
	}
	return n
}

func (n *naiveModel) orderedGroupsLocked() []string {
	order := make([]string, 0, len(n.groups))
	for name := range n.groups {
		order = append(order, name)
	}
	sort.Slice(order, func(i, j int) bool {
		gi, gj := n.groups[order[i]], n.groups[order[j]]
		if gi.Priority != gj.Priority {
			return gi.Priority > gj.Priority
		}
		return order[i] < order[j]
	})
	return order
}

// freezeLocked 冻结当前活动状态为结构化快照；钩子行为一并固化（行为在创建
// 时即不可变，这里读取安全），保证推演旧版本时不依赖已前进的活动状态。
func (n *naiveModel) freezeLocked(beh *hookBehaviors) []snapshotGroupLite {
	out := make([]snapshotGroupLite, 0, len(n.groups))
	for _, name := range n.orderedGroupsLocked() {
		spec := n.groups[name]
		g := snapshotGroupLite{name: name, shortCircuit: spec.ShortCircuit}
		for _, h := range n.hooks[name] {
			g.hooks = append(g.hooks, hookLite{id: h.ID, group: name, cond: beh.get(h.ID)})
		}
		out = append(out, g)
	}
	return out
}

func (n *naiveModel) commitLocked(beh *hookBehaviors) {
	n.opSeq++
	n.versionedSnaps[n.opSeq] = n.freezeLocked(beh)
}

// snapshotLocked 冻结当前状态但不推进版本，供测试取“当前时刻”的对照快照。
func (n *naiveModel) snapshotLocked(beh *hookBehaviors) []snapshotGroupLite {
	return n.freezeLocked(beh)
}

func (n *naiveModel) registerLocked(h Hook[target], beh *hookBehaviors) bool {
	if _, dup := n.ids[h.ID]; dup {
		return false
	}
	if _, ok := n.groups[h.Group]; !ok {
		return false
	}
	n.ids[h.ID] = h.Group
	n.hooks[h.Group] = append(n.hooks[h.Group], h)
	n.commitLocked(beh)
	return true
}

func (n *naiveModel) registerBatchLocked(hs []Hook[target], beh *hookBehaviors) bool {
	for _, h := range hs {
		if _, dup := n.ids[h.ID]; dup {
			return false
		}
		if _, ok := n.groups[h.Group]; !ok {
			return false
		}
	}
	for _, h := range hs {
		n.ids[h.ID] = h.Group
		n.hooks[h.Group] = append(n.hooks[h.Group], h)
	}
	n.commitLocked(beh)
	return true
}

func (n *naiveModel) unregisterLocked(id string, beh *hookBehaviors) bool {
	group, ok := n.ids[id]
	if !ok {
		return false
	}
	delete(n.ids, id)
	old := n.hooks[group]
	// 必须分配新切片：旧版本快照引用旧底层数组，就地过滤（old[:0]）
	// 会篡改历史快照，使旧版本被未来注销污染。
	kept := make([]Hook[target], 0, len(old))
	for _, h := range old {
		if h.ID != id {
			kept = append(kept, h)
		}
	}
	n.hooks[group] = kept
	n.commitLocked(beh)
	return true
}

func (n *naiveModel) snapshotAt(version uint64) []snapshotGroupLite {
	if version == 0 {
		// 版本 0：仅有空分组。
		out := make([]snapshotGroupLite, 0, len(n.groups))
		for _, name := range func() []string {
			order := make([]string, 0, len(n.groups))
			for name := range n.groups {
				order = append(order, name)
			}
			sort.Slice(order, func(i, j int) bool {
				gi, gj := n.groups[order[i]], n.groups[order[j]]
				if gi.Priority != gj.Priority {
					return gi.Priority > gj.Priority
				}
				return order[i] < order[j]
			})
			return order
		}() {
			out = append(out, snapshotGroupLite{name: name, shortCircuit: n.groups[name].ShortCircuit})
		}
		return out
	}
	return n.versionedSnaps[version]
}

// expectedAt 在给定的冻结版本快照上推演一次调用，严格复刻生产调度语义，
// 但不进入真实钩子函数（结论直接取自冻结的 cond）。
func expectedAt(snap []snapshotGroupLite) callObservation {
	exp := callObservation{decision: Approve}
	blockedGroup, errored := false, false
	for _, g := range snap {
		exp.snapGroups = append(exp.snapGroups, g.name)
		if blockedGroup || errored {
			exp.skipped += len(g.hooks)
			for _, h := range g.hooks {
				exp.snapIDs = append(exp.snapIDs, h.id)
			}
			continue
		}
		stopInGroup, rejectedInGroup := false, false
		for _, h := range g.hooks {
			exp.snapIDs = append(exp.snapIDs, h.id)
			if stopInGroup || errored {
				exp.skipped++
				continue
			}
			exp.ranIDs = append(exp.ranIDs, h.id)
			switch h.cond {
			case 'R':
				rejectedInGroup = true
				exp.decision = Reject
				if g.shortCircuit {
					stopInGroup = true
				}
			case 'E':
				errored = true
				exp.isError = true
				// 异常优先于拒绝：非短路组中即便已有钩子拒绝，只要后续钩子异常，
				// 整组即不可判定，最终结论不再是普通拒绝（decision 回到零值）。
				exp.decision = Approve
			}
		}
		if rejectedInGroup {
			blockedGroup = true
		}
	}
	return exp
}

// hookBehaviors 是生产实现与朴素实现共享的钩子语义表。
type hookBehaviors struct {
	mu    sync.Mutex
	cond  map[string]byte // 'A' approve, 'R' reject, 'E' error
	calls sync.Map        // hookID -> *atomic.Int64，真实执行次数
}

func newHookBehaviors() *hookBehaviors {
	return &hookBehaviors{cond: map[string]byte{}}
}

func (b *hookBehaviors) set(id string, c byte) {
	b.mu.Lock()
	b.cond[id] = c
	b.mu.Unlock()
}

func (b *hookBehaviors) get(id string) byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.cond[id]; ok {
		return c
	}
	return 'A'
}

func (b *hookBehaviors) makeHook(id, group string) Hook[target] {
	b.calls.LoadOrStore(id, &atomic.Int64{})
	return Hook[target]{
		ID: id, Group: group,
		Fn: func(context.Context, target) (Outcome, error) {
			if v, ok := b.calls.Load(id); ok {
				v.(*atomic.Int64).Add(1)
			}
			switch b.get(id) {
			case 'R':
				return Outcome{Decision: Reject, Reason: "R:" + id}, nil
			case 'E':
				return Outcome{}, fmt.Errorf("E:%s", id)
			default:
				return Outcome{Decision: Approve, Reason: "A:" + id}, nil
			}
		},
	}
}

// makeHookFixed 在创建时一次性绑定不可变行为，保证该钩子在整个存活期内
// 对所有并发调用结论一致——这是“某快照对应唯一串行结论”的前提。
func (b *hookBehaviors) makeHookFixed(id, group string, decision byte) Hook[target] {
	b.set(id, decision)
	return b.makeHook(id, group)
}

// callObservation 是一次校验调用可与朴素模型对照的全部观察。
type callObservation struct {
	snapIDs    []string
	snapGroups []string
	decision   Decision
	isError    bool
	ranIDs     []string
	skipped    int
	basis      string
}

func observeFromReport(rep *Report[target], err error) callObservation {
	obs := callObservation{
		decision: rep.FinalDecision,
		isError:  err != nil,
		skipped:  rep.SkippedHooks(),
		basis:    rep.Basis,
	}
	obs.snapIDs = rep.Snapshot.HookIDs()
	for _, g := range rep.Snapshot.Groups {
		obs.snapGroups = append(obs.snapGroups, g.Name)
	}
	for _, g := range rep.Groups {
		for _, rec := range g.Records {
			if rec.Status == StatusRunApproved || rec.Status == StatusRunRejected || rec.Status == StatusRunError {
				obs.ranIDs = append(obs.ranIDs, rec.ID)
			}
		}
	}
	return obs
}

func observationsEqual(a, b callObservation) bool {
	eqStrings := func(x, y []string) bool {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
		return true
	}
	return a.decision == b.decision &&
		a.isError == b.isError &&
		a.skipped == b.skipped &&
		eqStrings(a.snapIDs, b.snapIDs) &&
		eqStrings(a.snapGroups, b.snapGroups) &&
		eqStrings(a.ranIDs, b.ranIDs)
	// Basis 是判定依据的可读描述，其正确性由 decision/分组 outcome/ranIDs
	// 间接保证；线性化对照关注快照集合与结论，不逐字比较文案。
}

// TestConcurrentLinearizationAgainstNaive 交织注册/批次/注销/校验，
// 与独立朴素串行模型共享同一个操作全序，逐一核对每次并发调用观察到的
// 快照内容与最终结论都对应全序中的某个时刻。
func TestConcurrentLinearizationAgainstNaive(t *testing.T) {
	groupSpecs := []GroupSpec{
		{Name: "G-high", Priority: 100, ShortCircuit: true},
		{Name: "G-mid", Priority: 50, ShortCircuit: false},
		{Name: "G-low", Priority: 1, ShortCircuit: true},
	}
	groupNames := []string{"G-high", "G-mid", "G-low"}

	beh := newHookBehaviors()
	real, err := NewRegistry[target](groupSpecs...)
	if err != nil {
		t.Fatal(err)
	}
	naive := newNaiveModel(groupSpecs...)

	// linMu 定义唯一全序：真实变更 + 朴素重放在同一临界区完成；
	// 校验调用结束后进入该临界区读取该版本对应的朴素期望，做对照。
	var linMu sync.Mutex

	var obsMu sync.Mutex
	observations := map[uint64][]callObservation{}

	const workers = 8
	const opsPerWorker = 150
	var totalMutations atomic.Int64

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(worker)))
			targets := []target{{value: 1}, {value: 2}}
			for i := 0; i < opsPerWorker; i++ {
				id := fmt.Sprintf("w%di%03d", worker, i)
				group := groupNames[r.Intn(3)]
				decision := []byte{'A', 'A', 'A', 'R', 'E'}[r.Intn(5)]

				switch r.Intn(5) {
				case 0, 1, 2: // 注册
					h := beh.makeHookFixed(id, group, decision)
					linMu.Lock()
					realOK := real.Register(h) == nil
					naiveOK := naive.registerLocked(h, beh)
					linMu.Unlock()
					if realOK != naiveOK {
						t.Errorf("register divergence: %s real=%v naive=%v", id, realOK, naiveOK)
						return
					}
					if realOK {
						totalMutations.Add(1)
					}
				case 3: // 批次（刻意交错分组；批次内相对顺序必须保留）
					id2 := fmt.Sprintf("w%di%03db", worker, i)
					h1 := beh.makeHookFixed(id, group, decision)
					h2 := beh.makeHookFixed(id2, groupNames[r.Intn(3)],
						[]byte{'A', 'R', 'E'}[r.Intn(3)])
					linMu.Lock()
					realOK := real.RegisterBatch(Registration[target]{Hook: h1}, Registration[target]{Hook: h2}) == nil
					naiveOK := naive.registerBatchLocked([]Hook[target]{h1, h2}, beh)
					linMu.Unlock()
					if realOK != naiveOK {
						t.Errorf("batch divergence: real=%v naive=%v", realOK, naiveOK)
						return
					}
					if realOK {
						totalMutations.Add(1)
					}
				case 4: // 注销（多数 id 尚未注册，两种实现必须同样返回 false 且不涨版本）
					linMu.Lock()
					realOK := real.Unregister(id)
					naiveOK := naive.unregisterLocked(id, beh)
					linMu.Unlock()
					if realOK != naiveOK {
						t.Errorf("unregister divergence: %s real=%v naive=%v", id, realOK, naiveOK)
						return
					}
					if realOK {
						totalMutations.Add(1)
					}
				}

				// 高频发起并发校验；Validate 不持有 linMu，与变更真正并发。
				if r.Intn(2) == 0 {
					tg := targets[r.Intn(len(targets))]
					rep, callErr := real.Validate(context.Background(), tg)

					linMu.Lock()
					version := rep.Snapshot.Version
					if version > naive.opSeq {
						linMu.Unlock()
						t.Errorf("snapshot version %d beyond linearized frontier %d", version, naive.opSeq)
						return
					}
					expected := expectedAt(naive.snapshotAt(version))
					linMu.Unlock()

					got := observeFromReport(rep, callErr)
					if !observationsEqual(got, expected) {
						var dbg []string
						for _, gg := range rep.Groups {
							for _, rr := range gg.Records {
								dbg = append(dbg, gg.Name+"/"+rr.ID+"="+string(rr.Status))
							}
						}
						t.Errorf("observation mismatch at version %d:\nreal =%+v\nnaive=%+v\ndetail=%v",
							version, got, expected, dbg)
						return
					}
					obsMu.Lock()
					observations[version] = append(observations[version], got)
					obsMu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	// 终态一致性：版本号与存活钩子 ID 序列必须完全相同（含组内顺序）。
	finalReal := real.Snapshot()
	if finalReal.Version != naive.opSeq {
		t.Fatalf("final version real=%d naive=%d", finalReal.Version, naive.opSeq)
	}
	finalExp := expectedAt(naive.snapshotAt(naive.opSeq))
	if !equalStrings(finalReal.HookIDs(), finalExp.snapIDs) {
		t.Fatalf("final live hook sets differ:\nreal =%v\nnaive=%v",
			finalReal.HookIDs(), finalExp.snapIDs)
	}

	checked := 0
	coveredVersions := map[uint64]bool{}
	for version, list := range observations {
		if version > naive.opSeq {
			t.Fatalf("observed version %d exists at no point of the serial history", version)
		}
		coveredVersions[version] = true
		checked += len(list)
	}
	t.Logf("linearization: %d mutations, %d serial versions, %d concurrent validations across %d versions",
		totalMutations.Load(), naive.opSeq, checked, len(coveredVersions))
	if checked == 0 {
		t.Fatal("test generated no validation observations")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
