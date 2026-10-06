// Package standby 实现航班候补席位的登记与兑现。
package standby

import "errors"

// 拒绝类别（按统一拒绝次序定义）。
var (
	ErrInvalid        = errors.New("参数非法")
	ErrClockRewind    = errors.New("时钟回退")
	ErrFlightNotFound = errors.New("航班不存在")
	ErrFlightCanceled = errors.New("航班已取消")
	ErrEntryNotFound  = errors.New("条目不存在")
	ErrEntryState     = errors.New("条目状态不符")
	ErrDuplicate      = errors.New("重复登记")
	ErrQueueFull      = errors.New("队列已满")
)

// Priority 是候补优先等级，数值越大越优先。
type Priority uint8

const (
	PrioLow  Priority = 0
	PrioMid  Priority = 1
	PrioHigh Priority = 2
)

// Valid 判断优先等级是否合法。
func (p Priority) Valid() bool { return p <= PrioHigh }

// EntryState 是候补条目六态。
type EntryState uint8

const (
	StateWaiting   EntryState = iota // 候补中
	StatePending                     // 待确认
	StateConfirmed                   // 已确认
	StateWithdrawn                   // 已撤回（候补撤回 / 待确认撤回 / 已确认取消均归入此态）
	StateExpired                     // 已过期
	StateVoided                      // 作废（航班取消）
)

// String 返回状态名，便于测试日志与文档引用。
func (s EntryState) String() string {
	switch s {
	case StateWaiting:
		return "waiting"
	case StatePending:
		return "pending"
	case StateConfirmed:
		return "confirmed"
	case StateWithdrawn:
		return "withdrawn"
	case StateExpired:
		return "expired"
	case StateVoided:
		return "voided"
	default:
		return "invalid"
	}
}

// EntryID 是全局唯一、单调递增的条目标识。
type EntryID uint64

// Entry 是候补条目在任一时刻的完整状态。
type Entry struct {
	ID         EntryID
	Flight     string
	Cabin      string
	Passenger  string
	Party      int
	Priority   Priority
	Registered int64 // 登记时刻，等级调整后仍保留
	State      EntryState
	Deadline   int64 // 待确认期限；其余状态下无意义

	waitLeaf *trieLeaf // 候补中：在 waiting 有序集合中的叶子
	pendLeaf *trieLeaf // 待确认：在 pending 有序集合中的叶子
}

// FlightSnapshot 是某航班舱位在查询时刻结算过期后的视图。
type FlightSnapshot struct {
	Flight            string
	Cabin             string
	Capacity          int
	Confirmed         int
	Pending           int
	WaitingCount      int
	Free              int // 容量 - 已确认 - 待确认（可为负）
	Canceled          bool
	QueueLimit        int
	ConfirmationDelay int64
	Entries           []EntryInfo
}

// EntryInfo 是快照中单条条目的只读信息。
func (e Entry) Info() EntryInfo {
	return EntryInfo{
		ID:         e.ID,
		Passenger:  e.Passenger,
		Party:      e.Party,
		Priority:   e.Priority,
		Registered: e.Registered,
		State:      e.State,
		Deadline:   e.Deadline,
	}
}

// EntryInfo 是快照中单条条目的只读信息。
type EntryInfo struct {
	ID         EntryID
	Passenger  string
	Party      int
	Priority   Priority
	Registered int64
	State      EntryState
	Deadline   int64
}

type flightKey struct {
	flight string
	cabin  string
}
type flight struct {
	capacity   int
	confirmed  int
	waitingN   int // 候补中条目数（队列容量据此判定）
	pendingN   int // 待确认人数之和（不是条目数）
	canceled   bool
	queueLimit int
	delay      int64

	entries map[EntryID]*Entry
	active  map[string]*Entry // 旅客 -> 候补中/待确认条目

	// waiting[pri]：键为 (registered, id) 的有序集合，值为条目。
	waiting [3]*orderedSet
	// pending：键为 (deadline, id)，0 或 1 把（每把对应同一时刻的过期合并）。
	pending *orderedSet
}

// System 是候补系统。
type System struct {
	mu      chan struct{} // 全局互斥
	now     int64         // 上一次被接受操作的时刻
	nextID  EntryID
	flights map[flightKey]*flight
	// allEntries 让按条目标识的操作与航班数、队列长度无关：O(1) 定位。
	allEntries map[EntryID]*Entry
}

// New 创建空系统。
func New() *System {
	return &System{
		mu:         make(chan struct{}, 1),
		flights:    make(map[flightKey]*flight),
		allEntries: make(map[EntryID]*Entry),
	}
}

func newFlight(capacity, queueLimit int, delay int64) *flight {
	f := &flight{
		capacity:   capacity,
		queueLimit: queueLimit,
		delay:      delay,
		entries:    make(map[EntryID]*Entry),
		active:     make(map[string]*Entry),
		pending:    newOrderedSet(),
	}
	for i := range f.waiting {
		f.waiting[i] = newOrderedSet()
	}
	return f
}

func flightKeyOf(flightName, cabin string) flightKey {
	return flightKey{flight: flightName, cabin: cabin}
}

func (s *System) lock()   { s.mu <- struct{}{} }
func (s *System) unlock() { <-s.mu }
