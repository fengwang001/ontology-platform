package alert

import (
	"container/heap"
	"errors"
)

var (
	// ErrUnknownTest 项目未登记。
	ErrUnknownTest = errors.New("alert: unknown test code")
)

// State 事件状态。
type State int

const (
	StateNotify   State = iota // 待通知
	StateReadBack              // 待回读
	StateAct                   // 待处置
	StateClosed                // 已闭环
)

// Result 追加进事件的一条检验结果。
type Result struct {
	Now     int64
	Patient string
	Code    string
	Value   int64
}

// Event 危急事件。
type Event struct {
	ID       int64
	Patient  string
	Code     string
	Sev      int
	Rep      int64
	Deadline int64
	State    State
	Results  []Result
	Late     bool   // 粘滞逾期标记
	Tech     string // 本次通知的通知人
	Receiver string
	Mismatch int
	heapIdx  int // 在到期堆中的下标；不在堆中为 -1
}

type openKey struct{ patient, code string }

// 到期最小堆：按 (deadline, 事件号) 升序。只含未闭环且尚未落地逾期的事件。
type dueHeap []*Event

func (h dueHeap) Len() int { return len(h) }
func (h dueHeap) Less(i, j int) bool {
	if h[i].Deadline != h[j].Deadline {
		return h[i].Deadline < h[j].Deadline
	}
	return h[i].ID < h[j].ID
}
func (h dueHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}
func (h *dueHeap) Push(x any) {
	e := x.(*Event)
	e.heapIdx = len(*h)
	*h = append(*h, e)
}
func (h *dueHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.heapIdx = -1
	*h = old[:n-1]
	return e
}

// Board 事件板，骨架占位。
type Board struct {
	events []*Event           // 下标 = 事件号-1
	open   map[openKey]*Event // 每位患者每个项目的未闭环事件
	due    dueHeap

	// 非导出计数器（touched 证明用）
	lastResultTouches int // 最近一次 Ingest 定位时触碰的事件数
	lastLandingPops   int // 最近一次落地从堆中取出的事件数
	lastLandingLanded int // 最近一次实际落地数
}

// NewBoard 创建事件板，骨架占位。
func NewBoard() *Board {
	b := &Board{open: make(map[openKey]*Event)}
	b.due = dueHeap{}
	heap.Init(&b.due)
	return b
}

// Ingest 上报一条危急结果（调用方须已判定为危急）。
// 返回事件、是否新建、是否本次发生升级。
func (b *Board) Ingest(now int64, patient, code string, v int64, sev int, limitFor func(sev int) int64) (e *Event, created, upgraded bool) {
	b.lastResultTouches = 0
	key := openKey{patient, code}
	e = b.open[key]
	b.lastResultTouches = 1 // map 定位至多触碰 1 个事件，与未闭环事件总数无关
	r := Result{Now: now, Patient: patient, Code: code, Value: v}
	if e == nil {
		e = &Event{
			ID:       int64(len(b.events) + 1),
			Patient:  patient,
			Code:     code,
			Sev:      sev,
			Rep:      v,
			Deadline: now + limitFor(sev),
			State:    StateNotify,
			Results:  []Result{r},
			heapIdx:  -1,
		}
		b.events = append(b.events, e)
		b.open[key] = e
		heap.Push(&b.due, e)
		return e, true, false
	}
	e.Results = append(e.Results, r)
	if sev > e.Sev {
		e.Sev = sev
		e.Rep = v
		e.State = StateNotify // 已做通知与回读作废
		e.Tech = ""
		e.Receiver = ""
		e.Mismatch = 0
		if d := now + limitFor(sev); d < e.Deadline {
			e.Deadline = d
		}
		heap.Fix(&b.due, e.heapIdx) // 升级使 deadline 变小：原位调整，不留作废条目
		return e, false, true
	}
	return e, false, false
}

// Get 按号取事件（含已闭环）。
func (b *Board) Get(id int64) *Event {
	if id < 1 || int(id) > len(b.events) {
		return nil
	}
	return b.events[id-1]
}

// Events 返回全部事件的快照（测试与遍历用）。
func (b *Board) Events() []*Event {
	out := make([]*Event, len(b.events))
	copy(out, b.events)
	return out
}

// LandOverdue 在判定时刻 now 之前，按 (deadline,id) 升序把所有新逾期事件
// （deadline<now；恰等不逾期）落地：粘滞置 Late 并从到期结构取出。
func (b *Board) LandOverdue(now int64) []*Event {
	b.lastLandingPops = 0
	var landed []*Event
	for b.due.Len() > 0 {
		top := b.due[0] // 堆顶检查：即使不逾期也触碰 1 次
		if top.Deadline >= now {
			break
		}
		heap.Pop(&b.due)
		b.lastLandingPops++
		top.Late = true
		delete(b.open, openKey{top.Patient, top.Code})
		landed = append(landed, top)
	}
	b.lastLandingLanded = len(landed)
	return landed
}

// Close 将事件闭环并从到期结构移除（逾期落地过的事件已不在堆中）。
func (b *Board) Close(e *Event) {
	e.State = StateClosed
	delete(b.open, openKey{e.Patient, e.Code})
	if e.heapIdx >= 0 {
		heap.Remove(&b.due, e.heapIdx)
	}
}

// LastResultTouches 返回上次 Ingest 定位触碰的事件数（证明 ≤1）。
func (b *Board) LastResultTouches() int { return b.lastResultTouches }

// LastLandingPops 返回上次落地从到期结构取出的事件数。
func (b *Board) LastLandingPops() int { return b.lastLandingPops }

// LastLandingLanded 返回上次实际落地事件数（Pops ≤ Landed+1）。
func (b *Board) LastLandingLanded() int { return b.lastLandingLanded }
