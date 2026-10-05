// Package notify 实现危急事件的授权、通知、回读确认、处置与逾期清单。
package notify

import (
	"ontology/alert"
)

// Role 是用户在某病区的资格。
type Role int

const (
	Nurse Role = iota + 1
	Doctor
)

// Manager 在 alert.Center 之上叠加授权表，提供完整闭环操作。
// 所有方法可并发调用，效果等价于某个串行顺序。
type Manager struct {
	*alert.Center
	grants map[string]map[string]Role
}

// New 创建一个闭环管理器，T1/T2/T3 为三档严重度各自的闭环时限（分钟）。
func New(t1, t2, t3 int64) (*Manager, error) {
	c, err := alert.New(t1, t2, t3)
	if err != nil {
		return nil, err
	}
	return &Manager{Center: c, grants: make(map[string]map[string]Role)}, nil
}

// Grant 授予 user 在某病区的护士或医生资格。
func (m *Manager) Grant(user, ward string, role Role) error {
	if user == "" || ward == "" || (role != Nurse && role != Doctor) {
		return alert.ErrInvalidParam
	}
	m.Lock()
	defer m.Unlock()
	if m.grants[ward] == nil {
		m.grants[ward] = make(map[string]Role)
	}
	m.grants[ward][user] = role
	return nil
}

// qualified 报告 user 是否持有 ward 的任一给定资格，调用方须持锁。
func (m *Manager) qualified(user, ward string, roles ...Role) bool {
	r, ok := m.grants[ward][user]
	if !ok {
		return false
	}
	for _, want := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// Notify 对待通知事件发起通知，receiver 须具有事件患者当前病区的
// 护士或医生资格；成功后事件转为待回读并记下接收人。
func (m *Manager) Notify(now int64, eventID int, tech, receiver string) error {
	if tech == "" || receiver == "" || now < 0 || now > alert.MaxClock {
		return alert.ErrInvalidParam
	}
	m.Lock()
	defer m.Unlock()
	if err := m.CheckClockLocked(now); err != nil {
		return err
	}
	ev := m.EventLocked(eventID)
	if ev == nil {
		return alert.ErrNotFound
	}
	ward, _ := m.WardLocked(ev.Patient)
	if !m.qualified(receiver, ward, Nurse, Doctor) {
		return alert.ErrNoQual
	}
	if ev.Status != alert.PendingNotify {
		return alert.ErrBadState
	}
	m.CommitLocked(now)
	ev.Tech = tech
	ev.Receiver = receiver
	ev.Status = alert.PendingReadBack
	return nil
}

// ReadBack 回读确认。receiver 须为本次通知的接收人（否则报无资格），
// 事件须为待回读。v 等于代表值则转为待处置；不等则返回 mismatch=true
// （操作仍被接受），不符累计达 2 次时事件退回待通知并清零计数。
func (m *Manager) ReadBack(now int64, eventID int, receiver string, v int64) (mismatch bool, err error) {
	if receiver == "" || now < 0 || now > alert.MaxClock || v < -alert.MaxValue || v > alert.MaxValue {
		return false, alert.ErrInvalidParam
	}
	m.Lock()
	defer m.Unlock()
	if err := m.CheckClockLocked(now); err != nil {
		return false, err
	}
	ev := m.EventLocked(eventID)
	if ev == nil {
		return false, alert.ErrNotFound
	}
	if ev.Receiver != "" && receiver != ev.Receiver {
		return false, alert.ErrNoQual
	}
	if ev.Status != alert.PendingReadBack {
		return false, alert.ErrBadState
	}
	m.CommitLocked(now)
	if v == ev.Rep {
		ev.Status = alert.PendingAct
		return false, nil
	}
	ev.Mismatch++
	if ev.Mismatch >= 2 {
		ev.Status = alert.PendingNotify
		ev.Mismatch = 0
		ev.Tech = ""
		ev.Receiver = ""
	}
	return true, nil
}

// Act 由具有患者当前病区医生资格的人处置，成功即闭环；
// 返回的 late 为事件闭环时是否已逾期（逾期标记粘滞）。
func (m *Manager) Act(now int64, eventID int, doctor string) (late bool, err error) {
	if doctor == "" || now < 0 || now > alert.MaxClock {
		return false, alert.ErrInvalidParam
	}
	m.Lock()
	defer m.Unlock()
	if err := m.CheckClockLocked(now); err != nil {
		return false, err
	}
	ev := m.EventLocked(eventID)
	if ev == nil {
		return false, alert.ErrNotFound
	}
	ward, _ := m.WardLocked(ev.Patient)
	if !m.qualified(doctor, ward, Doctor) {
		return false, alert.ErrNoQual
	}
	if ev.Status != alert.PendingAct {
		return false, alert.ErrBadState
	}
	m.CommitLocked(now)
	ev.Status = alert.Closed
	ev.ClosedLate = ev.Late
	m.CloseLocked(ev)
	return ev.ClosedLate, nil
}
