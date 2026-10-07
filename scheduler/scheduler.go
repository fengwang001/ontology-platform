package scheduler

import (
	"sort"
	"sync"
)

// Scheduler 是有序用电错避峰轮换调度器。
// 所有公开方法持有同一把互斥锁，并发调用等价于某个串行执行顺序。
type Scheduler struct {
	slotLen int64 // 轮换时段长度（固定）
	now     int64 // 当前时刻，只能前进
	mu      sync.Mutex

	regions map[string]*region
	groups  map[string]*group
	users   map[string]*user
	instrs  map[string]*instruction

	accum map[string]int64 // 组 -> 累计被限时长（groupHeap 的权威存储）
	gh    *groupHeap

	events map[int64][]instrEvent // 挂在未来时段边界上的指令事件
	live   map[string]int         // 当前生效的指令 -> 等级
	liveH  *levelHeap
	endH   *endHeap

	curSlotStart int64    // 当前时段起点
	curSelection []string // 当前时段被限的组

	forecast    map[int64]*forecastSlot // 未来时段的预选结果
	notifs      notifIndex              // 通知：时段起点 -> 用户 -> 通知
	assessments []Assessment            // 「未确认」考核记录（追加有序）
	history     []SlotRecord            // 已结算时段序列（选组序列）
	stats       Stats
}

// NewScheduler 创建调度器，slotLen 为固定的轮换时段长度。
func NewScheduler(slotLen int64) *Scheduler {
	if slotLen <= 0 {
		panic("scheduler: slotLen must be positive")
	}
	s := &Scheduler{
		slotLen:  slotLen,
		regions:  map[string]*region{},
		groups:   map[string]*group{},
		users:    map[string]*user{},
		instrs:   map[string]*instruction{},
		accum:    map[string]int64{},
		events:   map[int64][]instrEvent{},
		live:     map[string]int{},
		forecast: map[int64]*forecastSlot{},
		notifs:   notifIndex{},
	}
	s.gh = newGroupHeap(s.accum, &s.stats.GroupHeapOps)
	s.liveH = newLevelHeap(s.live, &s.stats.LevelHeapOps)
	s.endH = &endHeap{instrs: s.instrs}
	return s
}

// nextSlotStart 返回当前时刻所在时段的下一时段起点。
func (s *Scheduler) nextSlotStart() int64 {
	return (s.now/s.slotLen + 1) * s.slotLen
}

// effBoundary 返回操作生效的时段边界：恰在边界上即当前边界，否则下一边界。
func (s *Scheduler) effBoundary() int64 {
	if s.now%s.slotLen == 0 {
		return s.now
	}
	return s.nextSlotStart()
}

// AddRegion 新增区域。
func (s *Scheduler) AddRegion(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.regions[id]; dup {
		return paramErr("区域已存在: " + id)
	}
	s.regions[id] = &region{id: id, groups: map[string]bool{}}
	return nil
}

// AddGroup 在区域下新增轮换组，累计被限时长从零开始。
func (s *Scheduler) AddGroup(regionID, groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.regions[regionID]
	if !ok {
		return paramErr("区域不存在: " + regionID)
	}
	if _, dup := s.groups[groupID]; dup {
		return paramErr("组已存在: " + groupID)
	}
	s.groups[groupID] = &group{id: groupID, region: regionID, users: map[string]bool{}}
	r.groups[groupID] = true
	s.accum[groupID] = 0
	s.gh.insert(groupID)
	// 新组以累计 0 参与排序，可能改变未来时段的预选，须重算预测。
	s.recomputeForecast()
	return nil
}

// AddUser 新增用户并编入一个组；类别为免控/保底/普通，保底须给保底功率。
func (s *Scheduler) AddUser(id, groupID string, cat Category, basePower int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.users[id]; dup {
		return paramErr("用户已存在: " + id)
	}
	g, ok := s.groups[groupID]
	if !ok {
		return paramErr("组不存在: " + groupID)
	}
	if !validCat(cat) || basePower < 0 {
		return paramErr("类别或保底功率非法")
	}
	s.users[id] = &user{id: id, group: groupID, cat: cat, basePower: basePower}
	g.users[id] = true
	return nil
}

func validCat(c Category) bool { return c >= CatExempt && c <= CatNormal }

// groupBusyLocked 判断组在当前时段是否处于被限状态。
func (s *Scheduler) groupBusyLocked(groupID string) bool {
	for _, g := range s.curSelection {
		if g == groupID {
			return true
		}
	}
	return false
}

// MoveGroup 把用户换到另一个组；所在组当前处于被限时段时报「时段进行中」。
func (s *Scheduler) MoveGroup(userID, groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return paramErr("用户不存在: " + userID)
	}
	g, ok := s.groups[groupID]
	if !ok {
		return paramErr("组不存在: " + groupID)
	}
	if s.groupBusyLocked(u.group) {
		return busyErr("用户所在组当前处于被限时段: " + u.group)
	}
	delete(s.groups[u.group].users, userID)
	u.group = groupID
	g.users[userID] = true
	return nil
}

// SetCategory 修改用户类别与保底功率；所在组被限时报「时段进行中」。
func (s *Scheduler) SetCategory(userID string, cat Category, basePower int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return paramErr("用户不存在: " + userID)
	}
	if !validCat(cat) || basePower < 0 {
		return paramErr("类别或保底功率非法")
	}
	if s.groupBusyLocked(u.group) {
		return busyErr("用户所在组当前处于被限时段: " + u.group)
	}
	u.cat = cat
	u.basePower = basePower
	return nil
}

// AdvanceTo 把当前时刻推进到 t；跨越时段边界时按边界先后依次结算。
func (s *Scheduler) AdvanceTo(t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t < s.now {
		return clockErr("目标时刻早于当前时刻")
	}
	for b := s.nextSlotStart(); b <= t; b += s.slotLen {
		s.settleBoundary(b)
	}
	s.now = t
	return nil
}

// settleBoundary 结算一个时段边界：结束旧时段的累计、按当时有效等级
// 选新时段的组、生成/核对通知并对未确认者记考核。
func (s *Scheduler) settleBoundary(b int64) {
	// 1. 结束旧时段：被限组累计时长加上时段长度。
	for _, g := range s.curSelection {
		s.gh.add(g, s.slotLen)
	}
	// 2. 应用该边界上的指令事件，得到有效等级。
	s.applyEvents(b)
	k := s.liveH.top()
	// 3. 选组：累计最短者优先，相同则组编号小者优先。
	sel := s.gh.popK(k)
	s.curSelection = sel
	s.curSlotStart = b
	// 4. 通知：按当前成员核对名单，再对未确认者记考核。
	s.reconcileSlotNotifs(b, sel)
	s.assessSlot(b)
	delete(s.forecast, b)
	s.history = append(s.history, SlotRecord{Start: b, Level: k, Groups: sel})
}

// resettleCurrent 处理「恰在时段边界上」的改级/取消：当前时段的
// 有效等级立即变化，按当前累计重新选组并核对通知（累计尚未结算，可安全重选）。
func (s *Scheduler) resettleCurrent() {
	k := s.liveH.top()
	sel := s.gh.popK(k)
	s.curSelection = sel
	if n := len(s.history); n > 0 && s.history[n-1].Start == s.curSlotStart {
		s.history[n-1].Level = k
		s.history[n-1].Groups = sel
	}
	s.reconcileSlotNotifs(s.curSlotStart, sel)
}

// ---- 查询接口（测试与对外用） ----

// Now 返回当前时刻。
func (s *Scheduler) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Accum 返回组的累计被限时长。
func (s *Scheduler) Accum(groupID string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.accum[groupID]
	return v, ok
}

// CurrentSelection 返回当前时段被限的组（副本）。
func (s *Scheduler) CurrentSelection() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.curSelection...)
}

// History 返回已结算时段序列（选组序列）。
func (s *Scheduler) History() []SlotRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SlotRecord(nil), s.history...)
}

// Notifications 返回某时段的全部通知，按用户排序。
func (s *Scheduler) Notifications(slot int64) []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedNotifs(s.notifs[slot])
}

// AllNotifications 返回全部通知，按 (时段, 用户) 排序。
func (s *Scheduler) AllNotifications() []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	slots := make([]int64, 0, len(s.notifs))
	for slot := range s.notifs {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	var out []Notification
	for _, slot := range slots {
		out = append(out, sortedNotifs(s.notifs[slot])...)
	}
	return out
}

func sortedNotifs(m map[string]*Notification) []Notification {
	uids := make([]string, 0, len(m))
	for uid := range m {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	out := make([]Notification, 0, len(uids))
	for _, uid := range uids {
		out = append(out, *m[uid])
	}
	return out
}

// Assessments 返回「未确认」考核记录（追加有序）。
func (s *Scheduler) Assessments() []Assessment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Assessment(nil), s.assessments...)
}

// Stats 返回内部开销计数器。
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}
