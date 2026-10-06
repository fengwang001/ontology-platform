// Package naive 是独立编写的朴素参考模型：切片队列、全量扫描、
// 全局结算时间推进，刻意不与 archive.Service 共享任何业务逻辑代码，
// 仅复用纯枚举类型（Classification / ErrorCode 等）。
package naive

import (
	"sort"

	"ontology/archive"
)

type nLoan struct {
	user       string
	start, due int
	renewals   int
}

type nHold struct {
	user               string
	assigned, deadline int
}

type nVolume struct {
	class       archive.Classification
	status      archive.VolumeStatus
	loan        *nLoan
	hold        *nHold
	queue       []string // 朴素实现：每一步都全量扫描的切片
	sealPending bool
}

type nUser struct {
	maxClass     archive.Classification
	status       archive.UserStatus
	overdueTotal int
	lastReturn   int
	activeLoans  int
}

// Model 是朴素参考模型。
type Model struct {
	cfg     archive.Config
	lastNow int
	volumes map[string]*nVolume
	users   map[string]*nUser
}

// Result 与 archive.Outcome 同构，另附批量失败下标。
type Result struct {
	OK        bool
	Err       archive.ErrorCode
	Reason    string
	FailIndex int
}

func New(cfg archive.Config) *Model {
	return &Model{cfg: cfg, volumes: map[string]*nVolume{}, users: map[string]*nUser{}}
}

func (m *Model) AddUser(id string, c archive.Classification) {
	m.users[id] = &nUser{maxClass: c, status: archive.UserActive}
}

func (m *Model) AddVolume(id string, c archive.Classification) {
	m.volumes[id] = &nVolume{class: c, status: archive.VolInLibrary}
}

func rfail(e archive.ErrorCode, r string) Result {
	return Result{OK: false, Err: e, Reason: r, FailIndex: -1}
}

func rgood(r string) Result {
	return Result{OK: true, Err: archive.OK, Reason: r, FailIndex: -1}
}

// settle 全局结算 now 时刻的世界：解除暂停、处理所有待取到期。
// 朴素实现每次遍历全部卷和全部队列，不做任何惰性优化。
func (m *Model) settle(now int) {
	m.lastNow = now
	uids := make([]string, 0, len(m.users))
	for id := range m.users {
		uids = append(uids, id)
	}
	sort.Strings(uids)
	for _, id := range uids {
		u := m.users[id]
		if u.status == archive.UserSuspended && u.activeLoans == 0 &&
			now-u.lastReturn >= m.cfg.CooldownDays {
			u.status = archive.UserActive
			u.overdueTotal = 0
		}
	}
	vids := make([]string, 0, len(m.volumes))
	for id := range m.volumes {
		vids = append(vids, id)
	}
	sort.Strings(vids)
	for _, id := range vids {
		v := m.volumes[id]
		if v.hold != nil && now > v.hold.deadline {
			m.abandonHold(v, now)
		}
	}
}

// abandonHold 摘除当前持卷人并从头全量扫描队列；可级联。
func (m *Model) abandonHold(v *nVolume, now int) {
	v.hold = nil
	if v.sealPending {
		v.sealPending = false
		v.status = archive.VolSealed
		v.queue = nil
		return
	}
	m.assign(v, now)
}

func removeFirst(q []string, user string) []string {
	out := make([]string, 0, len(q))
	removed := false
	for _, x := range q {
		if !removed && x == user {
			removed = true
			continue
		}
		out = append(out, x)
	}
	return out
}

func contains(q []string, x string) bool {
	for _, s := range q {
		if s == x {
			return true
		}
	}
	return false
}

// assign 从切片头部全量扫描：资格不足/暂停者跳过但保留位置。
func (m *Model) assign(v *nVolume, now int) {
	for _, uid := range v.queue {
		u := m.users[uid]
		if u != nil && u.status == archive.UserActive && u.maxClass >= v.class {
			v.hold = &nHold{user: uid, assigned: now, deadline: now + m.cfg.PickupDeadlineDays}
			v.status = archive.VolLent
			v.queue = removeFirst(v.queue, uid) // 分配即出队
			return
		}
	}
	v.status = archive.VolInLibrary
}
