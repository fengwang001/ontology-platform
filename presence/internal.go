package presence

import (
	"sort"
	"sync"
)

// device 记录一台设备的当前状态租约。
type devRec struct {
	status  Status
	expires int64 // 恰等于该时刻即视为已到期
}

// user 记录单个用户的设备、隐身开关与被订阅关系。
type user struct {
	devices map[string]*devRec
	online  int // 未到期设备数，惰性到期时维护
	counts  [3]int

	invisible bool

	// subscribers: 观察者 -> 该观察者已确认（订阅起始或上次通知）的可见状态。
	subscribers map[string]Status
}

// expEntry 是某个到期时刻桶内的条目；seq 决定同一时刻内的到期先后。
type expEntry struct {
	user   string
	device string
	seq    int64
	exp    int64
}

// viewerState 是每个观察者的通知积压。
type viewerState struct {
	queue   []Notification
	dropped int
}

// 实际服务的内部状态（替换骨架中的占位 Service）。
type svc struct {
	mu     sync.Mutex
	t      int64
	now    int64 // 已接受操作的最大 now（全局单调水位）
	users  map[string]*user
	blocks map[string]map[string]bool // owner -> set(who)
	// wheel 是长度 T+1 的环形时间轮：wheel[exp mod (T+1)] 存放该到期时刻的条目。
	// 由于租约恰为 T，任意时刻每个槽至多对应一个到期时刻，用 slotAt 记录以判定是否同一轮次。
	wheel        [][]*expEntry
	slotAt       []int64
	cursor       int64 // 已惰性处理到的到期水位（含）
	cursorPrimed bool
	leaseSeq     int64
	viewers      map[string]*viewerState
}

const (
	maxDevices       = 8
	maxNotifications = 1000
	maxNow           = int64(1_000_000_000_000)
)

func newService(leaseSeconds int64) (*svc, error) {
	if leaseSeconds < 1 || leaseSeconds > 3600 {
		return nil, ErrInvalidLease
	}
	return &svc{
		t:       leaseSeconds,
		users:   map[string]*user{},
		blocks:  map[string]map[string]bool{},
		wheel:   make([][]*expEntry, leaseSeconds+1),
		slotAt:  make([]int64, leaseSeconds+1),
		viewers: map[string]*viewerState{},
	}, nil
}

// addExpiry 把一次续租放入环形时间轮对应槽。入桶为 O(1)。
// 旧条目在到期时通过 (user,device) 当前租约时刻比对判为陈旧，惰性丢弃。
func (s *svc) addExpiry(user, device string, exp, seq int64) {
	idx := exp % (s.t + 1)
	if s.slotAt[idx] != exp {
		s.slotAt[idx] = exp
		s.wheel[idx] = nil
	}
	s.wheel[idx] = append(s.wheel[idx], &expEntry{
		user: user, device: device, seq: seq, exp: exp,
	})
}

func (s *svc) getUser(name string) *user {
	u := s.users[name]
	if u == nil {
		u = &user{devices: map[string]*devRec{}, subscribers: map[string]Status{}}
		s.users[name] = u
	}
	return u
}

// aggregateReal 在调用者已完成惰性到期的前提下计算真实聚合状态。
func aggregateReal(u *user) Status {
	switch {
	case u.counts[StatusOnline-1] > 0:
		return StatusOnline
	case u.counts[StatusBusy-1] > 0:
		return StatusBusy
	case u.counts[StatusAway-1] > 0:
		return StatusAway
	default:
		return StatusOffline
	}
}

// visibleStatus 计算 viewer 看到 target 的可见状态。
func (s *svc) visibleStatus(viewer, target string, u *user) Status {
	if viewer == target {
		return aggregateReal(u)
	}
	if u.invisible {
		return StatusOffline
	}
	if set := s.blocks[target]; set != nil && set[viewer] {
		return StatusOffline
	}
	return aggregateReal(u)
}

// emit 向 target 的全部订阅者（除 self 外）按其独立可见性投递通知。
// 仅当可见状态相对其上次已通知值发生变化时入队；被拉黑者不收通知。
func (s *svc) emit(u *user, target string, at int64) {
	for viewer, last := range u.subscribers {
		vis := s.visibleStatus(viewer, target, u)
		if vis == last {
			continue
		}
		u.subscribers[viewer] = vis
		if viewer != target {
			if set := s.blocks[target]; set != nil && set[viewer] {
				// 被拉黑者不收到任何变更通知，但已更新其记忆值，解除后不补发。
				continue
			}
		}
		s.enqueue(viewer, Notification{Target: target, Status: vis, EffectiveAt: at})
	}
}

func (s *svc) enqueue(viewer string, n Notification) {
	v := s.viewers[viewer]
	if v == nil {
		v = &viewerState{}
		s.viewers[viewer] = v
	}
	if len(v.queue) >= maxNotifications {
		v.queue = v.queue[1:]
		v.dropped++
	}
	v.queue = append(v.queue, n)
}

// emitOne 仅评估单个观察者（解除拉黑使用）：按当前可见性规则正常投递。
func (s *svc) emitOne(viewer, target string, at int64) {
	u := s.users[target]
	if u == nil {
		return
	}
	last, ok := u.subscribers[viewer]
	if !ok {
		return
	}
	vis := s.visibleStatus(viewer, target, u)
	u.subscribers[viewer] = vis
	if vis == last {
		return
	}
	s.enqueue(viewer, Notification{Target: target, Status: vis, EffectiveAt: at})
}

// emitBlocked 用于拉黑当下：拉黑决定本身使观察者看到离线，该条通知必须送达；
// 此后拉黑期间的真实变化由 emit 抑制，解除时不补发。
func (s *svc) emitBlocked(viewer, target string, at int64) {
	u := s.users[target]
	if u == nil {
		return
	}
	last, ok := u.subscribers[viewer]
	if !ok {
		return
	}
	vis := StatusOffline
	u.subscribers[viewer] = vis
	if vis == last {
		return
	}
	s.enqueue(viewer, Notification{Target: target, Status: vis, EffectiveAt: at})
}

// expireDue 按（到期时刻, 续租到达次序）逐个惰性生效到期租约。
// 每个到期引发的聚合变化以到期时刻为生效时刻；只处理 exp <= now 的条目。
func (s *svc) expireDue(now int64) {
	if !s.cursorPrimed {
		// 首次操作之前系统中没有任何租约，直接把游标对齐到当前水位，避免空扫。
		s.cursorPrimed = true
		s.cursor = now
		return
	}
	// 挂租约都由不晚于 cursor 的上报产生，故到期时刻只可能落在 (cursor, cursor+T]。
	// 若 now 远超该窗口，窗口内无任何挂租约，直接把游标跳到 now，避免空扫随时钟跨度增长。
	limit := s.cursor + s.t
	if limit > now {
		limit = now
	}
	for s.cursor < limit {
		s.cursor++
		s.fireSlot(s.cursor)
	}
	if s.cursor < now {
		s.cursor = now
	}
}

func (s *svc) fireSlot(exp int64) {
	idx := exp % (s.t + 1)
	if s.slotAt[idx] != exp {
		return
	}
	entries := s.wheel[idx]
	s.wheel[idx] = nil
	s.slotAt[idx] = 0
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })
	for _, top := range entries {
		u := s.users[top.user]
		d := u.devices[top.device]
		if d == nil || d.expires != top.exp {
			continue
		}
		old := aggregateReal(u)
		d.expires = -1
		u.online--
		u.counts[d.status-1]--
		if cur := aggregateReal(u); old != cur {
			s.emit(u, top.user, exp)
		}
	}
}

func deviceList(u *user) []DeviceStatus {
	out := make([]DeviceStatus, 0, len(u.devices))
	for name, d := range u.devices {
		st := StatusOffline
		if d.expires > 0 { // -1 或 <= 当前水位均表示已到期/已下线
			st = d.status
		}
		out = append(out, DeviceStatus{Device: name, Status: st})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

func isDeviceStatus(st Status) bool {
	return st >= StatusOnline && st <= StatusAway
}
