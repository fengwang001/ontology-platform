// 本文件包含独立实现的朴素模型：不用事件堆与组堆，每个时段都按规则
// 线性扫描全部指令、排序全部组来求有效等级与选组，以此与优化实现对照。
package scheduler

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

type mInstr struct {
	start, end int64
	changes    []levelChange
	cancelled  bool
	cancelEff  int64
	region     string
}

type model struct {
	slotLen  int64
	now      int64
	regions  map[string]map[string]bool
	groups   map[string]map[string]bool
	users    map[string]*user
	accum    map[string]int64
	instrs   map[string]*mInstr
	curStart int64
	curSel   []string
	forecast map[int64][]string
	notifs   map[int64]map[string]*Notification
	assess   []Assessment
	history  []SlotRecord
}

func newModel(slotLen int64) *model {
	return &model{
		slotLen:  slotLen,
		regions:  map[string]map[string]bool{},
		groups:   map[string]map[string]bool{},
		users:    map[string]*user{},
		accum:    map[string]int64{},
		instrs:   map[string]*mInstr{},
		forecast: map[int64][]string{},
		notifs:   map[int64]map[string]*Notification{},
	}
}

func (m *model) nextSlotStart() int64 { return (m.now/m.slotLen + 1) * m.slotLen }

func (m *model) effBoundary() int64 {
	if m.now%m.slotLen == 0 {
		return m.now
	}
	return m.nextSlotStart()
}

func (m *model) addRegion(id string) { m.regions[id] = map[string]bool{} }

func (m *model) addGroup(regionID, groupID string) {
	m.regions[regionID][groupID] = true
	m.groups[groupID] = map[string]bool{}
	m.accum[groupID] = 0
}

func (m *model) addUser(id, groupID string, cat Category, bp int64) {
	m.users[id] = &user{id: id, group: groupID, cat: cat, basePower: bp}
	m.groups[groupID][id] = true
}

// levelAtSlot 朴素求某时段有效等级：扫描全部指令取最大。
func (m *model) levelAtSlot(slot int64) int {
	lvl := 0
	for _, in := range m.instrs {
		if slot < in.start || slot >= in.end {
			continue
		}
		if in.cancelled && slot >= in.cancelEff {
			continue
		}
		l := in.changes[0].level
		for _, c := range in.changes {
			if c.eff <= slot {
				l = c.level
			} else {
				break
			}
		}
		if l > lvl {
			lvl = l
		}
	}
	return lvl
}

// selectFrom 朴素选组：全部组按 (累计, 编号) 排序取前 k 个。
func selectFrom(acc map[string]int64, k int) []string {
	if k <= 0 {
		return nil
	}
	ids := make([]string, 0, len(acc))
	for id := range acc {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if acc[ids[i]] != acc[ids[j]] {
			return acc[ids[i]] < acc[ids[j]]
		}
		return ids[i] < ids[j]
	})
	return ids[:k]
}

func (m *model) reconcile(slot int64, groups []string) {
	desired := map[string]string{}
	for _, gid := range groups {
		for uid := range m.groups[gid] {
			if m.users[uid].cat != CatExempt {
				desired[uid] = gid
			}
		}
	}
	nm := m.notifs[slot]
	for uid, n := range nm {
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
		n, ok := nm[uid]
		if ok && n.State != NotifWithdrawn {
			continue
		}
		u := m.users[uid]
		nn := &Notification{Slot: slot, User: uid, Group: desired[uid], State: NotifPending}
		if u.cat == CatGuaranteed {
			nn.HasBasePower = true
			nn.BasePower = u.basePower
		}
		if nm == nil {
			nm = map[string]*Notification{}
			m.notifs[slot] = nm
		}
		nm[uid] = nn
		if slot <= m.now {
			m.doAssess(nn)
		}
	}
}

func (m *model) doAssess(n *Notification) {
	n.State = NotifAssessed
	m.assess = append(m.assess, Assessment{Slot: n.Slot, User: n.User, Group: n.Group})
}

func (m *model) assessSlot(slot int64) {
	nm := m.notifs[slot]
	uids := make([]string, 0, len(nm))
	for uid := range nm {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		if n := nm[uid]; n.State == NotifPending {
			m.doAssess(n)
		}
	}
}

// recomputeForecast 朴素重算：逐时段扫描指令求等级、排序选组。
func (m *model) recomputeForecast() {
	nextB := m.nextSlotStart()
	simAcc := make(map[string]int64, len(m.accum))
	for id, a := range m.accum {
		simAcc[id] = a
	}
	for _, g := range m.curSel {
		simAcc[g] += m.slotLen
	}
	horizon := nextB
	for _, in := range m.instrs {
		e := in.end
		if in.cancelled && in.cancelEff < e {
			e = in.cancelEff
		}
		if e > horizon {
			horizon = e
		}
	}
	newF := map[int64][]string{}
	for b := nextB; b < horizon; b += m.slotLen {
		if k := m.levelAtSlot(b); k > 0 {
			sel := selectFrom(simAcc, k)
			for _, g := range sel {
				simAcc[g] += m.slotLen
			}
			newF[b] = sel
		}
	}
	seen := map[int64]bool{}
	var slots []int64
	for b := range m.forecast {
		seen[b] = true
		slots = append(slots, b)
	}
	for b := range newF {
		if !seen[b] {
			slots = append(slots, b)
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, b := range slots {
		m.reconcile(b, newF[b])
	}
	m.forecast = newF
}

func (m *model) resettleCurrent() {
	k := m.levelAtSlot(m.curStart)
	sel := selectFrom(m.accum, k)
	m.curSel = sel
	if n := len(m.history); n > 0 && m.history[n-1].Start == m.curStart {
		m.history[n-1].Level = k
		m.history[n-1].Groups = sel
	}
	m.reconcile(m.curStart, sel)
}

func (m *model) issue(id, regionID string, level int, start, end int64) error {
	if _, dup := m.instrs[id]; dup {
		return paramErr("指令ID重复: " + id)
	}
	r, ok := m.regions[regionID]
	if !ok {
		return paramErr("区域不存在: " + regionID)
	}
	if level < 1 || level > len(r) {
		return paramErr("等级越界：须为 1..区域内组数")
	}
	if start%m.slotLen != 0 || end%m.slotLen != 0 {
		return paramErr("窗口未对齐时段边界")
	}
	if end <= start {
		return paramErr("窗口为空")
	}
	if start < m.nextSlotStart() {
		return paramErr("不允许对已开始或已结束的时段下指令")
	}
	m.instrs[id] = &mInstr{start: start, end: end, region: regionID,
		changes: []levelChange{{eff: start, level: level}}}
	m.recomputeForecast()
	return nil
}

func (m *model) modify(id string, newLevel int) error {
	if newLevel < 1 {
		return paramErr("等级越界：须为正整数")
	}
	in, ok := m.instrs[id]
	if !ok || in.cancelled {
		return instrErr(id)
	}
	if newLevel > len(m.regions[in.region]) {
		return paramErr("等级越界：须不超过区域内组数")
	}
	in.changes = append(in.changes, levelChange{eff: m.effBoundary(), level: newLevel})
	if m.effBoundary() == m.now {
		m.resettleCurrent()
	}
	m.recomputeForecast()
	return nil
}

func (m *model) cancel(id string) error {
	in, ok := m.instrs[id]
	if !ok || in.cancelled {
		return instrErr(id)
	}
	in.cancelled = true
	in.cancelEff = m.effBoundary()
	if m.effBoundary() == m.now {
		m.resettleCurrent()
	}
	m.recomputeForecast()
	return nil
}

func (m *model) confirm(userID string, slot int64) error {
	if _, ok := m.users[userID]; !ok {
		return paramErr("用户不存在: " + userID)
	}
	if m.now >= slot {
		return deadlineErr("确认须在时段开始前到达")
	}
	n, ok := m.notifs[slot][userID]
	if !ok || n.State == NotifWithdrawn {
		return notifErr("该用户在该时段无有效通知")
	}
	if n.State == NotifPending {
		n.State = NotifConfirmed
	}
	return nil
}

func (m *model) moveGroup(userID, groupID string) error {
	u, ok := m.users[userID]
	if !ok {
		return paramErr("用户不存在: " + userID)
	}
	g, ok := m.groups[groupID]
	if !ok {
		return paramErr("组不存在: " + groupID)
	}
	for _, cg := range m.curSel {
		if cg == u.group {
			return busyErr("用户所在组当前处于被限时段: " + u.group)
		}
	}
	delete(m.groups[u.group], userID)
	u.group = groupID
	g[userID] = true
	return nil
}

func (m *model) setCategory(userID string, cat Category, bp int64) error {
	u, ok := m.users[userID]
	if !ok {
		return paramErr("用户不存在: " + userID)
	}
	if !validCat(cat) || bp < 0 {
		return paramErr("类别或保底功率非法")
	}
	for _, cg := range m.curSel {
		if cg == u.group {
			return busyErr("用户所在组当前处于被限时段: " + u.group)
		}
	}
	u.cat = cat
	u.basePower = bp
	return nil
}

func (m *model) advanceTo(t int64) error {
	if t < m.now {
		return clockErr("目标时刻早于当前时刻")
	}
	for b := m.nextSlotStart(); b <= t; b += m.slotLen {
		for _, g := range m.curSel {
			m.accum[g] += m.slotLen
		}
		k := m.levelAtSlot(b)
		sel := selectFrom(m.accum, k)
		m.curSel = sel
		m.curStart = b
		m.reconcile(b, sel)
		m.assessSlot(b)
		delete(m.forecast, b)
		m.history = append(m.history, SlotRecord{Start: b, Level: k, Groups: sel})
	}
	m.now = t
	return nil
}

// ---- 随机对照测试 ----

type view struct {
	now    int64
	accum  map[string]int64
	cur    []string
	hist   []SlotRecord
	assess []Assessment
	notifs []Notification
}

func schedView(s *Scheduler, groups []string) view {
	acc := map[string]int64{}
	for _, g := range groups {
		a, _ := s.Accum(g)
		acc[g] = a
	}
	return view{s.Now(), acc, s.CurrentSelection(), s.History(), s.Assessments(), s.AllNotifications()}
}

func modelView(m *model) view {
	acc := map[string]int64{}
	for g, a := range m.accum {
		acc[g] = a
	}
	var notifs []Notification
	var slots []int64
	for slot := range m.notifs {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, slot := range slots {
		notifs = append(notifs, sortedNotifs(m.notifs[slot])...)
	}
	return view{m.now, acc, m.curSel, m.history, m.assess, notifs}
}

func kindOfErr(err error) (Kind, bool) {
	if err == nil {
		return 0, false
	}
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

func checkInvariants(t *testing.T, v view, slotLen int64) {
	t.Helper()
	var minA, maxA int64 = -1, 0
	for g, a := range v.accum {
		if a%slotLen != 0 {
			t.Fatalf("accum[%s]=%d 不是时段长度的倍数", g, a)
		}
		if minA < 0 || a < minA {
			minA = a
		}
		if a > maxA {
			maxA = a
		}
	}
	if len(v.accum) > 0 && maxA-minA > slotLen {
		t.Fatalf("公平性不变量被破坏: max=%d min=%d", maxA, minA)
	}
	for _, r := range v.hist {
		if len(r.Groups) != r.Level {
			t.Fatalf("时段 %d: 有效等级 %d 但被限组数 %d", r.Start, r.Level, len(r.Groups))
		}
	}
}

func TestRandomAgainstModel(t *testing.T) {
	for seed := uint64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			runRandom(t, seed)
		})
	}
}

func runRandom(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	const slotLen = int64(10)
	s := NewScheduler(slotLen)
	m := newModel(slotLen)
	mustOK(t, s.AddRegion("R"))
	m.addRegion("R")
	groups := []string{"g1", "g2", "g3", "g4"}
	for _, g := range groups {
		mustOK(t, s.AddGroup("R", g))
		m.addGroup("R", g)
	}
	users := []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6", "u7"}
	for i, u := range users {
		cat := Category(i % 3)
		var bp int64
		if cat == CatGuaranteed {
			bp = int64(100 + i)
		}
		mustOK(t, s.AddUser(u, groups[i%len(groups)], cat, bp))
		m.addUser(u, groups[i%len(groups)], cat, bp)
	}

	var instrIDs []string
	issueN, addN, histLen := 0, 0, 0
	pickInstr := func() string {
		if len(instrIDs) > 0 && rng.IntN(100) < 80 {
			return instrIDs[rng.IntN(len(instrIDs))]
		}
		return "ghost"
	}

	for step := 0; step < 300; step++ {
		now := s.Now()
		var desc string
		var errS, errM error
		switch dice := rng.IntN(100); {
		case dice < 30: // 推进时钟（含偶发回退）
			tgt := now + rng.Int64N(50)
			if rng.IntN(100) < 3 && now > 0 {
				tgt = now - 1 - rng.Int64N(10)
			}
			desc = fmt.Sprintf("ADVANCE now=%d -> %d", now, tgt)
			errS, errM = s.AdvanceTo(tgt), m.advanceTo(tgt)
		case dice < 50: // 发布指令（含偶发非法参数）
			issueN++
			id := fmt.Sprintf("i%d", issueN)
			level := 1 + rng.IntN(4)
			start := (now/slotLen+1)*slotLen + slotLen*rng.Int64N(5)
			end := start + slotLen*(1+rng.Int64N(4))
			region := "R"
			if r := rng.IntN(100); r < 6 {
				level = rng.IntN(7) // 可能为 0 或超过组数
			} else if r < 10 {
				start += 1 + rng.Int64N(9) // 未对齐
			} else if r < 13 {
				region = "noregion"
			}
			desc = fmt.Sprintf("ISSUE %s region=%s level=%d [%d,%d)", id, region, level, start, end)
			errS, errM = s.Issue(id, region, level, start, end), m.issue(id, region, level, start, end)
			if errS == nil {
				instrIDs = append(instrIDs, id)
			}
		case dice < 62: // 改级
			id, level := pickInstr(), rng.IntN(6)
			desc = fmt.Sprintf("MODIFY %s level=%d (now=%d)", id, level, now)
			errS, errM = s.Modify(id, level), m.modify(id, level)
		case dice < 74: // 取消
			id := pickInstr()
			desc = fmt.Sprintf("CANCEL %s (now=%d)", id, now)
			errS, errM = s.Cancel(id), m.cancel(id)
		case dice < 86: // 确认
			u := users[rng.IntN(len(users))]
			if rng.IntN(100) < 5 {
				u = "ghost"
			}
			slot := slotLen * rng.Int64N(12)
			desc = fmt.Sprintf("CONFIRM %s slot=%d (now=%d)", u, slot, now)
			errS, errM = s.Confirm(u, slot), m.confirm(u, slot)
		case dice < 93: // 换组
			u := users[rng.IntN(len(users))]
			g := groups[rng.IntN(len(groups))]
			if rng.IntN(100) < 5 {
				g = "ghost"
			}
			desc = fmt.Sprintf("MOVE %s -> %s (now=%d)", u, g, now)
			errS, errM = s.MoveGroup(u, g), m.moveGroup(u, g)
		case dice < 97: // 改类别
			u := users[rng.IntN(len(users))]
			cat := Category(rng.IntN(3))
			bp := int64(rng.IntN(500))
			desc = fmt.Sprintf("SETCAT %s cat=%d bp=%d (now=%d)", u, cat, bp, now)
			errS, errM = s.SetCategory(u, cat, bp), m.setCategory(u, cat, bp)
		default: // 新增用户
			addN++
			u := fmt.Sprintf("w%d", addN)
			g := groups[rng.IntN(len(groups))]
			cat := Category(rng.IntN(3))
			bp := int64(rng.IntN(500))
			desc = fmt.Sprintf("ADDUSER %s group=%s cat=%d bp=%d", u, g, cat, bp)
			errS, errM = s.AddUser(u, g, cat, bp), func() error { m.addUser(u, g, cat, bp); return nil }()
		}

		ks, okS := kindOfErr(errS)
		km, okM := kindOfErr(errM)
		if okS != okM || (okS && ks != km) {
			t.Fatalf("#%d %s\n调度器: %v\n模型:   %v", step, desc, errS, errM)
		}
		out := "OK"
		if errS != nil {
			out = errS.Error()
		}
		// 判定依据：错误消息含触发的校验；推进则附新结算的时段记录。
		extra := ""
		if h := s.History(); len(h) > histLen {
			extra = fmt.Sprintf(" | 新结算: %v", h[histLen:])
			histLen = len(h)
		}
		t.Logf("#%03d %s -> %s%s | now=%d accum=%v cur=%v 考核数=%d",
			step, desc, out, extra, s.Now(), schedView(s, groups).accum, s.CurrentSelection(), len(s.Assessments()))

		vs, vm := schedView(s, groups), modelView(m)
		if !reflect.DeepEqual(vs, vm) {
			t.Fatalf("#%d %s 状态分叉:\n调度器: %+v\n模型:   %+v", step, desc, vs, vm)
		}
		checkInvariants(t, vs, slotLen)
	}
}
