// Package api 是迟到纠正重处理的对外入口，依赖 chlog（后者依赖 agg）。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/agg"
	"ontology/chlog"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrBadWindow = errors.New("api: correction window W must be > 0")
	ErrEmptyKey  = errors.New("api: key must not be empty")
	ErrBadSeq    = errors.New("api: seq must be > 0")
)

// Change、Status 分别是 chlog.Change、agg.Kind 的对外别名。
type (
	Change = chlog.Change
	Status = agg.Kind
)

// 事件状态常量。
const StatusNew, StatusLate, StatusCorrection, StatusDuplicate, StatusStale = agg.KindNew, agg.KindLate, agg.KindCorrection, agg.KindDuplicate, agg.KindStale

// API 并发安全的重处理器，状态全部在进程内存。
type API struct {
	w     int
	mu    sync.RWMutex
	keys  map[string]*agg.Key
	log   *chlog.Log
	stale int
}

// New 创建窗口容量为 W 的处理器，W <= 0 返回 ErrBadWindow。
func New(W int) (*API, error) {
	if W <= 0 {
		return nil, ErrBadWindow
	}
	return &API{w: W, keys: map[string]*agg.Key{}, log: chlog.New()}, nil
}

// Apply 处理一条事件。校验类错误整体失败且不留痕（不变量 4）；
// 过期纠正是合法拒绝（StatusStale，nil error）。返回本次新产出的变更。
func (a *API) Apply(key string, seq, val int64) ([]Change, Status, error) {
	if key == "" { // 全部校验先于任何状态变更
		return nil, 0, ErrEmptyKey
	}
	if seq <= 0 {
		return nil, 0, ErrBadSeq
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	k, ok := a.keys[key]
	if !ok {
		k = agg.NewKey(a.w)
		a.keys[key] = k
	}
	r := k.Apply(seq, val)
	if r.Kind == agg.KindStale {
		a.stale++
		return nil, StatusStale, nil
	}
	cs, err := a.log.Emit(key, !ok, r)
	if err != nil {
		return nil, 0, err
	}
	return cs, Status(r.Kind), nil
}

// View 返回 key→当前 sum 的副本。
func (a *API) View() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.log.View()
}

// Stale 返回过期纠正计数。
func (a *API) Stale() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.stale
}

// SelfCheck 对内置八事件序列核验四条不变量，失败返回可判定错误。
func (a *API) SelfCheck() error {
	e, _ := New(3)
	ev := []struct {
		s, v int64
		st   Status
	}{
		{5, 10, StatusNew}, {7, 20, StatusNew}, {6, 15, StatusLate}, {8, 5, StatusNew},
		{5, 99, StatusStale}, {6, 15, StatusDuplicate}, {6, 30, StatusCorrection}, {9, 3, StatusNew},
	}
	want := []int64{10, 30, 45, 50, 50, 50, 65, 68} // 朴素批量重算的逐 key 和
	var all []Change
	for i, x := range ev { // 不变量 1：每步类别与 sum 与批量结果一致
		cs, st, err := e.Apply("k", x.s, x.v)
		if err != nil || st != x.st || e.View()["k"] != want[i] {
			return fmt.Errorf("selfcheck: step %d st=%d err=%v", i+1, st, err)
		}
		all = append(all, cs...)
	}
	cur := map[string]int64{} // 不变量 2：重放变更日志每个前缀
	for i, c := range all {
		v, has := cur[c.Key]
		if c.Plus == has || (!c.Plus && v != c.Sum) {
			return fmt.Errorf("selfcheck: inv2 prefix %d", i)
		}
		if c.Plus {
			cur[c.Key] = c.Sum
		} else {
			delete(cur, c.Key)
		}
	}
	if _, st, _ := e.Apply("k", 6, 30); st != StatusDuplicate { // 不变量 3：重复幂等
		return errors.New("selfcheck: inv3 duplicate not idempotent")
	}
	if e.View()["k"] != 68 || e.Stale() != 1 {
		return errors.New("selfcheck: inv3 state changed by duplicate")
	}
	if _, _, err := e.Apply("", 1, 1); !errors.Is(err, ErrEmptyKey) { // 不变量 4
		return errors.New("selfcheck: ErrEmptyKey missing")
	}
	if _, _, err := e.Apply("k", 0, 1); !errors.Is(err, ErrBadSeq) {
		return errors.New("selfcheck: ErrBadSeq missing")
	}
	if _, err := New(0); !errors.Is(err, ErrBadWindow) {
		return errors.New("selfcheck: ErrBadWindow missing")
	}
	if e.View()["k"] != 68 || e.Stale() != 1 {
		return errors.New("selfcheck: inv4 rejected op left a trace")
	}
	return nil
}
