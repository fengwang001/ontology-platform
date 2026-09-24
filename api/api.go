// Package api 是对外接口层：包装 alm.Store 并提供自检。
package api

import (
	"errors"
	"math"
	"reflect"

	"ontology/alm"
	"ontology/thr"
)

// 可判定哨兵错误（定义在 alm，此处再导出），四者互不相同。
var (
	ErrBadParam      = alm.ErrBadParam
	ErrBadHysteresis = alm.ErrBadHysteresis
	ErrEmptyKey      = alm.ErrEmptyKey
	ErrOverflow      = alm.ErrOverflow
)

// Event 是一次报警状态翻转事件。
type Event = alm.Event

// API 是报警流句柄，并发安全。
type API struct{ s *alm.Store }

// New 校验 (T, H, maxKeys)，非法时返回可判定错误。
func New(t, h int64, maxKeys int) (*API, error) {
	s, err := alm.NewStore(t, h, maxKeys)
	if err != nil {
		return nil, err
	}
	return &API{s: s}, nil
}

// Add 累加并返回本次事件（0 或 1 条）；被拒时不留痕。
func (a *API) Add(key string, delta int64) ([]Event, error) { return a.s.Add(key, delta) }

// View 返回各 Key 当前累计值。
func (a *API) View() map[string]int64 { return a.s.View() }

// Alarm 返回各 Key 当前 on 状态。
func (a *API) Alarm() map[string]bool { return a.s.Alarm() }

// Events 返回全部事件，按发生顺序。
func (a *API) Events() []Event { return a.s.Events() }

// Check 是一条自检结论。
type Check struct {
	Name string
	OK   bool
}

type step struct {
	key   string
	delta int64
}

// naive 是朴素参照：从 0 起步逐步按规则判定。
func naive(t, h int64, steps []step) (map[string]int64, map[string]bool, []Event) {
	vals, ons := map[string]int64{}, map[string]bool{}
	var evs []Event
	for _, st := range steps {
		old := vals[st.key]
		nv := old + st.delta
		on, k := thr.Judge(old, nv, ons[st.key], t, h)
		vals[st.key], ons[st.key] = nv, on
		if k != thr.KindNone {
			evs = append(evs, Event{Seq: int64(len(evs)) + 1, Key: st.key, Kind: k, Value: nv})
		}
	}
	return vals, ons, evs
}

func replay(t, h int64, steps []step) *API {
	a, err := New(t, h, len(steps)+1)
	if err != nil {
		panic(err)
	}
	for _, st := range steps {
		if _, err := a.Add(st.key, st.delta); err != nil {
			panic(err)
		}
	}
	return a
}

// SelfCheck 对内置 Add 序列核验四条不变量与 Add 检查个数上界。
func (a *API) SelfCheck() []Check {
	var out []Check
	add := func(name string, ok bool) { out = append(out, Check{name, ok}) }

	// 不变量 1：与朴素参照一致（含第三节八步与多 Key 混合序列）。
	seqs := [][]step{
		{{"k", 10}, {"k", 2}, {"k", -5}, {"k", 2}, {"k", -2}, {"k", 4}, {"k", -7}, {"k", 3}},
		{{"a", 10}, {"b", 7}, {"a", -8}, {"b", 3}, {"a", 5}, {"b", -9}, {"c", 20}, {"c", -15}},
		{{"x", -5}, {"x", 15}, {"x", -3}, {"y", 10}, {"x", -2}, {"y", -4}},
	}
	ok := true
	for _, sq := range seqs {
		g := replay(10, 3, sq)
		wv, wo, we := naive(10, 3, sq)
		if !reflect.DeepEqual(g.View(), wv) || !reflect.DeepEqual(g.Alarm(), wo) ||
			!reflect.DeepEqual(g.Events(), we) {
			ok = false
		}
	}
	add("naive-reference", ok)

	// 不变量 2：不重复不遗漏——ON 期间反复 >=T 只一条 ON，OFF 期间反复 <T 无 OFF。
	g := replay(10, 3, []step{{"k", 10}, {"k", 5}, {"k", -1}, {"k", 3}, {"k", -2},
		{"k", -20}, {"k", -5}, {"k", 2}, {"k", 1}})
	evs := g.Events()
	add("no-dup-no-omit", len(evs) == 2 && evs[0].Kind == thr.KindOn && evs[1].Kind == thr.KindOff)

	// 不变量 3：去抖——ON 后在 [T-H, T) 抖动（含 ==T-H）不 OFF，<T-H 才 OFF。
	g = replay(10, 3, []step{{"k", 10}, {"k", -3}, {"k", 2}, {"k", -2}, {"k", 1}, {"k", -1}})
	ok = len(g.Events()) == 1 && g.Alarm()["k"] && g.View()["k"] == 7
	g2 := replay(10, 3, []step{{"k", 10}, {"k", -4}})
	ok = ok && len(g2.Events()) == 2 && g2.Events()[1].Kind == thr.KindOff
	add("debounce", ok)

	// 不变量 4：失败不留痕 + 四类可判定且互不相同的错误。
	bad := []error{
		func() error { _, e := New(0, 3, 4); return e }(),
		func() error { _, e := New(10, 10, 4); return e }(),
		func() error { _, e := g.Add("", 1); return e }(),
		func() error { _, e := g.Add("k", math.MaxInt64); return e }(),
	}
	ok = errors.Is(bad[0], ErrBadParam) && errors.Is(bad[1], ErrBadHysteresis) &&
		errors.Is(bad[2], ErrEmptyKey) && errors.Is(bad[3], ErrOverflow)
	for i := range bad {
		for j := range bad {
			if i != j && errors.Is(bad[i], bad[j]) {
				ok = false
			}
		}
	}
	add("distinct-errors", ok)
	ok = len(g.Events()) == 1 && g.View()["k"] == 7 && g.Alarm()["k"] // 与注入前一致
	if _, err := g.Add("k", -7); err != nil || len(g.Events()) != 2 { // 仍可正常使用
		ok = false
	}
	add("reject-no-trace", ok)

	// 复杂度：Add 检查个数不随 m 增长。
	add("add-cost-bounded", alm.VerifyAddCost())
	return out
}
