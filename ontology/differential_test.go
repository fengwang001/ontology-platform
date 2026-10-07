package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveLink / naiveModel 是与生产实现独立的朴素参考模型：
// 只维护一个全量链接集合，每次基数校验都遍历全量集合重新统计计数，
// 刻意不维护任何增量账本。若生产实现与它在同一随机序列上逐条同结果，
// 则证明增量账本与「重新全量计数」等价。
type naiveLink struct {
	typ            LinkTypeName
	source, target InstanceID
}

type naiveModel struct {
	spec         LinkTypeSpec
	links        map[naiveLink]struct{}
	sourceExists map[InstanceID]struct{}
	targetExists map[InstanceID]struct{}
}

func newNaive(spec LinkTypeSpec, sources, targets []InstanceID) *naiveModel {
	m := &naiveModel{
		spec:         spec,
		links:        map[naiveLink]struct{}{},
		sourceExists: map[InstanceID]struct{}{},
		targetExists: map[InstanceID]struct{}{},
	}
	for _, id := range sources {
		m.sourceExists[id] = struct{}{}
	}
	for _, id := range targets {
		m.targetExists[id] = struct{}{}
	}
	return m
}

func (m *naiveModel) count(side endpointSide, id InstanceID) int {
	n := 0
	for lk := range m.links {
		if side == sideSource && lk.source == id {
			n++
		}
		if side == sideTarget && lk.target == id {
			n++
		}
	}
	return n
}

func (m *naiveModel) validate(source, target InstanceID, seen map[InstancePair]int) *LinkError {
	if _, ok := m.sourceExists[source]; !ok {
		return invalidArgument("naive source missing")
	}
	if _, ok := m.targetExists[target]; !ok {
		return invalidArgument("naive target missing")
	}
	if _, ok := seen[InstancePair{source, target}]; ok {
		return invalidArgument("duplicate within batch")
	}
	if _, ok := m.links[naiveLink{m.spec.Name, source, target}]; ok {
		return invalidArgument("already linked")
	}
	if max, ok := m.spec.SourceBound.limit(); ok && m.count(sideSource, source) >= max {
		return sourceCapExceeded("naive source full")
	}
	if max, ok := m.spec.TargetBound.limit(); ok && m.count(sideTarget, target) >= max {
		return targetCapExceeded("naive target full")
	}
	return nil
}

type naiveOutcome struct {
	err       Reason
	committed bool
	statuses  []ItemStatus
	reasons   []Reason
}

func (m *naiveModel) create(source, target InstanceID) naiveOutcome {
	if err := m.validate(source, target, map[InstancePair]int{}); err != nil {
		return naiveOutcome{err: err.Code}
	}
	m.links[naiveLink{m.spec.Name, source, target}] = struct{}{}
	return naiveOutcome{committed: true}
}

func (m *naiveModel) del(source, target InstanceID) naiveOutcome {
	lk := naiveLink{m.spec.Name, source, target}
	if _, ok := m.links[lk]; !ok {
		return naiveOutcome{err: ReasonLinkNotFound}
	}
	delete(m.links, lk)
	return naiveOutcome{committed: true}
}

func (m *naiveModel) batch(pairs []InstancePair, mode ImportMode) naiveOutcome {
	statuses := make([]ItemStatus, len(pairs))
	reasons := make([]Reason, len(pairs))
	accepted := []naiveLink{}
	seen := map[InstancePair]int{}

	if mode == ModeBestEffort {
		for i, p := range pairs {
			if err := m.validate(p.Source, p.Target, seen); err != nil {
				statuses[i], reasons[i] = StatusRejected, err.Code
				continue
			}
			m.links[naiveLink{m.spec.Name, p.Source, p.Target}] = struct{}{}
			seen[p] = i
			statuses[i] = StatusAccepted
		}
		return naiveOutcome{committed: true, statuses: statuses, reasons: reasons}
	}

	failed := -1
	for i, p := range pairs {
		if err := m.validate(p.Source, p.Target, seen); err != nil {
			statuses[i], reasons[i] = StatusRejected, err.Code
			failed = i
			break
		}
		m.links[naiveLink{m.spec.Name, p.Source, p.Target}] = struct{}{}
		seen[p] = i
		accepted = append(accepted, naiveLink{m.spec.Name, p.Source, p.Target})
		statuses[i] = StatusAccepted
	}
	if failed >= 0 {
		for _, lk := range accepted {
			delete(m.links, lk)
		}
		for j := 0; j < len(accepted); j++ {
			statuses[j] = StatusRolledBack
		}
		for j := failed + 1; j < len(pairs); j++ {
			statuses[j] = StatusSkipped
		}
		return naiveOutcome{committed: false, statuses: statuses, reasons: reasons}
	}
	return naiveOutcome{committed: true, statuses: statuses, reasons: reasons}
}

type op struct {
	kind  int
	s, d  InstanceID
	pairs []InstancePair
	mode  ImportMode
}

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	const seeds = 25
	const opsPerSeed = 80
	persons := []InstanceID{"p1", "p2", "p3", "p4", "p5"}
	articles := []InstanceID{"a1", "a2", "a3", "a4", "a5"}
	ghostS, ghostT := InstanceID("ghost"), InstanceID("ax")

	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		spec := LinkTypeSpec{
			Name:        "authored",
			SourceType:  "Person",
			TargetType:  "Article",
			SourceBound: AtMost(2),
			TargetBound: AtMostOne(),
		}
		env := setupTyped(t, spec, persons, articles)
		naive := newNaive(spec, persons, articles)

		t.Logf("==== seed=%d start ====", seed)
		for step := 0; step < opsPerSeed; step++ {
			o := genOp(rng, persons, articles, ghostS, ghostT)

			var actualErrs []Reason
			var actualStatuses []ItemStatus
			committed := true
			switch o.kind {
			case 0:
				err := env.ledger.CreateLink("authored", o.s, o.d)
				committed = err == nil
				if err != nil {
					actualErrs = []Reason{reasonOf(err)}
				}
			case 1:
				err := env.ledger.DeleteLink("authored", o.s, o.d)
				committed = err == nil
				if err != nil {
					actualErrs = []Reason{reasonOf(err)}
				}
			default:
				rep := env.imp.Import("authored", o.pairs, o.mode)
				committed = rep.Committed
				for _, r := range rep.Results {
					actualStatuses = append(actualStatuses, r.Status)
					if r.Err != nil {
						actualErrs = append(actualErrs, r.Err.Code)
					}
				}
			}

			var exp naiveOutcome
			switch o.kind {
			case 0:
				exp = naive.create(o.s, o.d)
			case 1:
				exp = naive.del(o.s, o.d)
			default:
				exp = naive.batch(o.pairs, o.mode)
			}

			t.Logf("seed=%d step=%d input=%s | actual committed=%v statuses=%v errs=%v | basis committed=%v statuses=%v reasons=%v",
				seed, step, describeOp(o),
				committed, actualStatuses, actualErrs,
				exp.committed, exp.statuses, nonEmptyReasons(exp.reasons))

			if o.kind < 2 {
				var expErr Reason
				if !exp.committed {
					expErr = exp.err
				}
				if committed != exp.committed || firstReason(actualErrs) != expErr {
					t.Fatalf("seed=%d step=%d %s single mismatch: actual(%v,%v) naive(%v,%v)",
						seed, step, describeOp(o), committed, firstReason(actualErrs), exp.committed, expErr)
				}
			} else {
				if committed != exp.committed || !statusesEqual(actualStatuses, exp.statuses) ||
					!reasonsAtRejectsMatch(actualStatuses, actualErrs, exp.statuses, exp.reasons) {
					t.Fatalf("seed=%d step=%d %s batch mismatch: actual(%v,%v,%v) naive(%v,%v,%v)",
						seed, step, describeOp(o),
						committed, actualStatuses, actualErrs,
						exp.committed, exp.statuses, nonEmptyReasons(exp.reasons))
				}
			}

			if !linksEqualNaive(env.ledger, naive) {
				t.Fatalf("seed=%d step=%d link set diverged", seed, step)
			}
			if !countsMatchNaive(env.ledger, spec.Name) {
				t.Fatalf("seed=%d step=%d ledger counts diverged from naive recount", seed, step)
			}
		}
		t.Logf("==== seed=%d done: %d steps, final links=%d ====", seed, opsPerSeed, env.ledger.LinkCount())
	}
}

func setupTyped(t *testing.T, spec LinkTypeSpec, persons, articles []InstanceID) *testEnv {
	t.Helper()
	l := NewLedger()
	must(t, l.RegisterLinkType(spec))
	env := &testEnv{ledger: l, imp: NewImporter(l)}
	for _, id := range persons {
		must(t, l.CreateObject(spec.SourceType, id))
		env.persons = append(env.persons, id)
	}
	for _, id := range articles {
		must(t, l.CreateObject(spec.TargetType, id))
		env.articles = append(env.articles, id)
	}
	return env
}

func genOp(rng *rand.Rand, persons, articles []InstanceID, ghostS, ghostT InstanceID) op {
	pick := func(ids []InstanceID) InstanceID { return ids[rng.Intn(len(ids))] }
	maybeGhost := func(real []InstanceID, ghost InstanceID) InstanceID {
		if rng.Intn(8) == 0 {
			return ghost
		}
		return pick(real)
	}
	switch k := rng.Intn(4); k {
	case 0:
		return op{kind: 0, s: maybeGhost(persons, ghostS), d: maybeGhost(articles, ghostT)}
	case 1:
		return op{kind: 1, s: maybeGhost(persons, ghostS), d: maybeGhost(articles, ghostT)}
	default:
		n := 1 + rng.Intn(4)
		pairs := make([]InstancePair, n)
		for i := range pairs {
			pairs[i] = InstancePair{
				Source: maybeGhost(persons, ghostS),
				Target: maybeGhost(articles, ghostT),
			}
		}
		if n > 1 && rng.Intn(4) == 0 {
			pairs[n-1] = pairs[0]
		}
		mode := ModeAllOrNothing
		if k == 3 {
			mode = ModeBestEffort
		}
		return op{kind: k, pairs: pairs, mode: mode}
	}
}

func describeOp(o op) string {
	switch o.kind {
	case 0:
		return fmt.Sprintf("create(%s->%s)", o.s, o.d)
	case 1:
		return fmt.Sprintf("delete(%s->%s)", o.s, o.d)
	case 2:
		return fmt.Sprintf("import[all-or-nothing]%v", o.pairs)
	default:
		return fmt.Sprintf("import[best-effort]%v", o.pairs)
	}
}

func firstReason(rs []Reason) Reason {
	if len(rs) == 0 {
		return ""
	}
	return rs[0]
}

func nonEmptyReasons(rs []Reason) []Reason {
	out := []Reason{}
	for _, r := range rs {
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func statusesEqual(a, b []ItemStatus) bool {
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

// reasonsAtRejectsMatch 比较所有「被拒位置」上的错误类别是否逐位一致。
func reasonsAtRejectsMatch(actualStatus []ItemStatus, actualErrs []Reason,
	expStatus []ItemStatus, expErrs []Reason) bool {
	ai := 0
	for i, st := range actualStatus {
		if st != StatusRejected {
			continue
		}
		if ai >= len(actualErrs) || i >= len(expStatus) || expStatus[i] != StatusRejected {
			return false
		}
		if actualErrs[ai] != expErrs[i] {
			return false
		}
		ai++
	}
	return ai == len(actualErrs)
}

func linksEqualNaive(l *Ledger, m *naiveModel) bool {
	snap := l.Snapshot()
	if len(snap) != len(m.links) {
		return false
	}
	for lk := range m.links {
		if _, ok := snap[LinkKey{Type: lk.typ, Source: lk.source, Target: lk.target}]; !ok {
			return false
		}
	}
	return true
}

// countsMatchNaive 对每一个 (端,实例) 用朴素全量遍历的计数校准增量账本。
func countsMatchNaive(l *Ledger, typ LinkTypeName) bool {
	snap := l.Snapshot()
	recount := map[countKey]int{}
	for lk := range snap {
		recount[countKey{typ, sideSource, lk.Source}]++
		recount[countKey{typ, sideTarget, lk.Target}]++
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, v := range recount {
		if l.counts[k] != v {
			return false
		}
	}
	for k, v := range l.counts {
		if recount[k] != v {
			return false
		}
	}
	return true
}
