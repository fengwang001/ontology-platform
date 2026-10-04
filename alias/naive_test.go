package alias_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
	"ontology/resolve"
)

// naiveState 是按题目规则逐字实现的逐步朴素模拟器，独立于生产代码的数据结构，
// 用于在 1500 组随机操作序列上对照结果、终态与纪元。
type naiveState struct {
	indices map[string]indexreg.State
	members map[string]map[string]naiveMember
	write   map[string]string
	epoch   uint64
}

type naiveMember struct {
	isWrite alias.TriState
	filter  string
}

func newNaive() *naiveState {
	return &naiveState{
		indices: map[string]indexreg.State{},
		members: map[string]map[string]naiveMember{},
		write:   map[string]string{},
	}
}

func (s *naiveState) createIndex(name string) error {
	if !indexreg.ValidName(name) {
		return indexreg.ErrInvalidName
	}
	if _, ok := s.indices[name]; ok {
		return indexreg.ErrNameConflict
	}
	s.indices[name] = indexreg.Open
	s.epoch++
	return nil
}

func (s *naiveState) closeIndex(name string) error {
	st, ok := s.indices[name]
	if !ok {
		return indexreg.ErrIndexNotFound
	}
	if st == indexreg.Closed {
		return nil
	}
	s.indices[name] = indexreg.Closed
	s.epoch++
	return nil
}

func (s *naiveState) openIndex(name string) error {
	st, ok := s.indices[name]
	if !ok {
		return indexreg.ErrIndexNotFound
	}
	if st == indexreg.Open {
		return nil
	}
	s.indices[name] = indexreg.Open
	s.epoch++
	return nil
}

// errKind 用于与生产结果按“错误类别 + 下标”比较，忽略包装文案。
type errKind int

const (
	kOK errKind = iota
	kInvalidArgument
	kIndexNotFound
	kMemberNotFound
	kNameConflict
	kMultipleWrite
)

func classify(err error) errKind {
	switch {
	case err == nil:
		return kOK
	case errors.Is(err, alias.ErrInvalidArgument):
		return kInvalidArgument
	case errors.Is(err, alias.ErrMemberNotFound):
		return kMemberNotFound
	case errors.Is(err, alias.ErrMultipleWrite):
		return kMultipleWrite
	case errors.Is(err, indexreg.ErrNameConflict):
		return kNameConflict
	case errors.Is(err, indexreg.ErrIndexNotFound):
		return kIndexNotFound
	default:
		return -1
	}
}

// simulate 严格按四级拒绝次序推演一批动作，返回（错误类别，出错下标，是否被接受）。
func (s *naiveState) simulate(acts []alias.Action) (errKind, int, bool) {
	if len(acts) < 1 || len(acts) > 100 {
		return kInvalidArgument, -1, false
	}
	for i, a := range acts {
		valid := indexreg.ValidName(a.Index)
		switch alias.ActionKind(a) {
		case 0, 1:
			valid = valid && indexreg.ValidName(alias.ActionAliasName(a))
			if alias.ActionKind(a) == 0 && a.IsWrite > alias.WriteFalse {
				valid = false
			}
		}
		if !valid {
			return kInvalidArgument, i, false
		}
	}

	idx := map[string]indexreg.State{}
	for n, st := range s.indices {
		idx[n] = st
	}
	mem := map[string]map[string]naiveMember{}
	for an, g := range s.members {
		ng := map[string]naiveMember{}
		for ix, mm := range g {
			ng[ix] = mm
		}
		mem[an] = ng
	}

	for i, a := range acts {
		switch alias.ActionKind(a) {
		case 0: // Add
			if _, ok := idx[a.Index]; !ok {
				return kIndexNotFound, i, false
			}
			g := mem[alias.ActionAliasName(a)]
			if g == nil {
				g = map[string]naiveMember{}
				mem[alias.ActionAliasName(a)] = g
			}
			g[a.Index] = naiveMember{isWrite: a.IsWrite, filter: a.Filter}
		case 1: // Remove
			g := mem[alias.ActionAliasName(a)]
			if _, ok := g[a.Index]; !ok {
				if alias.ActionMustExist(a) {
					return kMemberNotFound, i, false
				}
				continue
			}
			delete(g, a.Index)
			if len(g) == 0 {
				delete(mem, alias.ActionAliasName(a))
			}
		case 2: // RemoveIndex
			if _, ok := idx[a.Index]; !ok {
				return kIndexNotFound, i, false
			}
			delete(idx, a.Index)
			for an, g := range mem {
				delete(g, a.Index)
				if len(g) == 0 {
					delete(mem, an)
				}
			}
		}
	}

	conflicts := []string{}
	for an := range mem {
		if _, ok := idx[an]; ok {
			conflicts = append(conflicts, an)
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return kNameConflict, -1, false
	}

	bad := []string{}
	for an, g := range mem {
		trues := 0
		for _, mm := range g {
			if mm.isWrite == alias.WriteTrue {
				trues++
			}
		}
		if trues > 1 {
			bad = append(bad, an)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return kMultipleWrite, -1, false
	}

	// 接受：比较终态是否变化以决定纪元。
	changed := !mapsDeepEqualState(idx, s.indices) || !mapsDeepEqualMember(mem, s.members)
	s.indices = idx
	s.members = mem
	s.recomputeWrite()
	if changed {
		s.epoch++
	}
	return kOK, -1, true
}

func (s *naiveState) recomputeWrite() {
	s.write = map[string]string{}
	for an, g := range s.members {
		var trueOne, sole string
		trueN, unspecN := 0, 0
		for ix, mm := range g {
			if mm.isWrite == alias.WriteTrue {
				trueN++
				trueOne = ix
			}
			if mm.isWrite == alias.WriteUnspecified {
				unspecN++
			}
			sole = ix
		}
		switch {
		case trueN == 1:
			s.write[an] = trueOne
		case len(g) == 1 && unspecN == 1:
			s.write[an] = sole
		}
	}
}

func mapsDeepEqualState(a, b map[string]indexreg.State) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mapsDeepEqualMember(a, b map[string]map[string]naiveMember) bool {
	if len(a) != len(b) {
		return false
	}
	for k, ga := range a {
		gb, ok := b[k]
		if !ok || len(ga) != len(gb) {
			return false
		}
		for ix, ma := range ga {
			if ma != gb[ix] {
				return false
			}
		}
	}
	return true
}

func TestAgainstNaiveSimulation1500(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	const sequences = 1500
	logCount := 0

	for seq := 0; seq < sequences; seq++ {
		prod := alias.NewManager(indexreg.New())
		naive := newNaive()

		// 初始索引池，两个实现保持一致。
		pool := []string{"i0", "i1", "i2", "i3", "i4", "i5", "i6", "i7", "i8", "i9"}
		for _, n := range pool {
			if err := prod.CreateIndex(n); err != nil {
				t.Fatalf("seed prod %q: %v", n, err)
			}
			if err := naive.createIndex(n); err != nil {
				t.Fatalf("seed naive %q: %v", n, err)
			}
		}

		steps := 1 + rng.Intn(12)
		for step := 0; step < steps; step++ {
			acts, desc := genBatch(rng, pool)

			prodErr := prod.Update(acts)
			nKind, nIdx, _ := naive.simulate(acts)

			pKind := classify(prodErr)
			pIdx := -1
			var ae *alias.ActionError
			if errors.As(prodErr, &ae) {
				pIdx = ae.Index
			}

			if pKind != nKind || (pKind != kOK && pKind != kInvalidArgument && pIdx != nIdx && nIdx >= 0) {
				t.Fatalf("seq=%d step=%d 输入=%s\n生产: kind=%d idx=%d err=%v\n朴素: kind=%d idx=%d",
					seq, step, desc, pKind, pIdx, prodErr, nKind, nIdx)
			}

			// 被拒后纪元必须不变；接受且终态变化的两侧纪元都 +1，空效果不 +1。
			if prod.Epoch() != naive.epoch {
				t.Fatalf("seq=%d step=%d 输入=%s 生产 epoch=%d 朴素 epoch=%d",
					seq, step, desc, prod.Epoch(), naive.epoch)
			}

			// 终态对照：索引开闭、别名成员、写索引推定、读写解析。
			assertStatesAgree(t, prod, naive, pool, seq, step, desc)

			if logCount < 12 {
				verdict := "接受"
				if pKind != kOK {
					verdict = fmt.Sprintf("拒绝(kind=%d,idx=%d)", pKind, pIdx)
				}
				t.Logf("seq=%d step=%d 输入=%s => %s epoch=%d", seq, step, desc, verdict, prod.Epoch())
				logCount++
			}
		}
	}
	t.Logf("共对照 %d 组随机操作序列，每组多步；判定依据=错误类别/最小下标/纪元/终态/读写解析全量一致", sequences)
}

func assertStatesAgree(t *testing.T, prod *alias.Manager, naive *naiveState, pool []string, seq, step int, desc string) {
	t.Helper()

	// 索引开闭状态一致。
	for _, n := range pool {
		ps, pok := prod.IndexState(n)
		ns, nok := naive.indices[n]
		if pok != nok || ps != ns {
			t.Fatalf("seq=%d step=%d index %q prod=(%v,%v) naive=(%v,%v)", seq, step, n, ps, pok, ns, nok)
		}
	}

	// 别名成员与写索引一致。
	aliasNames := map[string]bool{}
	for an := range naive.members {
		aliasNames[an] = true
	}
	for _, an := range prod.Aliases() {
		aliasNames[an] = true
	}
	if len(prod.Aliases()) != len(naive.members) {
		t.Fatalf("seq=%d step=%d alias count prod=%d naive=%d", seq, step, len(prod.Aliases()), len(naive.members))
	}
	r := resolve.New()
	for an := range aliasNames {
		pm := prod.Members(an)
		ng := naive.members[an]
		if len(pm) != len(ng) {
			t.Fatalf("seq=%d step=%d alias %q member count prod=%d naive=%d", seq, step, an, len(pm), len(ng))
		}
		for _, mm := range pm {
			nm, ok := ng[mm.Index]
			if !ok || nm.isWrite != mm.IsWrite || nm.filter != mm.Filter {
				t.Fatalf("seq=%d step=%d member (%s,%s) prod=(%v,%q) naive=(%v,%q)",
					seq, step, an, mm.Index, mm.IsWrite, mm.Filter, nm.isWrite, nm.filter)
			}
		}
		pw, pOK := prod.WriteIndex(an)
		nw, nOK := naive.write[an]
		if pOK != nOK || pw != nw {
			t.Fatalf("seq=%d step=%d write(%s) prod=(%q,%v) naive=(%q,%v)", seq, step, an, pw, pOK, nw, nOK)
		}

		// 读写解析与朴素规则对照（对每个别名名与索引名抽样）。
		v := prod.SnapshotView()
		// 读：未关闭成员按索引名排序。
		pr, perr := r.Read(v, an)
		var wantRead []string
		for ix, mm := range ng {
			if naive.indices[ix] == indexreg.Open {
				wantRead = append(wantRead, ix)
			}
			_ = mm
		}
		sort.Strings(wantRead)
		if perr != nil {
			t.Fatalf("seq=%d step=%d Read(%s) err=%v", seq, step, an, perr)
		}
		if got := readNames(pr); fmt.Sprint(got) != fmt.Sprint(wantRead) {
			t.Fatalf("seq=%d step=%d Read(%s) prod=%v naive=%v", seq, step, an, got, wantRead)
		}
		// 写：无写索引或已关闭 -> 对应错误，不回落。
		tracer := resolve.NewTracer()
		_, pwerr := r.Write(v, an, tracer)
		switch {
		case !nOK:
			if !errors.Is(pwerr, alias.ErrNoWriteIndex) {
				t.Fatalf("seq=%d step=%d Write(%s) want no-write got %v", seq, step, an, pwerr)
			}
		case naive.indices[nw] == indexreg.Closed:
			if !errors.Is(pwerr, indexreg.ErrIndexClosed) {
				t.Fatalf("seq=%d step=%d Write(%s) want closed got %v", seq, step, an, pwerr)
			}
		default:
			if pwerr != nil {
				t.Fatalf("seq=%d step=%d Write(%s) unexpected %v", seq, step, an, pwerr)
			}
		}
		if tracer.Touched() > 1 {
			t.Fatalf("seq=%d step=%d Write(%s) touched=%d > 1", seq, step, an, tracer.Touched())
		}
	}
}

func genBatch(rng *rand.Rand, pool []string) ([]alias.Action, string) {
	n := 1 + rng.Intn(6)
	acts := make([]alias.Action, 0, n)
	desc := "["
	aliasPool := []string{"a0", "a1", "a2", "i0", "i1"} // 故意混入索引名以触发冲突
	for i := 0; i < n; i++ {
		ix := pool[rng.Intn(len(pool))]
		// 有时引用不存在的索引，触发逐步错误。
		if rng.Intn(8) == 0 {
			ix = fmt.Sprintf("ghost%d", rng.Intn(3))
		}
		var a alias.Action
		switch rng.Intn(3) {
		case 0:
			an := aliasPool[rng.Intn(len(aliasPool))]
			tri := []alias.TriState{alias.WriteUnspecified, alias.WriteTrue, alias.WriteFalse}[rng.Intn(3)]
			filter := ""
			if rng.Intn(3) == 0 {
				filter = "f"
			}
			a = alias.Add(an, ix, tri, filter)
		case 1:
			an := aliasPool[rng.Intn(len(aliasPool))]
			a = alias.Remove(an, ix, rng.Intn(2) == 0)
		case 2:
			a = alias.RemoveIndex(ix)
		}
		acts = append(acts, a)
		if i > 0 {
			desc += " "
		}
		desc += describeAction(a)
	}
	desc += "]"
	return acts, desc
}

func describeAction(a alias.Action) string {
	switch alias.ActionKind(a) {
	case 0:
		return fmt.Sprintf("Add(%s->%s,%v,%q)", alias.ActionAliasName(a), a.Index, a.IsWrite, a.Filter)
	case 1:
		return fmt.Sprintf("Remove(%s->%s,must=%v)", alias.ActionAliasName(a), a.Index, alias.ActionMustExist(a))
	default:
		return fmt.Sprintf("RemoveIndex(%s)", a.Index)
	}
}
