package erase

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/refs"
	"ontology/store"
)

// model 是测试侧的朴素镜像：直接维护记录、边、保全与墓碑，
// 用全库扫描求不动点的方式计算期望报告，与 Executor 的索引实现对照。
type model struct {
	owners map[int64]map[string]bool
	edges  map[refs.Edge]refs.Policy
	holds  map[int64]int64
	tomb   map[string]bool
}

func newModel() *model {
	return &model{
		owners: make(map[int64]map[string]bool),
		edges:  make(map[refs.Edge]refs.Policy),
		holds:  make(map[int64]int64),
		tomb:   make(map[string]bool),
	}
}

func (m *model) held(id, now int64) bool {
	until, ok := m.holds[id]
	return ok && now < until
}

func (m *model) outDegree(id int64) int {
	n := 0
	for e := range m.edges {
		if e.Child == id {
			n++
		}
	}
	return n
}

func (m *model) inDegree(id int64) int {
	n := 0
	for e := range m.edges {
		if e.Parent == id {
			n++
		}
	}
	return n
}

// naiveErase 对全库求不动点，返回期望报告、期望 Restrict 错误与 visited 上界。
func (m *model) naiveErase(subject string, now int64) (Report, *RestrictedError, int) {
	rep := Report{Deleted: []int64{}, Detached: []int64{}, Anonymized: []int64{}, Unlinked: []refs.Edge{}}
	var seeds []int64
	for id, os := range m.owners {
		if os[subject] {
			seeds = append(seeds, id)
		}
	}
	slices.Sort(seeds)
	inD := make(map[int64]bool)
	for _, id := range seeds {
		if len(m.owners[id]) == 1 {
			if m.held(id, now) {
				rep.Anonymized = append(rep.Anonymized, id)
			} else {
				inD[id] = true
			}
		} else {
			rep.Detached = append(rep.Detached, id)
		}
	}
	for changed := true; changed; {
		changed = false
		for id := range m.owners {
			if inD[id] || len(m.owners[id]) != 0 || m.held(id, now) {
				continue
			}
			for e, p := range m.edges {
				if e.Child == id && p == refs.Cascade && inD[e.Parent] {
					inD[id] = true
					changed = true
					break
				}
			}
		}
	}
	var hit *RestrictedError
	for e, p := range m.edges {
		if !inD[e.Parent] || inD[e.Child] {
			continue
		}
		if p == refs.Restrict {
			if hit == nil || e.Parent < hit.Record ||
				(e.Parent == hit.Record && e.Child < hit.Referrer) {
				hit = &RestrictedError{Record: e.Parent, Referrer: e.Child}
			}
		} else {
			rep.Unlinked = append(rep.Unlinked, e)
		}
	}
	if hit != nil {
		return Report{}, hit, 0
	}
	for id := range inD {
		rep.Deleted = append(rep.Deleted, id)
	}
	slices.Sort(rep.Deleted)
	slices.SortFunc(rep.Unlinked, func(a, b refs.Edge) int {
		if a.Child != b.Child {
			return compare(a.Child, b.Child)
		}
		return compare(a.Parent, b.Parent)
	})
	bound := len(seeds)
	for id := range inD {
		bound += m.inDegree(id)
	}
	return rep, nil, bound
}

func (m *model) applyErase(subject string, rep Report) {
	for _, e := range rep.Unlinked {
		delete(m.edges, e)
	}
	for _, id := range rep.Detached {
		delete(m.owners[id], subject)
	}
	for _, id := range rep.Anonymized {
		delete(m.owners[id], subject)
	}
	for _, id := range rep.Deleted {
		delete(m.owners, id)
		delete(m.holds, id)
		for e := range m.edges {
			if e.Child == id || e.Parent == id {
				delete(m.edges, e)
			}
		}
	}
	m.tomb[subject] = true
}

var subjects = []string{"s0", "s1", "s2"}

func randomOwners(rng *rand.Rand) []string {
	perm := rng.Perm(len(subjects))
	n := rng.Intn(len(subjects) + 1)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, subjects[perm[i]])
	}
	return out
}

// runTrial 执行一条随机操作序列，逐步对照 Executor 与朴素模型，
// 返回每次 Erase 的结果串供重放一致性比较。
func runTrial(t *testing.T, trial int, verbose bool) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(trial)*7919 + 13))
	ex := NewExecutor(store.New(), refs.New())
	m := newModel()
	var outcomes []string
	now := int64(0)
	ops := 30 + rng.Intn(30)
	for i := 0; i < ops; i++ {
		now += int64(rng.Intn(4))
		switch rng.Intn(6) {
		case 0: // Put
			id := 1 + rng.Int63n(30)
			owners := randomOwners(rng)
			err := ex.Put(id, owners, now)
			var want error
			switch {
			case m.owners[id] != nil:
				want = ErrExists
			default:
				for _, o := range owners {
					if m.tomb[o] {
						want = ErrErased
					}
				}
			}
			assertErr(t, trial, i, "Put", err, want)
			if want == nil {
				set := make(map[string]bool)
				for _, o := range owners {
					set[o] = true
				}
				m.owners[id] = set
			}
		case 1: // AddRef
			child := 1 + rng.Int63n(30)
			parent := 1 + rng.Int63n(30)
			if child == parent {
				continue
			}
			pol := refs.Policy(rng.Intn(3))
			err := ex.AddRef(child, parent, pol, now)
			var want error
			switch {
			case m.owners[child] == nil || m.owners[parent] == nil:
				want = ErrNotFound
			case func() bool { _, ok := m.edges[refs.Edge{Child: child, Parent: parent}]; return ok }():
				want = ErrExists
			case m.outDegree(child) >= refs.MaxOut:
				want = ErrTooManyOutgoing
			}
			assertErr(t, trial, i, "AddRef", err, want)
			if want == nil {
				m.edges[refs.Edge{Child: child, Parent: parent}] = pol
			}
		case 2: // RemoveRef
			child := 1 + rng.Int63n(30)
			parent := 1 + rng.Int63n(30)
			if child == parent {
				continue
			}
			err := ex.RemoveRef(child, parent, now)
			var want error
			_, edgeOK := m.edges[refs.Edge{Child: child, Parent: parent}]
			if m.owners[child] == nil || m.owners[parent] == nil || !edgeOK {
				want = ErrNotFound
			}
			assertErr(t, trial, i, "RemoveRef", err, want)
			if want == nil {
				delete(m.edges, refs.Edge{Child: child, Parent: parent})
			}
		case 3: // Hold
			id := 1 + rng.Int63n(30)
			until := now + 1 + rng.Int63n(6)
			err := ex.Hold(id, until, now)
			var want error
			if m.owners[id] == nil {
				want = ErrNotFound
			}
			assertErr(t, trial, i, "Hold", err, want)
			if want == nil {
				m.holds[id] = until
			}
		case 4, 5: // Plan / Erase
			subject := subjects[rng.Intn(len(subjects))]
			isErase := rng.Intn(2) == 0
			kind := "Plan"
			if isErase {
				kind = "Erase"
			}
			if m.tomb[subject] {
				var err error
				if isErase {
					_, err = ex.Erase(subject, now)
				} else {
					_, err = ex.Plan(subject, now)
				}
				assertErr(t, trial, i, kind, err, ErrAlreadyErased)
				continue
			}
			wantRep, wantRestrict, bound := m.naiveErase(subject, now)
			var got Report
			var err error
			if isErase {
				got, err = ex.Erase(subject, now)
			} else {
				got, err = ex.Plan(subject, now)
			}
			if verbose {
				t.Logf("trial=%d op=%d %s(%q, now=%d) -> report=%+v err=%v | 依据: 模型不动点 D=%v 上界=%d",
					trial, i, kind, subject, now, got, err, wantRep.Deleted, bound)
			}
			if wantRestrict != nil {
				var re *RestrictedError
				if !errors.Is(err, ErrRestricted) || !errors.As(err, &re) || *re != *wantRestrict {
					t.Fatalf("trial=%d op=%d %s: got err=%v, want Restricted%+v", trial, i, kind, err, wantRestrict)
				}
				outcomes = append(outcomes, fmt.Sprintf("%s:restricted:%v", kind, wantRestrict))
				continue
			}
			if err != nil {
				t.Fatalf("trial=%d op=%d %s: 意外错误 %v", trial, i, kind, err)
			}
			if !reflect.DeepEqual(got, wantRep) {
				t.Fatalf("trial=%d op=%d %s 报告不符\n got: %+v\nwant: %+v", trial, i, kind, got, wantRep)
			}
			if ex.visited > bound {
				t.Fatalf("trial=%d op=%d %s: visited=%d 超过上界 %d", trial, i, kind, ex.visited, bound)
			}
			outcomes = append(outcomes, fmt.Sprintf("%s:%+v", kind, got))
			if isErase {
				m.applyErase(subject, got)
				// 不变量：没有任何记录归属 s，没有任何边指向已删除记录。
				for id, os := range m.owners {
					if os[subject] {
						t.Fatalf("trial=%d: 记录 %d 仍归属 %q", trial, id, subject)
					}
				}
				for _, id := range got.Deleted {
					for e := range m.edges {
						if e.Parent == id {
							t.Fatalf("trial=%d: 边 %+v 仍指向已删除记录 %d", trial, e, id)
						}
					}
				}
			}
		}
	}
	return outcomes
}

func assertErr(t *testing.T, trial, op int, kind string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("trial=%d op=%d %s: got %v, want %v", trial, op, kind, got, want)
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	for trial := 0; trial < 1500; trial++ {
		first := runTrial(t, trial, trial < 2)
		second := runTrial(t, trial, false)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("trial=%d 重放结果不一致\n first: %v\nsecond: %v", trial, first, second)
		}
	}
}

func TestVisitedIndependentOfUnrelated(t *testing.T) {
	for _, extra := range []int{100, 10000} {
		t.Run(fmt.Sprintf("无关记录%d", extra), func(t *testing.T) {
			ex := newExec()
			must(t, ex.Put(1, []string{"s"}, 0))
			must(t, ex.Put(2, nil, 0))
			must(t, ex.AddRef(2, 1, refs.Cascade, 0))
			for i := 0; i < extra; i++ {
				id := int64(1000 + i)
				must(t, ex.Put(id, []string{"u"}, 0))
				if i > 0 {
					must(t, ex.AddRef(id, id-1, refs.Cascade, 0))
				}
			}
			got, err := ex.Erase("s", 1)
			must(t, err)
			checkReport(t, got, rep([]int64{1, 2}, nil, nil, nil))
			// visited = 种子 1 + 记录 1 的入边 1 + 记录 2 的入边 0 = 2，与无关规模无关。
			if ex.visited != 2 {
				t.Fatalf("visited = %d, want 2", ex.visited)
			}
		})
	}
}

func TestConcurrent(t *testing.T) {
	ex := newExec()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			subject := fmt.Sprintf("s%d", g)
			base := int64(g * 1000)
			for i := int64(1); i <= 50; i++ {
				id := base + i
				_ = ex.Put(id, []string{subject}, i)
				if i > 1 {
					_ = ex.AddRef(id, id-1, refs.Cascade, i)
				}
				_ = ex.Hold(id, i+5, i)
				_, _ = ex.Plan(subject, i)
			}
			_, _ = ex.Erase(subject, 100)
		}(g)
	}
	wg.Wait()
}
