package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func seven() []Def {
	return []Def{
		{Name: "A", Kind: "src", K: 1}, {Name: "E", Kind: "src", K: 100}, {Name: "B", Kind: "scale", Inputs: []string{"A"}, K: 2}, {Name: "C", Kind: "add", Inputs: []string{"A"}, K: 10},
		{Name: "D", Kind: "sum", Inputs: []string{"B", "C"}}, {Name: "F", Kind: "sum", Inputs: []string{"D", "E"}}, {Name: "G", Kind: "sum", Inputs: []string{"A", "D"}},
	}
}
func chg(n string, o, v int64) Change { return Change{Name: n, Old: o, New: v} }
func ok(t *testing.T, cond bool, msg string, args ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(msg, args...)
	}
}

type tc struct {
	defs    []Def
	sets    map[string]int64
	wantErr error
	wantLog []Change
}

func TestInitialAndApplyLogs(t *testing.T) {
	a, err := New(seven())
	ok(t, err == nil, "%v", err)
	ok(t, reflect.DeepEqual(a.View(), map[string]int64{"A": 1, "E": 100, "B": 2, "C": 11, "D": 13, "F": 113, "G": 14}), "initial values: %v", a.View())
	levels := []int{}
	for _, n := range []string{"A", "E", "B", "C", "D", "F", "G"} {
		levels = append(levels, a.g.Level(n))
	}
	ok(t, reflect.DeepEqual(levels, []int{0, 0, 1, 1, 2, 3, 3}), "levels: %v", levels)
	steps := []tc{
		{sets: map[string]int64{"A": 5}, wantLog: []Change{chg("A", 1, 5), chg("B", 2, 10), chg("C", 11, 15), chg("D", 13, 25), chg("F", 113, 125), chg("G", 14, 30)}},
		{sets: map[string]int64{"A": 0, "E": 10}, wantLog: []Change{chg("A", 5, 0), chg("E", 100, 10), chg("B", 10, 0), chg("C", 15, 10), chg("D", 25, 10), chg("F", 125, 20), chg("G", 30, 10)}},
	}
	for i, s := range steps {
		log, err := a.Apply(s.sets)
		ok(t, err == nil && reflect.DeepEqual(log, s.wantLog), "step %d: %v %v", i, log, err)
	}
}

// 不变量 1：随机赋值序列，每次 Apply 后与朴素全量重算一致。
func TestFullRecomputeConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	a, _ := New(seven())
	for i := 0; i < 200; i++ {
		sets := map[string]int64{"A": int64(rng.Intn(21) - 10), "E": int64(rng.Intn(200))}
		_, err := a.Apply(sets)
		ok(t, err == nil, "%v", err)
		ok(t, reflect.DeepEqual(a.View(), a.fullRecompute()), "iter %d", i)
	}
}

// 不变量 2：日志每节点至多一次、旧新值正确、排在其日志内输入之后。
func TestLogWellFormed(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	a, _ := New(seven())
	for i := 0; i < 200; i++ {
		pre := a.View()
		log, err := a.Apply(map[string]int64{"A": int64(rng.Intn(11) - 5), "E": int64(rng.Intn(200))})
		ok(t, err == nil, "%v", err)
		ok(t, checkLog(pre, a.View(), log, a.g) == nil, "iter %d", i)
	}
}

// 不变量 3：设成当前值返回空日志。
func TestNoopApplyEmpty(t *testing.T) {
	a, _ := New(seven())
	for _, sets := range []map[string]int64{{"A": 1}, {"A": 1, "E": 100}, {}} {
		log, err := a.Apply(sets)
		ok(t, err == nil && len(log) == 0, "sets=%v log=%v err=%v", sets, log, err)
	}
}

// 四类可判定错误互不相同；New 按 定义非法→节点不存在→成环 只报第一条。
func TestFaultInjection(t *testing.T) {
	cases := []tc{
		{defs: []Def{{Name: "", Kind: "src"}}, wantErr: ErrInvalidDef},
		{defs: []Def{{Name: "X", Kind: "src"}, {Name: "X", Kind: "src"}}, wantErr: ErrInvalidDef},
		{defs: []Def{{Name: "S", Kind: "src"}, {Name: "X", Kind: "sum", Inputs: []string{"S", "S"}}}, wantErr: ErrInvalidDef},
		{defs: []Def{{Name: "X", Kind: "sum"}}, wantErr: ErrInvalidDef},
		{defs: []Def{{Name: "X", Kind: "bogus"}}, wantErr: ErrInvalidDef},
		{defs: []Def{{Name: "X", Kind: "add", Inputs: []string{"ZZ"}, K: 1}}, wantErr: ErrUnknownNode},
		{defs: []Def{{Name: "X", Kind: "add", Inputs: []string{"Y"}, K: 1}, {Name: "Y", Kind: "add", Inputs: []string{"X"}, K: 1}}, wantErr: ErrCycle},
		{defs: []Def{{Name: "X", Kind: "add", Inputs: []string{"X"}, K: 1}}, wantErr: ErrCycle},
		{defs: []Def{{Kind: "bogus"}, {Name: "Y", Kind: "add", Inputs: []string{"ZZ"}, K: 1}}, wantErr: ErrInvalidDef},
		{defs: []Def{{Name: "Y", Kind: "add", Inputs: []string{"ZZ"}, K: 1}, {Name: "Z", Kind: "add", Inputs: []string{"Z"}, K: 1}}, wantErr: ErrUnknownNode},
	}
	for i, c := range cases {
		_, err := New(c.defs)
		ok(t, errors.Is(err, c.wantErr), "case %d: err=%v want %v", i, err, c.wantErr)
	}
	a, _ := New(seven())
	applyCases := []tc{
		{sets: map[string]int64{"NOPE": 1}, wantErr: ErrUnknownNode},
		{sets: map[string]int64{"D": 1}, wantErr: ErrNotSource},
		{sets: map[string]int64{"NOPE": 1, "D": 2}, wantErr: ErrUnknownNode},
	}
	for i, c := range applyCases {
		_, err := a.Apply(c.sets)
		ok(t, errors.Is(err, c.wantErr), "apply case %d: err=%v", i, err)
	}
}

// 不变量 4：被拒的 Apply 不改任何状态，之后仍可正常使用。
func TestRejectedKeepsState(t *testing.T) {
	a, _ := New(seven())
	a.Apply(map[string]int64{"A": 5})
	pre := a.View()
	for _, sets := range []map[string]int64{{"D": 1}, {"NOPE": 1}, {"A": 9, "D": 1}} {
		_, err := a.Apply(sets)
		ok(t, err != nil, "sets=%v should be rejected", sets)
	}
	ok(t, reflect.DeepEqual(a.View(), pre), "rejected apply changed state")
	log, err := a.Apply(map[string]int64{"A": 7})
	ok(t, err == nil && len(log) > 0, "usable after rejection: %v", err)
}
func TestConcurrentViewGlitchFree(t *testing.T) {
	a, _ := New(seven())
	var wg sync.WaitGroup
	var bad atomic.Bool
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				v := a.View()
				if v["D"] != v["B"]+v["C"] || v["G"] != v["A"]+v["D"] || v["F"] != v["D"]+v["E"] {
					bad.Store(true)
				}
			}
		}()
	}
	for i := 1; i <= 2000; i++ {
		a.Apply(map[string]int64{"A": int64(i % 7), "E": int64(i % 5)})
	}
	wg.Wait()
	ok(t, !bad.Load(), "glitch view observed")
}
