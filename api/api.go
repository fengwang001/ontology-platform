// Package api 对外提供多副本反熵读修复引擎。依赖 rrep。
package api

import (
	"errors"
	"fmt"

	"ontology/rep"
	"ontology/rrep"
)

// 四类可判定、互不相同的哨兵错误。
var (
	ErrBadN        = errors.New("api: replica count n < 1")
	ErrBadVersion  = errors.New("api: version must be > 0")
	ErrEmptyKey    = errors.New("api: key must not be empty")
	ErrBadReplicas = errors.New("api: replica set empty or index out of range")
)

// Engine 是 n 副本读修复引擎。
type Engine struct {
	n  int
	st *rrep.Store
}

// New 创建引擎；n<1 时失败且不留任何状态。
func New(n int) (*Engine, error) {
	if n < 1 {
		return nil, ErrBadN
	}
	return &Engine{n: n, st: rrep.New(n)}, nil
}

// Put 校验后把 (value,ver) 写入 reps 的每个副本；任何校验失败都整体拒绝、不改状态。
func (e *Engine) Put(key, value string, ver int64, reps []int) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ver <= 0 {
		return ErrBadVersion
	}
	if len(reps) == 0 {
		return ErrBadReplicas
	}
	for _, r := range reps {
		if r < 0 || r >= e.n {
			return ErrBadReplicas
		}
	}
	e.st.Put(key, value, ver, reps)
	return nil
}

// Read 判定 winner、回填落后副本，返回 (value, found, repaired)。
func (e *Engine) Read(key string) (string, bool, int, error) {
	if key == "" {
		return "", false, 0, ErrEmptyKey
	}
	w, repaired := e.st.Read(key)
	if !w.Ok {
		return "", false, 0, nil
	}
	return w.Value, true, repaired, nil
}

// Snapshot 返回 key 当前各副本条目（空副本 Ok=false）。
func (e *Engine) Snapshot(key string) []rep.Slot { return e.st.Snapshot(key) }

// SelfCheck 用内置事件序列核验四条不变量，失败返回首个错误。
func SelfCheck() error {
	e, err := New(3)
	if err != nil {
		return err
	}
	steps := []struct {
		put       bool
		val       string
		ver       int64
		reps      []int
		wantVal   string
		wantRep   int
		wantFound bool
	}{
		{put: true, val: "a", ver: 5, reps: []int{0, 1}},
		{wantVal: "a", wantRep: 1, wantFound: true},
		{put: true, val: "b", ver: 7, reps: []int{1, 2}},
		{put: true, val: "c", ver: 7, reps: []int{2}},
		{wantVal: "c", wantRep: 2, wantFound: true},
		{put: true, val: "d", ver: 4, reps: []int{0}},
		{wantVal: "c", wantRep: 1, wantFound: true},
		{wantVal: "c", wantRep: 0, wantFound: true}, // 不变量 2：收敛后 repaired==0
	}
	for i, st := range steps {
		if st.put {
			if err := e.Put("k", st.val, st.ver, st.reps); err != nil {
				return fmt.Errorf("selfcheck step %d put: %w", i, err)
			}
			continue
		}
		want, _ := rep.Winner(e.Snapshot("k")) // 不变量 1：与朴素参照一致
		v, found, rep, err := e.Read("k")
		if err != nil || found != st.wantFound || v != st.wantVal || rep != st.wantRep {
			return fmt.Errorf("selfcheck step %d: got (%q,%v,%d)", i, v, found, rep)
		}
		if found && (v != want.Value || !converged(e.Snapshot("k"), want)) {
			return fmt.Errorf("selfcheck step %d: diverges from naive reference", i)
		}
	}
	for _, op := range []error{ // 不变量 4：失败不留痕
		e.Put("k", "x", 0, []int{0}), e.Put("", "x", 1, []int{0}),
		e.Put("k", "x", 1, nil), e.Put("k", "x", 1, []int{3}),
	} {
		if op == nil {
			return errors.New("selfcheck: invalid op accepted")
		}
	}
	if v, _, _, _ := e.Read("k"); v != "c" {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}

func converged(slots []rep.Slot, w rep.Slot) bool {
	for _, s := range slots {
		if !s.Ok || s.Value != w.Value || s.Ver != w.Ver {
			return false
		}
	}
	return true
}
