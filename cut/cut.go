// Package cut 实现双写迁移期间的成员变更：加入、退出与全局高水位接续。
package cut

import (
	"errors"

	"ontology/stride"
)

var (
	ErrParam      = errors.New("cut: 参数越界")
	ErrPermission = errors.New("cut: 权限不足，需要 role=2")
	ErrState      = errors.New("cut: 当前成员状态不允许该操作")
	ErrLast       = errors.New("cut: 不能退出最后一个活跃系统")
)

const (
	RoleMin   = 1
	RoleAdmin = 2
)

// State 是成员变更作用的共享状态，由 seq.Allocator 持有并在锁内传入。
// Next 与 Active 按下标 0..K-1 存放系统 1..K，系统 s 的同余类 c_s = s-1。
type State struct {
	M           int64
	Next        []int64
	Active      []bool
	ActiveCount int
	J           int64
}

// HighWater 返回全部系统（含不活跃者）next 的最大值。
func (st *State) HighWater() int64 {
	h := int64(0)
	for _, n := range st.Next {
		if n > h {
			h = n
		}
	}
	return h
}

func (st *State) checkTarget(s int) error {
	if s < 1 || s > len(st.Next) {
		return ErrParam
	}
	return nil
}

func checkRole(role int) error {
	if role < RoleMin || role > RoleAdmin {
		return ErrParam
	}
	if role != RoleAdmin {
		return ErrPermission
	}
	return nil
}

func (st *State) onlyActive() int {
	for i := range st.Active {
		if st.Active[i] {
			return i
		}
	}
	return -1
}

// Join 让系统 s（1..K）加入。校验次序：参数 → 权限 → 状态。
// H 在对齐既有活跃系统之前取定；被拒时不改变任何状态。
func (st *State) Join(role, s int) error {
	if err := st.checkTarget(s); err != nil {
		return err
	}
	if err := checkRole(role); err != nil {
		return err
	}
	i := s - 1
	if st.Active[i] {
		return ErrState
	}
	h := st.HighWater()
	if st.ActiveCount == 1 {
		t := st.onlyActive()
		aligned := stride.AlignUp(st.Next[t], int64(t), st.M)
		st.J += aligned - st.Next[t]
		st.Next[t] = aligned
	}
	base := st.Next[i]
	if base < h {
		base = h
	}
	aligned := stride.AlignUp(base, int64(i), st.M)
	st.J += aligned - st.Next[i]
	st.Next[i] = aligned
	st.Active[i] = true
	st.ActiveCount++
	return nil
}

// Leave 让系统 s（1..K）退出，其 next 原样保留。
// 若退出后只剩一个活跃系统 r，则 r 转入步长 1，
// next_r 接续含刚离开者在内的全局高水位。被拒时不改变任何状态。
func (st *State) Leave(role, s int) error {
	if err := st.checkTarget(s); err != nil {
		return err
	}
	if err := checkRole(role); err != nil {
		return err
	}
	i := s - 1
	if !st.Active[i] {
		return ErrState
	}
	if st.ActiveCount == 1 {
		return ErrLast
	}
	st.Active[i] = false
	st.ActiveCount--
	if st.ActiveCount == 1 {
		r := st.onlyActive()
		h := st.HighWater()
		st.J += h - st.Next[r]
		st.Next[r] = h
	}
	return nil
}
