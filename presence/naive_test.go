package presence

import "sort"

// 朴素参考模型：不做任何增量优化，只维护每台设备的最近一次租约与一条只追加事件日志。
// 所有可见状态都通过对事件日志做纯回放得到，与生产实现的堆/计数器结构完全独立。

const (
	nkExpire = iota // 同一时刻排在切换之前
	nkSwitch
)

type nLease struct {
	status    Status
	exp       int64
	seq       int64
	reportEvt int64 // 产生该租约的 report 事件 id
}

type nEvent struct {
	id        int64
	at        int64
	rank      int
	tie       int64
	kind      string // expire, report, offline, invis, block, unblock
	user      string
	dev       string
	status    Status
	who       string
	on        bool
	reportEvt int64
}

type naiveModel struct {
	t        int64
	now      int64
	leaseSeq int64
	evtSeq   int64
	opSeq    int64
	users    map[string]map[string]*nLease
	events   []*nEvent
	subs     map[string]map[string]int64
	sent     map[string]int // 每个观察者历史上已返回的通知条数
	sentDrop map[string]int // 上次返回时累计丢弃基数
}

func newNaive(t int64) *naiveModel {
	return &naiveModel{
		t:        t,
		users:    map[string]map[string]*nLease{},
		subs:     map[string]map[string]int64{},
		sent:     map[string]int{},
		sentDrop: map[string]int{},
	}
}

func (m *naiveModel) validBase(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	if now < m.now {
		return ErrClockRollback
	}
	return nil
}

// advance 线性扫描全部设备，使所有 exp <= now 的租约按 (exp, 续租次序) 逐个到期。
func (m *naiveModel) advance(now int64) {
	type ex struct {
		exp  int64
		seq  int64
		user string
		dev  string
	}
	var due []ex
	for u, devs := range m.users {
		for d, l := range devs {
			if l != nil && l.exp <= now {
				due = append(due, ex{l.exp, l.seq, u, d})
			}
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].exp != due[j].exp {
			return due[i].exp < due[j].exp
		}
		return due[i].seq < due[j].seq
	})
	for _, e := range due {
		l := m.users[e.user][e.dev]
		if l == nil || l.exp != e.exp || l.seq != e.seq {
			continue
		}
		m.evtSeq++
		m.events = append(m.events, &nEvent{
			id: m.evtSeq, at: e.exp, rank: nkExpire, tie: e.seq,
			kind: "expire", user: e.user, dev: e.dev, reportEvt: l.reportEvt,
		})
		m.users[e.user][e.dev] = nil
	}
}

func (m *naiveModel) addSwitch(ev *nEvent) {
	m.opSeq++
	m.evtSeq++
	ev.id = m.evtSeq
	ev.at = m.now
	ev.rank = nkSwitch
	ev.tie = m.opSeq
	m.events = append(m.events, ev)
}

func (m *naiveModel) Report(user, device string, status Status, now int64) error {
	if user == "" || device == "" {
		return ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return err
	}
	devs := m.users[user]
	if devs != nil {
		if _, exists := devs[device]; !exists && len(devs) >= maxDevices {
			return ErrTooManyDevices
		}
	}
	if !isDeviceStatus(status) {
		return ErrInvalidStatus
	}
	m.advance(now)
	if devs == nil {
		devs = map[string]*nLease{}
		m.users[user] = devs
	}
	m.leaseSeq++
	newEvtID := m.evtSeq + 1 // addSwitch 即将分配的事件 id
	devs[device] = &nLease{status: status, exp: now + m.t, seq: m.leaseSeq, reportEvt: newEvtID}
	m.now = now
	m.addSwitch(&nEvent{kind: "report", user: user, dev: device, status: status, reportEvt: newEvtID})
	return nil
}

func (m *naiveModel) Offline(user, device string, now int64) error {
	if user == "" || device == "" {
		return ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return err
	}
	devs := m.users[user]
	if devs == nil {
		return ErrUserNotFound
	}
	l, ok := devs[device]
	if !ok {
		return ErrDeviceNotFound
	}
	m.advance(now)
	m.now = now
	if l != nil && l.exp > now {
		devs[device] = nil
		m.addSwitch(&nEvent{kind: "offline", user: user, dev: device})
	}
	return nil
}

func (m *naiveModel) SetInvisible(user string, on bool, now int64) error {
	if user == "" {
		return ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return err
	}
	if _, ok := m.users[user]; !ok {
		return ErrUserNotFound
	}
	if m.invisibleAt(user, now) == on {
		return ErrAlreadyInvisible
	}
	m.advance(now)
	m.now = now
	m.addSwitch(&nEvent{kind: "invis", user: user, on: on})
	return nil
}

func (m *naiveModel) Block(owner, who string, now int64) error {
	if owner == "" || who == "" {
		return ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return err
	}
	if _, ok := m.users[owner]; !ok {
		return ErrUserNotFound
	}
	if _, ok := m.users[who]; !ok {
		return ErrUserNotFound
	}
	if owner == who {
		return ErrBlockSelf
	}
	if m.blockedAt(owner, who, now) {
		return ErrAlreadyBlocked
	}
	m.advance(now)
	m.now = now
	m.addSwitch(&nEvent{kind: "block", user: owner, who: who})
	return nil
}

func (m *naiveModel) Unblock(owner, who string, now int64) error {
	if owner == "" || who == "" {
		return ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return err
	}
	if _, ok := m.users[owner]; !ok {
		return ErrUserNotFound
	}
	if _, ok := m.users[who]; !ok {
		return ErrUserNotFound
	}
	if !m.blockedAt(owner, who, now) {
		return ErrNotBlocked
	}
	m.advance(now)
	m.now = now
	m.addSwitch(&nEvent{kind: "unblock", user: owner, who: who})
	return nil
}

func (m *naiveModel) Subscribe(viewer, target string, now int64) error {
	if viewer == "" || target == "" {
		return ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return err
	}
	if _, ok := m.users[viewer]; !ok {
		return ErrUserNotFound
	}
	if _, ok := m.users[target]; !ok {
		return ErrUserNotFound
	}
	if viewer == target {
		return ErrSubscribeSelf
	}
	if set := m.subs[viewer]; set != nil {
		if _, ok := set[target]; ok {
			return ErrAlreadySubscribed
		}
	}
	m.advance(now)
	m.now = now
	if m.subs[viewer] == nil {
		m.subs[viewer] = map[string]int64{}
	}
	m.subs[viewer][target] = m.evtSeq
	return nil
}

func (m *naiveModel) invisibleAt(target string, t int64) bool {
	on := false
	for _, e := range m.events {
		if e.kind == "invis" && e.user == target && e.at <= t {
			on = e.on
		}
	}
	return on
}

func (m *naiveModel) blockedAt(target, viewer string, t int64) bool {
	blocked := false
	for _, e := range m.events {
		if e.at > t {
			break
		}
		if e.kind == "block" && e.user == target && e.who == viewer {
			blocked = true
		}
		if e.kind == "unblock" && e.user == target && e.who == viewer {
			blocked = false
		}
	}
	return blocked
}

// replayVisible 从事件日志重建到 id 时刻的租约集合后计算可见状态。
func (m *naiveModel) replayVisible(target, viewer string, id int64) Status {
	return m.replayRange(target, viewer, 0, id)
}

func (m *naiveModel) visibleAfterEvent(target, viewer string, id int64) Status {
	return m.replayVisible(target, viewer, id)
}

// replayRange 使用 (fromID, toID] 内的事件重建状态。区间重放时，
// 起点之前的 report 不在 current 中，因此其陈旧 expire 事件也不会误删新租约。
func (m *naiveModel) replayVisibleRange(target, viewer string, fromID, toID int64) Status {
	return m.replayRange(target, viewer, fromID, toID)
}

type rLease struct {
	status    Status
	reportEvt int64
}

// snapshotAt 重放到 id（含）时刻，返回 target 存活租约、隐身与拉黑状态。
func (m *naiveModel) snapshotAt(target, viewer string, id int64) (map[string]rLease, bool, bool) {
	current := map[string]rLease{}
	on := false
	blocked := false
	for _, e := range m.events {
		if e.id > id {
			break
		}
		switch e.kind {
		case "report":
			if e.user == target {
				current[e.dev] = rLease{status: e.status, reportEvt: e.id}
			}
		case "offline":
			if e.user == target {
				delete(current, e.dev)
			}
		case "expire":
			if e.user == target {
				if l, ok := current[e.dev]; ok && l.reportEvt == e.reportEvt {
					delete(current, e.dev)
				}
			}
		case "invis":
			if e.user == target {
				on = e.on
			}
		case "block":
			if e.user == target && e.who == viewer && viewer != target {
				blocked = true
			}
		case "unblock":
			if e.user == target && e.who == viewer {
				blocked = false
			}
		}
	}
	return current, on, blocked
}

func (m *naiveModel) replayRange(target, viewer string, fromID, toID int64) Status {
	// 以 fromID 时刻的完整状态为种子（含起点之前仍存活的租约与隐身/拉黑），
	// 再应用 (fromID, toID] 内的事件。
	current, on, blocked := m.snapshotAt(target, viewer, fromID)
	for _, e := range m.events {
		if e.id <= fromID || e.id > toID {
			continue
		}
		switch e.kind {
		case "report":
			if e.user == target {
				current[e.dev] = rLease{status: e.status, reportEvt: e.id}
			}
		case "offline":
			if e.user == target {
				delete(current, e.dev)
			}
		case "expire":
			if e.user == target {
				if l, ok := current[e.dev]; ok && l.reportEvt == e.reportEvt {
					delete(current, e.dev)
				}
			}
		case "invis":
			if e.user == target {
				on = e.on
			}
		case "block":
			if e.user == target && e.who == viewer && viewer != target {
				blocked = true
			}
		case "unblock":
			if e.user == target && e.who == viewer {
				blocked = false
			}
		}
	}
	counts := [3]int{}
	for _, l := range current {
		counts[l.status-1]++
	}
	if viewer != target && (on || blocked) {
		return StatusOffline
	}
	switch {
	case counts[0] > 0:
		return StatusOnline
	case counts[1] > 0:
		return StatusBusy
	case counts[2] > 0:
		return StatusAway
	default:
		return StatusOffline
	}
}

type nQueryResult struct {
	Status      Status
	ActiveCount int
	Devices     []DeviceStatus
}

// Query 在推进到期后，直接依据“当前租约 + 事件回放”给出可见状态。
func (m *naiveModel) Query(viewer, target string, now int64) (nQueryResult, error) {
	if viewer == "" || target == "" {
		return nQueryResult{}, ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return nQueryResult{}, err
	}
	if _, ok := m.users[viewer]; !ok {
		return nQueryResult{}, ErrUserNotFound
	}
	devs, ok := m.users[target]
	if !ok {
		return nQueryResult{}, ErrUserNotFound
	}
	m.advance(now)
	m.now = now
	res := nQueryResult{}
	for _, l := range devs {
		if l != nil && l.exp > now {
			res.ActiveCount++
		}
	}
	res.Status = m.replayVisible(target, viewer, m.evtSeq)
	if viewer == target {
		for name, l := range devs {
			st := StatusOffline
			if l != nil {
				st = l.status
			}
			res.Devices = append(res.Devices, DeviceStatus{Device: name, Status: st})
		}
		sort.Slice(res.Devices, func(i, j int) bool { return res.Devices[i].Device < res.Devices[j].Device })
	}
	return res, nil
}

// relevant 判断事件 e 是否可能改变 viewer 对 target 的可见状态。
func (m *naiveModel) relevant(e *nEvent, target, viewer string) bool {
	if e.user != target {
		return false
	}
	switch e.kind {
	case "report", "offline", "expire", "invis":
		return true
	case "block", "unblock":
		return e.who == viewer
	}
	return false
}

// blockedAtEvent 返回事件 e 生效后 viewer 是否处于被 target 拉黑状态。
func (m *naiveModel) blockedAtEvent(target, viewer string, id int64) bool {
	blocked := false
	for _, e := range m.events {
		if e.id > id {
			break
		}
		if e.user == target && e.who == viewer {
			if e.kind == "block" {
				blocked = true
			}
			if e.kind == "unblock" {
				blocked = false
			}
		}
	}
	return blocked
}

// invisibleAtEvent 返回事件 id 生效后 target 是否处于隐身状态。
func (m *naiveModel) invisibleAtEvent(target string, id int64) bool {
	on := false
	for _, e := range m.events {
		if e.id > id {
			break
		}
		if e.kind == "invis" && e.user == target {
			on = e.on
		}
	}
	return on
}

func (m *naiveModel) invisibleBeforeEvent(target string, id int64) bool {
	on := false
	for _, e := range m.events {
		if e.id >= id {
			break
		}
		if e.kind == "invis" && e.user == target {
			on = e.on
		}
	}
	return on
}

func (m *naiveModel) blockedBeforeEvent(target, viewer string, id int64) bool {
	blocked := false
	for _, e := range m.events {
		if e.id >= id {
			break
		}
		if e.user == target && e.who == viewer {
			if e.kind == "block" {
				blocked = true
			}
			if e.kind == "unblock" {
				blocked = false
			}
		}
	}
	return blocked
}

// Drain 用“订阅起点 + 事件日志重放”独立重建观察者通知序列，再施加有界队列语义。
func (m *naiveModel) Drain(viewer string, now int64) (DrainResult, error) {
	if viewer == "" {
		return DrainResult{}, ErrInvalidArgument
	}
	if err := m.validBase(now); err != nil {
		return DrainResult{}, err
	}
	if _, ok := m.users[viewer]; !ok {
		return DrainResult{}, ErrUserNotFound
	}
	m.advance(now)
	m.now = now

	targets := m.subs[viewer]
	last := map[string]Status{}
	var evs []*nEvent
	for target, startEvt := range targets {
		last[target] = m.replayRange(target, viewer, startEvt, startEvt)
		for _, e := range m.events {
			if e.id <= startEvt {
				continue
			}
			if m.relevant(e, target, viewer) {
				evs = append(evs, e)
			}
		}
	}
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].at != evs[j].at {
			return evs[i].at < evs[j].at
		}
		if evs[i].rank != evs[j].rank {
			return evs[i].rank < evs[j].rank
		}
		return evs[i].tie < evs[j].tie
	})

	var out []Notification
	for _, e := range evs {
		target := e.user
		startEvt := targets[target]
		vis := m.replayVisibleRange(target, viewer, startEvt, e.id)
		deliver := true
		// 抑制与否只取决于事件当时的隐身/拉黑状态；通知一经产生即固化，事后切换不撤回。
		// 拉黑当下（block 事件本身）必须投递变离线。
		// 隐身/拉黑的“切换当下”本身必须投递变离线（或恢复）通知；
		// 只有切换之后、隐身/拉黑期间发生的其他变化才被抑制。
		invisThen := viewer != target && e.kind != "invis" && m.invisibleAtEvent(target, e.id)
		blockedThen := viewer != target && e.kind != "block" && m.blockedAtEvent(target, viewer, e.id)
		if invisThen || blockedThen {
			deliver = false
		}
		if deliver && vis != last[target] {
			out = append(out, Notification{Target: target, Status: vis, EffectiveAt: e.at})
		}
		last[target] = vis
	}

	// 生产语义：队列在每次 Drain 后清空，Dropped 仅统计两次 Drain 之间新发生的丢弃。
	prevSent := m.sent[viewer]
	batch := out
	if prevSent <= len(batch) {
		batch = batch[prevSent:]
	} else {
		batch = nil
	}
	res := DrainResult{Notifications: []Notification{}}
	if len(batch) > maxNotifications {
		res.Dropped = len(batch) - maxNotifications
		batch = batch[len(batch)-maxNotifications:]
	}
	res.Notifications = append(res.Notifications, batch...)
	m.sent[viewer] = len(out)
	return res, nil
}
