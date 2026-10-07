package lifecycle_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/lifecycle"
	"ontology/lifecycle/naive"
)

const (
	diffStateCount = 4
	diffNodeCount  = 6
)

type diffConfig struct {
	preAttr   [3]bool
	preCount  [3]bool
	cardMax   [3]int
	hookState [3]int
	cascade   [3]bool
	mutex     [3]bool
	needAttr  [3]int64
}

type diffWorld struct {
	eng   *lifecycle.Engine
	nav   *naive.Model
	logB  *bytes.Buffer
	ids   []string
	roots []string // 没有入边、可作为直接根请求的实例
}

func stateName(i int) string { return fmt.Sprintf("s%d", i) }

func buildDiffWorld(t *testing.T, rng *rand.Rand, cfg diffConfig) *diffWorld {
	t.Helper()
	store := lifecycle.NewStore()
	states := []lifecycle.State{}
	for i := 0; i < diffStateCount; i++ {
		states = append(states, lifecycle.State(stateName(i)))
	}
	rules := map[string]*lifecycle.TransitionRule{}
	nrules := map[string]*naive.Rule{}
	for i := 0; i < diffStateCount-1; i++ {
		name := fmt.Sprintf("r%d", i)
		from := lifecycle.State(stateName(i))
		to := lifecycle.State(stateName(i + 1))
		r := &lifecycle.TransitionRule{Name: name, From: []lifecycle.State{from}, To: to}
		nr := &naive.Rule{Name: name, From: []naive.State{naive.State(from)}, To: naive.State(to)}
		if cfg.preAttr[i] {
			r.Preconditions = append(r.Preconditions, lifecycle.Precondition{
				Attr: &lifecycle.AttrCheck{Key: "v", Op: lifecycle.CmpGe, Value: cfg.needAttr[i]}})
			nr.Preconditions = append(nr.Preconditions, naive.Precondition{
				Attr: &naive.AttrCheck{Key: "v", Op: "ge", Value: cfg.needAttr[i]}})
		}
		if cfg.preCount[i] {
			r.Preconditions = append(r.Preconditions, lifecycle.Precondition{
				LinkCount: &lifecycle.LinkCountCheck{Link: "edge", Min: 1, Max: -1}})
			nr.Preconditions = append(nr.Preconditions, naive.Precondition{
				LinkCount: &naive.LinkCountCheck{Link: "edge", Min: 1, Max: -1}})
		}
		if cfg.cardMax[i] >= 0 {
			r.Cardinality = append(r.Cardinality,
				lifecycle.CardinalityBound{Link: "edge", Min: 0, Max: cfg.cardMax[i]})
			nr.Cardinality = append(nr.Cardinality,
				naive.CardinalityBound{Link: "edge", Min: 0, Max: cfg.cardMax[i]})
		}
		if cfg.hookState[i] >= 0 {
			req := []lifecycle.State{lifecycle.State(stateName(cfg.hookState[i]))}
			r.Hooks = append(r.Hooks, lifecycle.Hook{Link: "edge", RequireStates: req})
			nr.Hooks = append(nr.Hooks, naive.Hook{Link: "edge",
				RequireStates: []naive.State{naive.State(stateName(cfg.hookState[i]))}})
		}
		if cfg.cascade[i] {
			r.Cascades = append(r.Cascades, lifecycle.Cascade{Link: "edge", ToRule: name})
			nr.Cascades = append(nr.Cascades, naive.Cascade{Link: "edge", ToRule: name})
		}
		if cfg.mutex[i] {
			r.MutexGroup = "m"
			nr.MutexGroup = "m"
		}
		rules[name] = r
		nrules[name] = nr
	}
	if err := store.RegisterType(&lifecycle.ObjectType{
		Name: "T", States: states, Initial: "s0",
		TerminalsList: []lifecycle.State{"s3"}, Transitions: rules,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	var logBuf bytes.Buffer
	eng := lifecycle.NewEngine(store, lifecycle.NewWriterLogger(&logBuf))
	nav := naive.NewModel()
	nav.RegisterType(&naive.Type{
		Name: "T", Initial: "s0", Terminals: []naive.State{"s3"}, Rules: nrules,
	})

	ids := make([]string, diffNodeCount)
	for i := range ids {
		id := fmt.Sprintf("n%d", i)
		ids[i] = id
		if _, err := store.CreateInstance(lifecycle.InstanceID(id), "T"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := nav.Create(naive.InstanceID(id), "T"); err != nil {
			t.Fatalf("naive create: %v", err)
		}
	}
	hasIncoming := make([]bool, diffNodeCount)
	for i := 0; i+1 < diffNodeCount; i++ {
		if rng.Intn(2) == 0 {
			// 只连接到编号更大且尚无入边的节点：构造一组 DAG 上的链/扇出
			// 树（每个节点至多一条入边），覆盖链式触发、扇出、钩子、基数
			// 与互斥。纯循环、多链汇聚由固定用例与设计说明单独处理。
			j := i + 1 + rng.Intn(diffNodeCount-i-1)
			if hasIncoming[j] {
				continue
			}
			hasIncoming[j] = true
			if err := eng.ModifyLinks(lifecycle.InstanceID(ids[i]),
				lifecycle.LinkOp{Link: "edge", Target: lifecycle.InstanceID(ids[j]), Op: lifecycle.LinkAdd}); err != nil {
				t.Fatalf("seed link: %v", err)
			}
			if code := nav.ModifyLinks(naive.InstanceID(ids[i]),
				naive.LinkOp{Link: "edge", Target: naive.InstanceID(ids[j]), Op: "add"}); code != 0 {
				t.Fatalf("naive seed link code=%d", code)
			}
		}
	}
	rootIDs := []string{}
	for i, id := range ids {
		if !hasIncoming[i] {
			rootIDs = append(rootIDs, id)
		}
	}
	return &diffWorld{eng: eng, nav: nav, logB: &logBuf, ids: ids, roots: rootIDs}
}

func (w *diffWorld) ruleFor(id string) (string, bool) {
	st, ok := w.nav.State(naive.InstanceID(id))
	if !ok {
		return "", false
	}
	for i := 0; i < diffStateCount-1; i++ {
		if string(st) == stateName(i) {
			return fmt.Sprintf("r%d", i), true
		}
	}
	return "", false
}

func randomConfig(rng *rand.Rand) diffConfig {
	var c diffConfig
	for i := 0; i < 3; i++ {
		c.preAttr[i] = rng.Intn(2) == 0
		c.preCount[i] = rng.Intn(3) == 0
		c.cardMax[i] = []int{-1, -1, 1, 2}[rng.Intn(4)]
		c.hookState[i] = []int{-1, -1, 1, 2, 3}[rng.Intn(5)]
		c.cascade[i] = rng.Intn(2) == 0
		c.mutex[i] = rng.Intn(4) == 0
		c.needAttr[i] = int64(rng.Intn(3))
	}
	return c
}

func tailLog(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.Join(lines, "\n")
}
