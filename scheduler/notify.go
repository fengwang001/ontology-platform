package scheduler

import "sort"

// reconcileSlotNotifs 使某时段的通知名单与「被选组的当前非免控成员」一致：
// 新进入名单的生成通知，退出名单的撤回（已确认的撤回不计考核）。
// 开销只与被选组内用户数及时段现有通知数相关，与区域总用户数无关。
func (s *Scheduler) reconcileSlotNotifs(slot int64, groups []string) {
	desired := map[string]string{} // 用户 -> 组
	for _, gid := range groups {
		g := s.groups[gid]
		for uid := range g.users {
			s.stats.UserVisits++
			if s.users[uid].cat != CatExempt {
				desired[uid] = gid
			}
		}
	}
	slotNotifs := s.notifs[slot]
	for uid, n := range slotNotifs {
		if _, ok := desired[uid]; !ok && (n.State == NotifPending || n.State == NotifConfirmed) {
			n.State = NotifWithdrawn
		}
	}
	uids := make([]string, 0, len(desired))
	for uid := range desired {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		n, ok := slotNotifs[uid]
		if ok && n.State != NotifWithdrawn {
			continue // 已存在且有效（待确认/已确认/已考核），保持不变
		}
		u := s.users[uid]
		nn := &Notification{
			Slot:  slot,
			User:  uid,
			Group: desired[uid],
			State: NotifPending,
		}
		if u.cat == CatGuaranteed {
			nn.HasBasePower = true
			nn.BasePower = u.basePower
		}
		if slotNotifs == nil {
			slotNotifs = map[string]*Notification{}
			s.notifs[slot] = slotNotifs
		}
		slotNotifs[uid] = nn
		if slot <= s.now {
			// 时段已开始才生成的通知，确认截止已过，立即记考核。
			s.assess(nn)
		}
	}
}

// assessSlot 在时段开始时对该时段全部未确认通知记考核。
func (s *Scheduler) assessSlot(slot int64) {
	m := s.notifs[slot]
	uids := make([]string, 0, len(m))
	for uid := range m {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		if n := m[uid]; n.State == NotifPending {
			s.assess(n)
		}
	}
}

func (s *Scheduler) assess(n *Notification) {
	n.State = NotifAssessed
	s.assessments = append(s.assessments, Assessment{Slot: n.Slot, User: n.User, Group: n.Group})
}

// Confirm 用户在时段开始前确认通知。拒绝次序：
// 用户不存在（参数非法） > 已过截止 > 通知不存在。
func (s *Scheduler) Confirm(userID string, slot int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return paramErr("用户不存在: " + userID)
	}
	if s.now >= slot {
		return deadlineErr("确认须在时段开始前到达")
	}
	n, ok := s.notifs[slot][userID]
	if !ok || n.State == NotifWithdrawn {
		return notifErr("该用户在该时段无有效通知")
	}
	if n.State == NotifPending {
		n.State = NotifConfirmed
	}
	// 已确认的重复确认幂等成功，不改变状态。
	return nil
}
