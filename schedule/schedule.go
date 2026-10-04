package schedule

import (
	"sort"
	"sync"

	"ontology/equip"
	"ontology/room"
)

const maxTime = int64(1_000_000_000)

type Surgery struct {
	ID        string
	Room      string
	Start     int64
	Dur       int64
	Surgeon   string
	Needs     map[string]int
	Emergency bool
}

func (s Surgery) end() int64 { return s.Start + s.Dur }

type Result struct {
	Room      string
	Start     int64
	Displaced []string
}

type entry struct {
	s Surgery
}

type Scheduler struct {
	mu      sync.Mutex
	rooms   *room.Registry
	pool    *equip.Pool
	list    []*entry // 按 Start 升序，Start 并列按 ID 升序
	byID    map[string]*entry
	lastNow int64
	haveNow bool

	// examined 为最近一次 Book 实际考察过的既有手术台数。
	examined int
}

func New() *Scheduler {
	return &Scheduler{
		rooms: room.NewRegistry(),
		pool:  equip.NewPool(),
		byID:  map[string]*entry{},
	}
}

// AddRoom 登记手术间。
func (sc *Scheduler) AddRoom(now int64, roomID string, turn int) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if now < 0 || roomID == "" || turn < 0 || turn > 240 {
		return ErrInvalid
	}
	if sc.haveNow && now < sc.lastNow {
		return ErrClockBack
	}
	sc.advance(now)
	if err := sc.rooms.Add(roomID, turn); err != nil {
		if err == room.ErrDuplicate {
			return ErrDuplicate
		}
		return ErrInvalid
	}
	return nil
}

// AddEquip 登记设备类型。
func (sc *Scheduler) AddEquip(now int64, typeName string, n, st int) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if now < 0 || typeName == "" || n < 1 || n > 100 || st < 0 || st > 240 {
		return ErrInvalid
	}
	if sc.haveNow && now < sc.lastNow {
		return ErrClockBack
	}
	sc.advance(now)
	if err := sc.pool.Add(typeName, n, st); err != nil {
		if err == equip.ErrDuplicate {
			return ErrDuplicate
		}
		return ErrInvalid
	}
	return nil
}

func (sc *Scheduler) advance(now int64) {
	if !sc.haveNow || now > sc.lastNow {
		sc.lastNow = now
		sc.haveNow = true
	}
}

// validateStruct 只做与全局状态无关的结构/范围校验。
func validateNeeds(needs map[string]int, pool *equip.Pool, checkExist bool) error {
	if len(needs) > 8 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for t, q := range needs {
		if t == "" || seen[t] {
			return ErrInvalid
		}
		seen[t] = true
		if q < 1 {
			return ErrInvalid
		}
		if checkExist {
			if !pool.Has(t) {
				return ErrNotFound
			}
			if q > pool.Total(t) {
				return ErrInvalid
			}
		}
	}
	return nil
}

func validBook(now int64, s Surgery) bool {
	if now < 0 || s.ID == "" || s.Room == "" || s.Surgeon == "" {
		return false
	}
	if s.Start < now || s.Start < 0 || s.Dur < 1 || s.Dur > 1440 {
		return false
	}
	return s.Start+s.Dur <= maxTime
}

func validEmergency(now int64, id string, dur int64, surgeon string) bool {
	if now < 0 || id == "" || surgeon == "" || dur < 1 || dur > 1440 {
		return false
	}
	return now+dur <= maxTime
}

// windowEntries 返回与 [lo,hi) 相交的在排手术（去重），并写入 examined。
func (sc *Scheduler) windowEntries(lo, hi int64) []*entry {
	// list 按 Start 升序；可能相交要求 Start < hi 且 end > lo。
	// dur ≤ 1440，故 Start ≤ lo-1440 者 end ≤ lo，必不相交，二分跳过。
	n := len(sc.list)
	lower := sort.Search(n, func(i int) bool { return sc.list[i].s.Start > lo-1440 })
	upper := sort.Search(n, func(i int) bool { return sc.list[i].s.Start >= hi })
	var out []*entry
	seen := map[*entry]bool{}
	for i := lower; i < upper; i++ {
		e := sc.list[i]
		if e.s.end() <= lo {
			continue
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sc.examined = len(out)
	return out
}

func toUses(es []*entry) []equip.Use {
	uses := make([]equip.Use, 0, len(es))
	for _, e := range es {
		uses = append(uses, equip.Use{
			ID:         e.s.ID,
			Start:      e.s.Start,
			End:        e.s.end(),
			Quantities: e.s.Needs,
		})
	}
	return uses
}
