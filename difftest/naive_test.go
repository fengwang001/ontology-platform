// 朴素模拟：按规格逐条直写，不追求效率，作为对拍基准。
package difftest

import (
	"fmt"

	"ontology/erase"
)

type nEras struct {
	status   erase.Status
	deadline int64
	subject  int64
	acks     map[int]int64
}

type nBackup struct {
	sys int
	tb  int64
}

type naive struct {
	S         int
	T         int64
	maxNow    int64
	eras      []*nEras
	open      map[int64]int // subject -> 擦除单编号（仅 Active/Deferred）
	held      map[int64]bool
	backups   []nBackup
	restoring map[int]bool
	todo      map[int]map[int]bool
}

func newNaive(s int, limit int64) *naive {
	return &naive{
		S:         s,
		T:         limit,
		open:      map[int64]int{},
		held:      map[int64]bool{},
		restoring: map[int]bool{},
		todo:      map[int]map[int]bool{},
	}
}

func (n *naive) clock(now int64) (error, string) {
	if now < n.maxNow {
		return erase.ErrClock, fmt.Sprintf("时钟回退 now=%d < maxNow=%d", now, n.maxNow)
	}
	return nil, ""
}

func (n *naive) request(role int, sub, now int64) (int, error, string) {
	if sub < 1 || sub > erase.MaxSubject || !erase.ValidNow(now) {
		return 0, erase.ErrParam, "参数越界(subject/now)"
	}
	if role != erase.RolePrivacy {
		return 0, erase.ErrPerm, "角色非隐私官"
	}
	if err, why := n.clock(now); err != nil {
		return 0, err, why
	}
	if _, ok := n.open[sub]; ok {
		return 0, erase.ErrDuplicate, "主体已有未完成擦除单"
	}
	e := &nEras{subject: sub, acks: map[int]int64{}}
	why := "新建 Active"
	if n.held[sub] {
		e.status = erase.Deferred
		why = "主体被保留，新建 Deferred（无时限）"
	} else {
		e.status = erase.Active
		e.deadline = now + n.T
	}
	n.eras = append(n.eras, e)
	n.open[sub] = len(n.eras)
	n.maxNow = now
	return len(n.eras), nil, why
}

func (n *naive) ack(role, e, s int, now int64) (error, string) {
	if s < 1 || s > n.S || !erase.ValidNow(now) {
		return erase.ErrParam, "参数越界(s/now)"
	}
	if e < 1 || e > len(n.eras) {
		return erase.ErrParam, "e 越界"
	}
	if role != erase.RoleOps {
		return erase.ErrPerm, "角色非系统运维"
	}
	if err, why := n.clock(now); err != nil {
		return err, why
	}
	er := n.eras[e-1]
	if er.status != erase.Active {
		return erase.ErrState, "擦除单非 Active"
	}
	if _, ok := er.acks[s]; ok {
		return erase.ErrState, "系统已确认"
	}
	er.acks[s] = now
	n.maxNow = now
	if len(er.acks) == n.S {
		er.status = erase.Done
		delete(n.open, er.subject)
		return nil, "全部确认，擦除单 Done"
	}
	return nil, "记录确认"
}

func (n *naive) hold(role int, sub, now int64) (error, string) {
	if sub < 1 || sub > erase.MaxSubject || !erase.ValidNow(now) {
		return erase.ErrParam, "参数越界(subject/now)"
	}
	if role != erase.RoleLegal {
		return erase.ErrPerm, "角色非法务"
	}
	if err, why := n.clock(now); err != nil {
		return err, why
	}
	if n.held[sub] {
		return erase.ErrAlready, "已保留"
	}
	n.held[sub] = true
	n.maxNow = now
	return nil, "置保留（不撤回已 Active 的擦除）"
}

func (n *naive) release(role int, sub, now int64) (error, string) {
	if sub < 1 || sub > erase.MaxSubject || !erase.ValidNow(now) {
		return erase.ErrParam, "参数越界(subject/now)"
	}
	if role != erase.RoleLegal {
		return erase.ErrPerm, "角色非法务"
	}
	if err, why := n.clock(now); err != nil {
		return err, why
	}
	if !n.held[sub] {
		return erase.ErrNotHeld, "未保留"
	}
	delete(n.held, sub)
	n.maxNow = now
	if id, ok := n.open[sub]; ok && n.eras[id-1].status == erase.Deferred {
		n.eras[id-1].status = erase.Active
		n.eras[id-1].deadline = now + n.T
		return nil, "解除保留，Deferred 转 Active，时限自解除起算"
	}
	return nil, "解除保留"
}

func (n *naive) backup(role, s int, now int64) (int, error, string) {
	if s < 1 || s > n.S || !erase.ValidNow(now) {
		return 0, erase.ErrParam, "参数越界(s/now)"
	}
	if role != erase.RoleOps {
		return 0, erase.ErrPerm, "角色非系统运维"
	}
	if err, why := n.clock(now); err != nil {
		return 0, err, why
	}
	n.backups = append(n.backups, nBackup{sys: s, tb: now})
	n.maxNow = now
	return len(n.backups), nil, "新建备份点"
}

func (n *naive) restore(role, s, b int, now int64) ([]int, error, string) {
	if s < 1 || s > n.S || !erase.ValidNow(now) {
		return nil, erase.ErrParam, "参数越界(s/now)"
	}
	if b < 1 || b > len(n.backups) {
		return nil, erase.ErrParam, "b 越界"
	}
	if role != erase.RoleOps {
		return nil, erase.ErrPerm, "角色非系统运维"
	}
	if err, why := n.clock(now); err != nil {
		return nil, err, why
	}
	bk := n.backups[b-1]
	if bk.sys != s {
		return nil, erase.ErrNoBackup, "备份点不属于该系统"
	}
	if n.restoring[s] {
		return nil, erase.ErrState, "系统非 Ready"
	}
	var L []int
	for i, e := range n.eras {
		if a, ok := e.acks[s]; ok && a > bk.tb {
			L = append(L, i+1)
		}
	}
	n.maxNow = now
	if len(L) == 0 {
		return nil, nil, "重放集为空，保持 Ready"
	}
	n.restoring[s] = true
	n.todo[s] = map[int]bool{}
	for _, id := range L {
		n.todo[s][id] = true
	}
	return L, nil, "重放集非空，进入 Restoring"
}

func (n *naive) reapply(role, s, e int, now int64) (error, string) {
	if s < 1 || s > n.S || !erase.ValidNow(now) {
		return erase.ErrParam, "参数越界(s/now)"
	}
	if e < 1 || e > len(n.eras) {
		return erase.ErrParam, "e 越界"
	}
	if role != erase.RoleOps {
		return erase.ErrPerm, "角色非系统运维"
	}
	if err, why := n.clock(now); err != nil {
		return err, why
	}
	if !n.restoring[s] || !n.todo[s][e] {
		return erase.ErrState, "非 Restoring 或不在待办"
	}
	delete(n.todo[s], e)
	n.maxNow = now
	if len(n.todo[s]) == 0 {
		n.restoring[s] = false
		delete(n.todo, s)
		return nil, "待办清空，回到 Ready"
	}
	return nil, "销账一项"
}

func (n *naive) read(s int) (error, string) {
	if s < 1 || s > n.S {
		return erase.ErrParam, "s 越界"
	}
	if n.restoring[s] {
		return erase.ErrRestoring, "Restoring 阻塞读"
	}
	return nil, "Ready 可读"
}

func (n *naive) overdue(now int64) ([]erase.OverdueEntry, string) {
	var res []erase.OverdueEntry
	for i, e := range n.eras {
		if e.status != erase.Active || e.deadline > now {
			continue
		}
		var pend []int
		for s := 1; s <= n.S; s++ {
			if _, ok := e.acks[s]; !ok {
				pend = append(pend, s)
			}
		}
		res = append(res, erase.OverdueEntry{ID: i + 1, Pending: pend})
	}
	return res, "Active 且 deadline<=now（只读，不验时钟）"
}
