
// Package apply 串行执行重命名计划，写撤销日志，并在失败时自动回滚。
package apply

import (
	"errors"
	"os"

	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

// ErrInjected 表示测试注入的第 k 步故障。
var ErrInjected = errors.New("apply: injected step failure")

// Options 控制执行：LogDir 为日志目录；FailAt 为 1 基步号，命中则失败。
type Options struct {
	LogDir string
	FailAt int
}

// Result 是执行结果。
type Result struct {
	Steps []plan.Step
	Temps []string
	Log   *undo.Log
}

// Build 仅做编译与破环（在持锁之外、修改之前），便于检查最终步骤。
func Build(reqs []plan.Req, sp *name.Space) ([]plan.Step, []string, error) {
	pl, err := plan.Compile(reqs, sp.Snapshot())
	if err != nil {
		return nil, nil, err
	}
	reserved := append(append([]string{}, sp.Snapshot()...), destinations(reqs)...)
	st, temps, err := cycle.Break(pl, reserved)
	if err != nil {
		return nil, nil, err
	}
	return st, temps, nil
}

func destinations(reqs []plan.Req) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.To
	}
	return out
}

// Execute 持写锁执行整批：任一步失败（含注入）即逆序回滚已完成步骤，
	并返回错误；命名空间最终与执行前逐元素相同。
func Execute(reqs []plan.Req, sp *name.Space, opt Options) (*Result, error) {
	st, temps, err := Build(reqs, sp)
	if err != nil {
		return nil, err
	}
	dir := opt.LogDir
	if dir == "" {
		dir, _ = os.MkdirTemp("", "rename-log-")
	}
	lg, err := undo.Create(dir, "rename")
	if err != nil {
		return nil, err
	}

	sp.Lock()
	var recorded []plan.Step
	for i, s := range st {
		if opt.FailAt > 0 && i+1 == opt.FailAt {
			rollbackLocked(sp, recorded)
			sp.Unlock()
			return nil, ErrInjected
		}
		if err := sp.MoveLocked(s.From, s.To); err != nil {
			rollbackLocked(sp, recorded)
			sp.Unlock()
			return nil, err
		}
		if err := lg.Append(undo.Step{From: s.From, To: s.To}); err != nil {
			rollbackLocked(sp, recorded)
			sp.Unlock()
			return nil, err
		}
		recorded = append(recorded, s)
	}
	sp.Unlock()

	return &Result{Steps: st, Temps: temps, Log: lg}, nil
}

// rollbackLocked 在持写锁时逆序撤销已完成步骤。
func rollbackLocked(sp *name.Space, done []plan.Step) {
	for i := len(done) - 1; i >= 0; i-- {
		_ = sp.MoveLocked(done[i].To, done[i].From)
	}
}
