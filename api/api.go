// Package api 是对外接口层。依赖 rrep。
package api

import (
	"errors"
	"fmt"

	"ontology/rep"
	"ontology/rrep"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrBadN     = errors.New("api: 副本数必须 >= 1")
	ErrBadVer   = errors.New("api: 版本号必须 > 0")
	ErrEmptyKey = errors.New("api: 键不能为空")
	ErrBadReps  = errors.New("api: 副本集为空或含越界下标")
)

// Engine 是多副本反熵读修复引擎。
type Engine struct {
	st *rrep.Store
	n  int
}

// New 创建 n 副本引擎；n < 1 时返回 ErrBadN。
func New(n int) (*Engine, error) {
	if n < 1 {
		return nil, ErrBadN
	}
	return &Engine{st: rrep.NewStore(n), n: n}, nil
}

func (e *Engine) checkPut(key string, ver int64, reps []int) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ver <= 0 {
		return ErrBadVer
	}
	if len(reps) == 0 {
		return ErrBadReps
	}
	for _, r := range reps {
		if r < 0 || r >= e.n {
			return ErrBadReps
		}
	}
	return nil
}

// Put 把 (value, ver) 写到 reps 里的每个副本；参数非法时整体失败且不改状态。
func (e *Engine) Put(key, value string, ver int64, reps []int) error {
	if err := e.checkPut(key, ver, reps); err != nil {
		return err
	}
	e.st.Put(key, value, ver, reps)
	return nil
}

// Read 判定 winner、回填落后副本，返回 (value, found, repaired, err)。
func (e *Engine) Read(key string) (string, bool, int, error) {
	if key == "" {
		return "", false, 0, ErrEmptyKey
	}
	v, repaired, found := e.st.Read(key)
	return v, found, repaired, nil
}

// Snapshot 返回 key 各副本当前条目。
func (e *Engine) Snapshot(key string) []rep.Entry {
	return e.st.Snapshot(key)
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	// 不变量 1+2+3：固定写入序列后，Read 结果等于朴素扫描，且收敛、只升不降。
	eng, _ := New(5)
	puts := []struct {
		v    string
		ver  int64
		reps []int
	}{
		{"a", 5, []int{0, 1}}, {"b", 7, []int{1, 2}}, {"c", 7, []int{2}},
		{"d", 4, []int{0}}, {"e", 9, []int{3}},
	}
	for _, p := range puts {
		if err := eng.Put("k", p.v, p.ver, p.reps); err != nil {
			return err
		}
	}
	before := eng.Snapshot("k")
	w, _, ok := rep.Winner(before) // 朴素参照：直接扫全部副本
	if !ok {
		return errors.New("selfcheck: 应有 winner")
	}
	v, found, _, err := eng.Read("k")
	if err != nil || !found || v != w.Value {
		return fmt.Errorf("selfcheck: 不变量1 违反 got=%q want=%q", v, w.Value)
	}
	after := eng.Snapshot("k")
	for i := range after {
		if after[i] != w {
			return fmt.Errorf("selfcheck: 不变量1 副本%d 未收敛到 winner", i)
		}
		if !before[i].Empty && after[i].Ver < before[i].Ver {
			return fmt.Errorf("selfcheck: 不变量3 副本%d 版本降低", i)
		}
	}
	if _, _, r2, _ := eng.Read("k"); r2 != 0 {
		return fmt.Errorf("selfcheck: 不变量2 二次 Read repaired=%d", r2)
	}
	// 不变量 4：被拒操作不留痕。
	bad := []error{
		eng.Put("", "x", 1, []int{0}), eng.Put("k", "x", 0, []int{0}),
		eng.Put("k", "x", -3, []int{0}), eng.Put("k", "x", 1, nil),
		eng.Put("k", "x", 1, []int{5}), eng.Put("k", "x", 1, []int{-1}),
	}
	for i, err := range bad {
		if err == nil {
			return fmt.Errorf("selfcheck: 不变量4 第%d个非法操作未被拒", i)
		}
	}
	for i := range after {
		if eng.Snapshot("k")[i] != after[i] {
			return fmt.Errorf("selfcheck: 不变量4 副本%d 状态被改", i)
		}
	}
	if _, err := New(0); err != ErrBadN {
		return errors.New("selfcheck: New(0) 未返回 ErrBadN")
	}
	return nil
}
