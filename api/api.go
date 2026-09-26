// Package api 是对外门面：构造、应用 delta、读取状态与版本、内置自检。
package api

import (
	"errors"
	"reflect"

	"ontology/delta"
	"ontology/replica"
)

// API 是增量快照同步副本的对外句柄。
type API struct {
	r *replica.Replica
}

// New 返回版本 0、空状态的实例。
func New() *API {
	return &API{r: replica.New()}
}

// Apply 应用一个 delta；非法或乱序时返回可判定错误且状态不变。
func (a *API) Apply(d delta.Delta) error {
	return a.r.Apply(d)
}

// State 返回当前状态副本。
func (a *API) State() map[string]int {
	return a.r.State()
}

// Version 返回当前版本号。
func (a *API) Version() int {
	return a.r.Version()
}

// SelfCheck 对一组内置 delta 序列核验四条不变量，全部通过返回 nil，
// 否则返回第一个失败的检查点错误。
func (a *API) SelfCheck() error {
	// 不变量 1+2：顺序应用与版本推进。
	seq := []delta.Delta{
		{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 1}}},
		{From: 1, To: 2, Changes: []delta.Change{{Kind: delta.Set, Key: "b", Val: 2}}},
		{From: 2, To: 3, Changes: []delta.Change{
			{Kind: delta.Set, Key: "c", Val: 3},
			{Kind: delta.Del, Key: "b"},
		}},
		{From: 3, To: 4, Changes: []delta.Change{{Kind: delta.Set, Key: "d", Val: 4}}},
	}
	fresh := New()
	for _, d := range seq {
		if err := fresh.Apply(d); err != nil {
			return err
		}
		if fresh.Version() != d.To {
			return errors.New("selfcheck: version not advanced to To")
		}
	}
	want := map[string]int{"a": 1, "c": 3, "d": 4}
	if !reflect.DeepEqual(fresh.State(), want) {
		return errors.New("selfcheck: state mismatch with naive replay")
	}

	// 不变量 3：重复 delta 幂等。
	dup := delta.Delta{From: 0, To: 1, Changes: []delta.Change{{Kind: delta.Set, Key: "a", Val: 100}}}
	if err := fresh.Apply(dup); err != nil {
		return err
	}
	if fresh.Version() != 4 || !reflect.DeepEqual(fresh.State(), want) {
		return errors.New("selfcheck: duplicate delta not idempotent")
	}

	// 不变量 4：三类故障注入均被拒绝且状态不变。
	bads := []struct {
		d   delta.Delta
		err error
	}{
		{delta.Delta{From: 9, To: 10}, replica.ErrGap},
		{delta.Delta{From: 5, To: 5}, replica.ErrInvalidRange},
		{delta.Delta{From: -1, To: 0}, replica.ErrNegativeVersion},
	}
	for _, b := range bads {
		if err := fresh.Apply(b.d); !errors.Is(err, b.err) {
			return errors.New("selfcheck: bad delta not rejected with expected error")
		}
	}
	if fresh.Version() != 4 || !reflect.DeepEqual(fresh.State(), want) {
		return errors.New("selfcheck: rejected delta left trace")
	}
	return nil
}
