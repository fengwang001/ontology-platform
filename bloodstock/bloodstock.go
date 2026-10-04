package bloodstock

import (
	"container/heap"
	"errors"
)

type ABO int

const (
	A ABO = iota
	B
	AB
	O
)

type Rh int

const (
	Positive Rh = iota
	Negative
)

type Status int

const (
	Available Status = iota
	Reserved
	Issued
	Discarded
)

type Bag struct {
	ID         string
	ABO        ABO
	Rh         Rh
	Exp        int64
	Status     Status
	Patient    string
	ReservedAt int64
	IssuedAt   int64
}

var ErrBagNotFound = errors.New("bloodstock: bag not found")

// Slot 把 (ABO,Rh) 编码为 0..7：slot = ABO*2 + rh（Positive=0, Negative=1）。
func Slot(abo ABO, rh Rh) int { return int(abo)*2 + int(rh) }

// availItem 是某血型槽位内按 (exp, bagID) 排序的可用堆元素。
type availItem struct {
	exp int64
	id  string
}

type availHeap []availItem

func (h availHeap) Len() int { return len(h) }
func (h availHeap) Less(i, j int) bool {
	if h[i].exp != h[j].exp {
		return h[i].exp < h[j].exp
	}
	return h[i].id < h[j].id
}
func (h availHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *availHeap) Push(x any)   { *h = append(*h, x.(availItem)) }
func (h *availHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// evKind：0 预留释放，1 效期报废。同刻同袋报废优先（kind 大先出）。
const (
	evRelease = 0
	evExpire  = 1
)

type event struct {
	at   int64
	kind int
	bag  string
	idx  int // 堆内位置，物理删除用
}

type eventHeap []*event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	if h[i].kind != h[j].kind {
		return h[i].kind > h[j].kind
	}
	return h[i].bag < h[j].bag
}
func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx, h[j].idx = i, j
}
func (h *eventHeap) Push(x any) {
	e := x.(*event)
	e.idx = len(*h)
	*h = append(*h, e)
}
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

type Store struct {
	m int64

	bags map[string]*Bag

	avail [8]*availHeap

	events *eventHeap
	// evRef[bag][kind] 指向事件堆中的元素；物理删除保证无陈旧事件。
	evRef map[string][2]*event

	Examined      int // 最近一次 Reserve 的考察袋数
	LandPopped    int // 最近一次 Land 的事件取出数
	LandApplied   int // 最近一次 Land 的实际落地数
	TotalExamined int
	TotalPopped   int
	TotalApplied  int
}

func NewStore(M int64) *Store {
	s := &Store{
		m:      M,
		bags:   map[string]*Bag{},
		events: &eventHeap{},
		evRef:  map[string][2]*event{},
	}
	for i := range s.avail {
		s.avail[i] = &availHeap{}
	}
	heap.Init(s.events)
	return s
}

func (s *Store) Add(id string, abo ABO, rh Rh, exp int64) *Bag {
	b := &Bag{ID: id, ABO: abo, Rh: rh, Exp: exp, Status: Available}
	s.bags[id] = b
	heap.Push(s.avail[Slot(abo, rh)], availItem{exp: exp, id: id})
	s.pushEvent(event{at: exp, kind: evExpire, bag: id})
	return b
}

func (s *Store) Get(id string) (*Bag, bool) {
	b, ok := s.bags[id]
	return b, ok
}

func (s *Store) pushEvent(e event) {
	pe := &e
	heap.Push(s.events, pe)
	ref := s.evRef[e.bag]
	ref[e.kind] = pe
	s.evRef[e.bag] = ref
}

// removeEvent 物理删除某袋某类事件（索引堆，故 Pop 取出的事件数受严格上界约束）。
func (s *Store) removeEvent(bag string, kind int) {
	ref := s.evRef[bag]
	e := ref[kind]
	if e != nil {
		heap.Remove(s.events, e.idx)
		ref[kind] = nil
		s.evRef[bag] = ref
	}
}

// dropEvents 删除某袋所有现存到期事件。
func (s *Store) dropEvents(bag string) {
	s.removeEvent(bag, evRelease)
	s.removeEvent(bag, evExpire)
	delete(s.evRef, bag)
}

// Land 落地全部 now 时刻到期事件，返回实际落地（释放/报废）袋数。
// 事件按 (at, kind=报废优先, bag) 升序处理。
func (s *Store) Land(now int64, onDiscard func(bag string)) int {
	s.LandPopped = 0
	s.LandApplied = 0
	for s.events.Len() > 0 {
		top := (*s.events)[0]
		if top.at > now {
			break
		}
		heap.Pop(s.events)
		s.LandPopped++
		b, ok := s.bags[top.bag]
		if !ok {
			continue
		}
		// top 已出堆，仅清除其引用（不可再 heap.Remove）。
		ref := s.evRef[top.bag]
		ref[top.kind] = nil
		s.evRef[top.bag] = ref
		switch top.kind {
		case evRelease:
			if b.Status != Reserved {
				continue
			}
			b.Status = Available
			b.Patient = ""
			b.ReservedAt = 0
			// 报废事件保留；释放后若 now>=exp，事件堆同刻报废会立即处理，
			// 若 exp<=now（理论上同刻已由报废优先处理），此处补一道防线。
			if b.Exp <= now {
				s.removeEvent(b.ID, evExpire)
				b.Status = Discarded
				s.LandApplied++
				if onDiscard != nil {
					onDiscard(b.ID)
				}
			} else {
				heap.Push(s.avail[Slot(b.ABO, b.Rh)], availItem{exp: b.Exp, id: b.ID})
				s.LandApplied++
			}
		case evExpire:
			s.removeEvent(top.bag, evRelease)
			switch b.Status {
			case Available:
				b.Status = Discarded
				s.LandApplied++
				if onDiscard != nil {
					onDiscard(b.ID)
				}
			case Reserved:
				b.Status = Discarded
				b.Patient = ""
				b.ReservedAt = 0
				s.LandApplied++
				if onDiscard != nil {
					onDiscard(b.ID)
				}
			}
		}
	}
	s.TotalPopped += s.LandPopped
	s.TotalApplied += s.LandApplied
	return s.LandApplied
}

// Reserve 按血型次序 order（槽位序列）选 n 袋 exp>now+M 的可用袋，全有或全无。
// examined 为本次考察的袋数（成功 Pop + 因效期排除）。
func (s *Store) Reserve(now, h int64, order []int, patient string, n int) (picked []string, examined int, ok bool) {
	s.Examined = 0
	deadline := now + s.m
	var chosen []string
	// 每个血型槽位内是 (exp,id) 升序堆；因 exp<=deadline 而排除者永不再候选，
	// 移入 near 集合。
	for _, slot := range order {
		h2 := s.avail[slot]
		for len(chosen) < n && h2.Len() > 0 {
			top := (*h2)[0]
			b := s.bags[top.id]
			if b.Status != Available {
				heap.Pop(h2) // 惰性：已离开可用态
				continue
			}
			if top.exp <= deadline {
				heap.Pop(h2)
				s.Examined++
				continue
			}
			heap.Pop(h2)
			s.Examined++
			chosen = append(chosen, top.id)
		}
		if len(chosen) == n {
			break
		}
	}
	s.TotalExamined += s.Examined
	if len(chosen) < n {
		// 全有或全无：未选中候选袋状态仍为可用，放回槽位堆；
		// 被效期排除的袋保留在 near（exp 单调，永不再候选）。
		for _, id := range chosen {
			b := s.bags[id]
			heap.Push(s.avail[Slot(b.ABO, b.Rh)], availItem{exp: b.Exp, id: b.ID})
		}
		return nil, s.Examined, false
	}
	for _, id := range chosen {
		b := s.bags[id]
		b.Status = Reserved
		b.Patient = patient
		b.ReservedAt = now
		s.pushEvent(event{at: now + h, kind: evRelease, bag: id})
	}
	return chosen, s.Examined, true
}

// ReleasePatient 释放某患者全部预留，返回释放袋数。
func (s *Store) ReleasePatient(patient string) int {
	cnt := 0
	for _, b := range s.bags {
		if b.Status == Reserved && b.Patient == patient {
			s.removeEvent(b.ID, evRelease)
			b.Status = Available
			b.Patient = ""
			b.ReservedAt = 0
			heap.Push(s.avail[Slot(b.ABO, b.Rh)], availItem{exp: b.Exp, id: b.ID})
			cnt++
		}
	}
	return cnt
}

// MarkIssued 把属于 patient 的预留袋发出，返回是否成功。
func (s *Store) MarkIssued(now int64, patient, bag string) bool {
	b, ok := s.bags[bag]
	if !ok || b.Status != Reserved || b.Patient != patient {
		return false
	}
	s.dropEvents(bag)
	b.Status = Issued
	b.Patient = ""
	b.ReservedAt = 0
	b.IssuedAt = now
	return true
}

// Return 退回已发袋。返回 (existed, expiredNow)：
// 成功时袋为可用；exp<=now 时直接报废且 expiredNow=true。
func (s *Store) Return(now int64, bag string) (bool, bool) {
	b, ok := s.bags[bag]
	if !ok || b.Status != Issued {
		return false, false
	}
	b.Status = Available
	b.IssuedAt = 0
	if b.Exp <= now {
		b.Status = Discarded
		return true, true
	}
	heap.Push(s.avail[Slot(b.ABO, b.Rh)], availItem{exp: b.Exp, id: b.ID})
	s.pushEvent(event{at: b.Exp, kind: evExpire, bag: b.ID})
	return true, false
}

// Discard 报废任意非报废袋，返回是否发生状态改变。
func (s *Store) Discard(bag string) bool {
	b, ok := s.bags[bag]
	if !ok || b.Status == Discarded {
		return false
	}
	s.dropEvents(bag)
	b.Status = Discarded
	b.Patient = ""
	b.ReservedAt = 0
	b.IssuedAt = 0
	return true
}
