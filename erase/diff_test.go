package erase

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/refs"
)

// naiveModel 是全库朴素模拟：不使用任何索引，扫描全部记录求种子与不动点，
// 扫描全部边求跨边，作为与真实执行器对照的独立参考实现。
type naiveModel struct {
	owners     map[int64]map[string]bool
	anon       map[int64]bool
	policy     map[[2]int64]refs.Policy
	holds      map[int64]int64
	tombstones map[string]bool
	now        int64
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		owners:     map[int64]map[string]bool{},
		anon:       map[int64]bool{},
		policy:     map[[2]int64]refs.Policy{},
		holds:      map[int64]int64{},
		tombstones: map[string]bool{},
	}
}

func (m *naiveModel) held(id, now int64) bool {
	u, ok := m.holds[id]
	return ok && now < u
}

// naiveReport 是朴素模拟求出的擦除结果。
type naiveReport struct {
	deleted    []int64
	detached   []int64
	anonymized []int64
	unlinked   []Ref
}

func (m *naiveModel) plan(s string, now int64) (*naiveReport, *RestrictedError) {
	// (a) 全库扫描求种子与初始 D。
	inD := map[int64]bool{}
	var detached, anonymized []int64
	for id := range m.owners {
		if !m.owners[id][s] {
			continue
		}
		onlyS := len(m.owners[id]) == 1
		switch {
		case onlyS && !m.held(id, now):
			inD[id] = true
		case onlyS:
			anonymized = append(anonymized, id)
		default:
			detached = append(detached, id)
		}
	}
	// (b) 全库扫描求不动点：Cascade 引用 D、未保全、owners 为空、非匿名。
	for changed := true; changed; {
		changed = false
		for key, p := range m.policy {
			child, parent := key[0], key[1]
			if p != refs.Cascade || !inD[parent] || inD[child] {
				continue
			}
			if _, exists := m.owners[child]; !exists {
				continue
			}
			if m.anon[child] || len(m.owners[child]) != 0 || m.held(child, now) {
				continue
			}
			inD[child] = true
			changed = true
		}
	}
	// 跨边：全库扫描，Restrict 取 (最小 parent, 最小 child)，其余断开。
	var dSorted []int64
	for id := range inD {
		dSorted = append(dSorted, id)
	}
	sort.Slice(dSorted, func(i, j int) bool { return dSorted[i] < dSorted[j] })
	var unlinked []Ref
	for _, parent := range dSorted {
		var cross []int64
		var restrictChildren []int64
		for key, p := range m.policy {
			if key[1] != parent || inD[key[0]] {
				continue
			}
			cross = append(cross, key[0])
			if p == refs.Restrict {
				restrictChildren = append(restrictChildren, key[0])
			}
		}
		if len(restrictChildren) > 0 {
			sort.Slice(restrictChildren, func(i, j int) bool {
				return restrictChildren[i] < restrictChildren[j]
			})
			return nil, &RestrictedError{Parent: parent, Child: restrictChildren[0]}
		}
		sort.Slice(cross, func(i, j int) bool { return cross[i] < cross[j] })
		for _, child := range cross {
			unlinked = append(unlinked, Ref{Child: child, Parent: parent})
		}
	}
	// 报告口径：(child,parent) 升序（Restrict 仍按 parent 优先取最小对）。
	sort.Slice(unlinked, func(i, j int) bool {
		if unlinked[i].Child != unlinked[j].Child {
			return unlinked[i].Child < unlinked[j].Child
		}
		return unlinked[i].Parent < unlinked[j].Parent
	})
	sort.Slice(detached, func(i, j int) bool { return detached[i] < detached[j] })
	sort.Slice(anonymized, func(i, j int) bool { return anonymized[i] < anonymized[j] })
	return &naiveReport{
		deleted:    dSorted,
		detached:   detached,
		anonymized: anonymized,
		unlinked:   unlinked,
	}, nil
}

// commit 让朴素模型应用同一报告，供多步序列里状态同步。
func (m *naiveModel) commit(s string, rep *naiveReport, now int64) {
	for _, ref := range rep.unlinked {
		delete(m.policy, [2]int64{ref.Child, ref.Parent})
	}
	for _, id := range rep.detached {
		delete(m.owners[id], s)
	}
	for _, id := range rep.anonymized {
		m.owners[id] = map[string]bool{}
		m.anon[id] = true
	}
	for _, id := range rep.deleted {
		delete(m.owners, id)
		delete(m.anon, id)
		delete(m.holds, id)
	}
	// 删除与 D 顶点关联的全部边。
	for key := range m.policy {
		if inSlice(rep.deleted, key[0]) || inSlice(rep.deleted, key[1]) {
			delete(m.policy, key)
		}
	}
	m.tombstones[s] = true
	m.now = now
}

func inSlice(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func intsEqual(a, b []int64) bool {
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

func refsEqual(a []Ref, b []Ref) bool {
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

// reflectReportEqual 比较真实报告与朴素模拟报告（nil 与空切片等价）。
func reflectReportEqual(got *Report, want *naiveReport) bool {
	return intsEqual(got.Deleted, want.deleted) &&
		intsEqual(got.Detached, want.detached) &&
		intsEqual(got.Anonymized, want.anonymized) &&
		refsEqual(got.Unlinked, want.unlinked)
}

// op 是随机序列中的一步。
type op struct {
	kind   string
	id     int64
	id2    int64
	pol    refs.Policy
	until  int64
	now    int64
	owners []string
	subj   string
}

// genWorld 生成一张随机引用图并同时填充真实执行器与朴素模型，
// 返回可复现的操作序列（含 Put/AddRef/Hold/Erase/Plan/RemoveRef）。
func genWorld(rng *rand.Rand, ex *Executor, m *naiveModel) []op {
	const n = 14
	subjects := []string{"a", "b", "c"}
	now := int64(10)
	var seq []op

	ownerSet := map[int64][]string{}
	for id := int64(1); id <= n; id++ {
		k := rng.Intn(4) // 0:无主 1..3:k 个主体
		picked := map[string]bool{}
		var owners []string
		for j := 0; j < k; j++ {
			s := subjects[rng.Intn(len(subjects))]
			if picked[s] {
				continue
			}
			picked[s] = true
			owners = append(owners, s)
		}
		ownerSet[id] = owners
		if err := ex.Put(id, owners, now); err != nil {
			panic(fmt.Sprintf("put %d: %v", id, err))
		}
		m.owners[id] = map[string]bool{}
		for _, s := range owners {
			m.owners[id][s] = true
		}
		seq = append(seq, op{kind: "put", id: id, now: now, owners: append([]string(nil), owners...)})
	}

	outdeg := map[int64]int{}
	for id := int64(1); id <= n; id++ {
		for other := int64(1); other <= n; other++ {
			if other == id || outdeg[id] >= 8 {
				continue
			}
			if rng.Float64() > 0.22 {
				continue
			}
			if _, exists := ex.graph.Has(id, other); exists {
				continue
			}
			pol := []refs.Policy{refs.Cascade, refs.Cascade, refs.SetNull, refs.Restrict}[rng.Intn(4)]
			if err := ex.AddRef(id, other, pol, now); err != nil {
				panic(fmt.Sprintf("addref %d->%d: %v", id, other, err))
			}
			m.policy[[2]int64{id, other}] = pol
			outdeg[id]++
			seq = append(seq, op{kind: "addref", id: id, id2: other, pol: pol, now: now})
		}
	}

	// 随机给部分记录加保全（now 前后 1 与相等边界都覆盖）。
	for _, id := range randPickIDs(rng, n, 0.35) {
		delta := []int64{-1, 0, 1, 50}[rng.Intn(4)]
		until := now + delta
		if until <= now {
			continue
		}
		if err := ex.Hold(id, until, now); err != nil {
			panic(fmt.Sprintf("hold %d: %v", id, err))
		}
		m.holds[id] = until
		seq = append(seq, op{kind: "hold", id: id, until: until, now: now})
	}
	return seq
}

func randPickIDs(rng *rand.Rand, n int, p float64) []int64 {
	var ids []int64
	for id := int64(1); id <= int64(n); id++ {
		if rng.Float64() < p {
			ids = append(ids, id)
		}
	}
	return ids
}

// stateDigest 直接读取真实执行器内部结构，产出与朴素模型可比较的状态文本。
func stateDigest(ex *Executor) string {
	type recLine struct {
		id     int64
		owners []string
		anon   bool
	}
	var recs []recLine
	for _, id := range ex.store.IDs() {
		r, _ := ex.store.Get(id)
		owners := append([]string(nil), r.Owners...)
		sort.Strings(owners)
		recs = append(recs, recLine{id: id, owners: owners, anon: r.Anon})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].id < recs[j].id })
	var b []byte
	for _, r := range recs {
		b = append(b, fmt.Sprintf("R%d:%v:a%v\n", r.id, r.owners, r.anon)...)
	}
	for _, e := range ex.graph.All() {
		b = append(b, fmt.Sprintf("E%d>%d=%d\n", e.Child, e.Parent, e.Policy)...)
	}
	var tombs []string
	for s := range ex.tombstones {
		tombs = append(tombs, s)
	}
	sort.Strings(tombs)
	b = append(b, fmt.Sprintf("T%v\n", tombs)...)
	return string(b)
}

func naiveDigest(m *naiveModel) string {
	var ids []int64
	for id := range m.owners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b []byte
	for _, id := range ids {
		var owners []string
		for s := range m.owners[id] {
			owners = append(owners, s)
		}
		sort.Strings(owners)
		b = append(b, fmt.Sprintf("R%d:%v:a%v\n", id, owners, m.anon[id])...)
	}
	type edge struct{ c, p int64 }
	var edges []edge
	for k := range m.policy {
		edges = append(edges, edge{k[0], k[1]})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].c != edges[j].c {
			return edges[i].c < edges[j].c
		}
		return edges[i].p < edges[j].p
	})
	for _, e := range edges {
		b = append(b, fmt.Sprintf("E%d>%d=%d\n", e.c, e.p, m.policy[[2]int64{e.c, e.p}])...)
	}
	var tombs []string
	for s := range m.tombstones {
		tombs = append(tombs, s)
	}
	sort.Strings(tombs)
	b = append(b, fmt.Sprintf("T%v\n", tombs)...)
	return string(b)
}

func reportsMatch(got *Report, want *naiveReport) bool {
	return reflectReportEqual(got, want)
}

func TestDifferentialRandom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1500 differential cases in -short mode")
	}
	policyName := map[refs.Policy]string{
		refs.Cascade: "Cascade", refs.SetNull: "SetNull", refs.Restrict: "Restrict",
	}
	for iter := 0; iter < 1500; iter++ {
		rng := rand.New(rand.NewSource(int64(100000 + iter)))
		ex := New()
		m := newNaiveModel()
		seq := genWorld(rng, ex, m)

		// 在同一世界上依次擦除若干主体，逐步比对。
		eraseNow := int64(20)
		for _, s := range []string{"a", "b", "c"} {
			now := eraseNow + rng.Int63n(30)
			eraseNow = now

			// Plan 只读且与 Erase 同源。
			planRep, planErr := ex.Plan(s, now)
			nr, nrest := m.plan(s, now)
			if nrest != nil {
				if !errors.Is(planErr, ErrRestricted) {
					t.Fatalf("iter %d plan %s: err=%v want restricted", iter, s, planErr)
				}
				var re *RestrictedError
				errors.As(planErr, &re)
				if re.Parent != nrest.Parent || re.Child != nrest.Child {
					t.Fatalf("iter %d plan %s: restricted (%d,%d) want (%d,%d)",
						iter, s, re.Parent, re.Child, nrest.Parent, nrest.Child)
				}
			} else {
				if planErr != nil {
					t.Fatalf("iter %d plan %s: unexpected err %v", iter, s, planErr)
				}
				if !reportsMatch(planRep, nr) {
					t.Fatalf("iter %d plan %s mismatch:\n got deleted=%v detached=%v anon=%v unlinked=%v\nwant deleted=%v detached=%v anon=%v unlinked=%v",
						iter, s,
						planRep.Deleted, planRep.Detached, planRep.Anonymized, planRep.Unlinked,
						nr.deleted, nr.detached, nr.anonymized, nr.unlinked)
				}
			}

			// Plan 不得改状态。
			if stateDigest(ex) != naiveDigest(m) {
				t.Fatalf("iter %d plan changed state:\n%s\nvs\n%s",
					iter, stateDigest(ex), naiveDigest(m))
			}

			rep, err := ex.Erase(s, now)
			if nrest != nil {
				if !errors.Is(err, ErrRestricted) {
					t.Fatalf("iter %d erase %s: err=%v want restricted", iter, s, err)
				}
				var re *RestrictedError
				errors.As(err, &re)
				if re.Parent != nrest.Parent || re.Child != nrest.Child {
					t.Fatalf("iter %d erase %s: restricted (%d,%d) want (%d,%d)",
						iter, s, re.Parent, re.Child, nrest.Parent, nrest.Child)
				}
				t.Logf("iter %d Erase(%s,t=%d) BLOCKED reason=Restrict parent=%d child=%d; state unchanged",
					iter, s, now, nrest.Parent, nrest.Child)
				seq = append(seq, op{kind: "erase-blocked", subj: s, now: now})
				continue
			}
			if err != nil {
				if errors.Is(err, ErrAlreadyErased) && m.tombstones[s] {
					t.Logf("iter %d Erase(%s) already erased; consistent", iter, s)
					continue
				}
				t.Fatalf("iter %d erase %s: unexpected err %v", iter, s, err)
			}
			if !reportsMatch(rep, nr) {
				t.Fatalf("iter %d erase %s mismatch:\n got deleted=%v detached=%v anon=%v unlinked=%v\nwant deleted=%v detached=%v anon=%v unlinked=%v",
					iter, s,
					rep.Deleted, rep.Detached, rep.Anonymized, rep.Unlinked,
					nr.deleted, nr.detached, nr.anonymized, nr.unlinked)
			}
			m.commit(s, nr, now)
			if stateDigest(ex) != naiveDigest(m) {
				t.Fatalf("iter %d post-erase state mismatch:\n%s\nwant\n%s",
					iter, stateDigest(ex), naiveDigest(m))
			}
			t.Logf("iter %d Erase(%s,t=%d) OK deleted=%v detached=%v anonymized=%v unlinked=%v visited=%d",
				iter, s, now, rep.Deleted, rep.Detached, rep.Anonymized, rep.Unlinked, ex.Visited())
			seq = append(seq, op{kind: "erase", subj: s, now: now})
		}

		// visited 上界（针对成功擦除）由公式独立计算。
		// 输入与判定依据打印：
		if iter < 3 || testing.Verbose() {
			for _, o := range seq {
				switch o.kind {
				case "put":
					t.Logf("iter %d input Put(%d,%v,%d)", iter, o.id, o.owners, o.now)
				case "addref":
					t.Logf("iter %d input AddRef(%d,%d,%s,%d)", iter, o.id, o.id2, policyName[o.pol], o.now)
				case "hold":
					t.Logf("iter %d input Hold(%d,until=%d,%d)", iter, o.id, o.until, o.now)
				}
			}
		}
	}
}
