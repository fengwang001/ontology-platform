// Package cut 实现迁移并行期系统的加入、退出与全局高水位接续。
package cut

import (
	"ontology/seq"
	"ontology/stride"
)

func checkArgs(st *seq.Store, role, sys int) error {
	if sys < 1 || sys > st.K() {
		return seq.ErrParam
	}
	if role < seq.MinRole || role > seq.MaxRole {
		return seq.ErrParam
	}
	if role != seq.RoleAdmin {
		return seq.ErrPermission
	}
	return nil
}

// Join 把不活跃系统 sys 加入签发集合。若加入前只有一个活跃系统 t，
// 先把 t 对齐到步长 m 的同余类；再把 next_sys 置为
// alignUp(max(next_sys, H), c_sys)，其中 H 是对齐 t 之前取定的
// 全局高水位（含不活跃系统）。各 next 增量计入 J。
func Join(st *seq.Store, role, sys int) error {
	if err := checkArgs(st, role, sys); err != nil {
		return err
	}
	return st.Locked(func(v *seq.View) error {
		if v.Active(sys) {
			return seq.ErrState
		}
		h := v.HighWater()
		if v.ActiveCount() == 1 {
			t := v.SoleActive()
			v.SetNext(t, stride.AlignUp(v.Next(t), stride.Class(t), st.M()))
		}
		base := v.Next(sys)
		if h > base {
			base = h
		}
		v.SetNext(sys, stride.AlignUp(base, stride.Class(sys), st.M()))
		v.SetActive(sys, true)
		return nil
	})
}

// Leave 把活跃系统 sys 移出签发集合，next_sys 原样保留。若此后只剩
// 一个活跃系统 r，则 r 转入步长 1，next_r 置为 max(next_r, H')，
// H' 是此时全部系统（含刚离开的 sys）next 的最大值，增量计入 J。
func Leave(st *seq.Store, role, sys int) error {
	if err := checkArgs(st, role, sys); err != nil {
		return err
	}
	return st.Locked(func(v *seq.View) error {
		if !v.Active(sys) {
			return seq.ErrState
		}
		if v.ActiveCount() == 1 {
			return seq.ErrLast
		}
		v.SetActive(sys, false)
		if v.ActiveCount() == 1 {
			r := v.SoleActive()
			base := v.Next(r)
			if h := v.HighWater(); h > base {
				base = h
			}
			v.SetNext(r, base)
		}
		return nil
	})
}
