package transformer

import (
	"container/heap"
	"sort"
	"sync"
)

// Stats 性能计数器，供测试验证复杂度上界。
type Stats struct {
	SlotVisits  int // 两级校验扫描的时刻槽数
	CapCompares int // 容量表二分查找的比较次数
	Settled     int // 最近一次推进落定的预约数
	SettlePops  int // 最近一次推进从队列/堆弹出的条目数
}

// endEntry 完成堆条目：按（结束时刻, ID）排序，惰性删除。
type endEntry struct {
	end int
	id  int
}

type endHeap []endEntry

func (h endHeap) Len() int { return len(h) }
func (h endHeap) Less(i, j int) bool {
	if h[i].end != h[j].end {
		return h[i].end < h[j].end
	}
	return h[i].id < h[j].id
}
func (h endHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *endHeap) Push(x any)   { *h = append(*h, x.(endEntry)) }
func (h *endHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// System 变压器容量预约系统。所有公开方法持同一把互斥锁，
// 并发调用等价于某个串行执行顺序，重放相同操作序列得到相同状态序列。
type System struct {
	mu           sync.Mutex
	now          int
	holdDuration int
	limits       map[string]int
	records      capacityTable
	res          map[int]*Reservation
	nextID       int
	feederOcc    map[string]slotMap // 每条馈线：占位中+已确认
	transOcc     slotMap            // 变压器：占位中+已确认
	confirmedOcc slotMap            // 变压器：仅已确认（容量变更校验用）
	holdQueue    []int              // 占位 ID，按创建序（到期时刻单调不减）
	done         endHeap            // 已确认预约的结束时刻堆
	stats        Stats
}

// NewSystem 构造系统。初始容量登记为时刻 0 的容量记录。
func NewSystem(cfg Config, feederLimits map[string]int, initialCapacity int) *System {
	limits := make(map[string]int, len(feederLimits))
	occ := make(map[string]slotMap, len(feederLimits))
	for f, lim := range feederLimits {
		limits[f] = lim
		occ[f] = slotMap{}
	}
	s := &System{
		holdDuration: cfg.HoldDuration,
		limits:       limits,
		res:          map[int]*Reservation{},
		nextID:       1,
		feederOcc:    occ,
		transOcc:     slotMap{},
		confirmedOcc: slotMap{},
	}
	s.records.set(0, initialCapacity)
	return s
}

// Now 返回当前时刻。
func (s *System) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Reservation 返回预约快照。
func (s *System) Reservation(id int) (Reservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.res[id]
	if !ok {
		return Reservation{}, false
	}
	return *r, true
}

// Snapshot 按 ID 升序返回全部预约快照。
func (s *System) Snapshot() []Reservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Reservation, 0, len(s.res))
	for _, r := range s.res {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CapacityAt 返回 t 时刻的变压器容量。
func (s *System) CapacityAt(t int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records.at(t, nil)
}

// Stats 返回性能计数器快照。
func (s *System) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ResetStats 清零性能计数器。
func (s *System) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = Stats{}
}

// AdvanceTime 推进当前时刻并落定到期占位与已完成预约。倒退报时钟回退。
func (s *System) AdvanceTime(to int) *Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if to < s.now {
		return &Error{Category: ErrClockRewind, Detail: "当前时刻只能前进"}
	}
	s.now = to
	s.stats.Settled = 0
	s.stats.SettlePops = 0
	s.settleLocked()
	return nil
}

// settleLocked 落定：到期占位转为已失效，区间终点不晚于当前时刻的已确认预约转为已完成。
// 开销只与本次弹出（落定+惰性清理）的条目数相关。
func (s *System) settleLocked() {
	for len(s.holdQueue) > 0 {
		r := s.res[s.holdQueue[0]]
		if r.HoldExpiresAt > s.now {
			break // 到期时刻随创建序单调不减，后续只会更晚
		}
		s.holdQueue = s.holdQueue[1:]
		s.stats.SettlePops++
		if r.Status == StatusHolding {
			s.removeOccupancyLocked(r)
			r.Status = StatusExpired
			s.stats.Settled++
		}
	}
	for len(s.done) > 0 && s.done[0].end <= s.now {
		e := heap.Pop(&s.done).(endEntry)
		s.stats.SettlePops++
		if r, ok := s.res[e.id]; ok && r.Status == StatusConfirmed && r.End == e.end {
			s.completeLocked(r)
			s.stats.Settled++
		}
	}
}

// completeLocked 把已确认预约落定为已完成并归还占用。
func (s *System) completeLocked(r *Reservation) {
	s.removeOccupancyLocked(r)
	r.Status = StatusCompleted
}

func (s *System) addOccupancyLocked(r *Reservation) {
	s.feederOcc[r.Feeder].add(r.Start, r.End, r.Power)
	s.transOcc.add(r.Start, r.End, r.Power)
	if r.Status == StatusConfirmed {
		s.confirmedOcc.add(r.Start, r.End, r.Power)
	}
}

func (s *System) removeOccupancyLocked(r *Reservation) {
	s.feederOcc[r.Feeder].add(r.Start, r.End, -r.Power)
	s.transOcc.add(r.Start, r.End, -r.Power)
	if r.Status == StatusConfirmed {
		s.confirmedOcc.add(r.Start, r.End, -r.Power)
	}
}
