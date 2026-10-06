// Package queue 实现候诊排序、复评与过号回队。
//
// 候诊者按等级分四个堆，堆内按 (q, 登记号) 升序；已叫号者在一个按
// (callAt+A, 登记号) 升序的过号堆中。排序键为 (等级, q, 登记号) 升序。
package queue

import (
	"container/heap"
	"errors"

	"ontology/triage"
)

// 拒绝原因（按判定优先级：参数非法 > 存在性 > 状态不符）。
var (
	ErrInvalidParam = errors.New("queue: invalid parameter")
	ErrExistence    = errors.New("queue: patient or room existence error")
	ErrState        = errors.New("queue: state mismatch")
)

// State 为患者状态。
type State int

const (
	Waiting   State = iota // 候诊中
	Called                 // 已叫号、等待到诊
	InService              // 就诊中
	Gone                   // 已离队（就诊完成或三次过号）
)

// Patient 为一名已登记患者。排序键为 (Level, Q, Reg) 升序。
type Patient struct {
	ID     string // 外部患者标识
	Reg    int    // 登记号，自 1 递增
	Level  int    // 等级 1..4
	Q      int    // 入级时刻
	LA     int    // 上次评估时刻
	CallAt int    // 最近一次叫号时刻
	Miss   int    // 过号次数
	State  State
	idx    int // 所在堆中的下标（Waiting 时在 wait[Level]，Called 时在 miss）
}

// waitHeap 为同一等级内的候诊堆，按 (Q, Reg) 升序。
type waitHeap []*Patient

func (h waitHeap) Len() int { return len(h) }
func (h waitHeap) Less(i, j int) bool {
	if h[i].Q != h[j].Q {
		return h[i].Q < h[j].Q
	}
	return h[i].Reg < h[j].Reg
}
func (h waitHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *waitHeap) Push(x any) {
	p := x.(*Patient)
	p.idx = len(*h)
	*h = append(*h, p)
}
func (h *waitHeap) Pop() any {
	old := *h
	n := len(old)
	p := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return p
}

// missHeap 为已叫号者的过号堆，按 (CallAt, Reg) 升序
// （A 为常量，(CallAt+A, Reg) 与 (CallAt, Reg) 同序）。
type missHeap []*Patient

func (h missHeap) Len() int { return len(h) }
func (h missHeap) Less(i, j int) bool {
	if h[i].CallAt != h[j].CallAt {
		return h[i].CallAt < h[j].CallAt
	}
	return h[i].Reg < h[j].Reg
}
func (h missHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *missHeap) Push(x any) {
	p := x.(*Patient)
	p.idx = len(*h)
	*h = append(*h, p)
}
func (h *missHeap) Pop() any {
	old := *h
	n := len(old)
	p := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return p
}

// Queue 为候诊队列。非并发安全，由上层（room.System）串行化。
type Queue struct {
	r       [5]int // 复评时限，按下标等级 1..4
	a       int    // 到诊宽限
	nextReg int
	pats    map[string]*Patient
	wait    [5]waitHeap
	miss    missHeap

	examinedCall   int // 最近一次 Select 考察的候诊者数
	examinedSettle int // 最近一次 Settle 取出的已叫号者数
}

// NewQueue 创建队列；r1..r4 为四个等级的复评时限，a 为到诊宽限，各为 1..10^4。
func NewQueue(r1, r2, r3, r4, a int) (*Queue, error) {
	for _, x := range [5]int{r1, r2, r3, r4, a} {
		if x < 1 || x > 10000 {
			return nil, ErrInvalidParam
		}
	}
	return &Queue{
		r:       [5]int{0, r1, r2, r3, r4},
		a:       a,
		nextReg: 1,
		pats:    make(map[string]*Patient),
	}, nil
}

// Has 报告患者是否已登记（含已离队者）。
func (q *Queue) Has(id string) bool {
	_, ok := q.pats[id]
	return ok
}

// Get 返回患者，未登记时 ok 为 false。
func (q *Queue) Get(id string) (p *Patient, ok bool) {
	p, ok = q.pats[id]
	return p, ok
}

// Snapshot 按登记号升序返回全部患者的副本，供测试对拍。
func (q *Queue) Snapshot() []Patient {
	ps := make([]Patient, 0, len(q.pats))
	for _, p := range q.pats {
		ps = append(ps, *p)
	}
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].Reg < ps[j-1].Reg; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
	return ps
}

// Register 定级入队：登记号自 1 递增，q=la=now。
func (q *Queue) Register(now int, id string, v triage.Vitals) (*Patient, error) {
	if !v.Valid() {
		return nil, ErrInvalidParam
	}
	if q.Has(id) {
		return nil, ErrExistence
	}
	lv := triage.Level(v)
	p := &Patient{ID: id, Reg: q.nextReg, Level: lv, Q: now, LA: now, State: Waiting}
	q.nextReg++
	q.pats[id] = p
	heap.Push(&q.wait[lv], p)
	return p, nil
}

// Reassess 复评，仅对候诊者：la=now；升级 q 保留，降级 q=now，不变 q 不变。
func (q *Queue) Reassess(now int, id string, v triage.Vitals) (*Patient, error) {
	if !v.Valid() {
		return nil, ErrInvalidParam
	}
	p, ok := q.pats[id]
	if !ok {
		return nil, ErrExistence
	}
	if p.State != Waiting {
		return nil, ErrState
	}
	lv := triage.Level(v)
	p.LA = now
	if lv > p.Level { // 降级，重新排队
		p.Q = now
	} // 升级或不变：q 保留
	if lv != p.Level {
		heap.Remove(&q.wait[p.Level], p.idx)
		p.Level = lv
		heap.Push(&q.wait[lv], p)
	}
	return p, nil
}

// Overdue 报告候诊者在 now 是否复评逾期（now-LA > R[等级]，恰等不逾期）。
func (q *Queue) Overdue(p *Patient, now int) bool {
	return now-p.LA > q.r[p.Level]
}

// lessKey 按排序键 (Level, Q, Reg) 比较两名候诊者。
func lessKey(a, b *Patient) bool {
	if a.Level != b.Level {
		return a.Level < b.Level
	}
	if a.Q != b.Q {
		return a.Q < b.Q
	}
	return a.Reg < b.Reg
}

// headMin 返回候选等级中排序键最小的堆顶候诊者。
func (q *Queue) headMin(levels []int) *Patient {
	var best *Patient
	for _, lv := range levels {
		if q.wait[lv].Len() == 0 {
			continue
		}
		if p := q.wait[lv][0]; best == nil || lessKey(p, best) {
			best = p
		}
	}
	return best
}

// Select 在候选等级中按排序键取第一个未逾期者，转为已叫号并返回；
// 排在其之前的逾期候选按序记入 skipped（仍留候诊）。无可叫者返回 nil。
func (q *Queue) Select(now int, levels []int) (chosen *Patient, skipped []*Patient) {
	q.examinedCall = 0
	var stash []*Patient
	defer func() { // 被跳过的逾期者回到候诊堆
		for _, p := range stash {
			heap.Push(&q.wait[p.Level], p)
		}
	}()
	for {
		best := q.headMin(levels)
		if best == nil {
			return nil, skipped
		}
		q.examinedCall++
		heap.Remove(&q.wait[best.Level], best.idx)
		if q.Overdue(best, now) {
			stash = append(stash, best)
			skipped = append(skipped, best)
			continue
		}
		best.State = Called
		best.CallAt = now
		heap.Push(&q.miss, best)
		return best, skipped
	}
}

// Settle 按 (CallAt+A, Reg) 升序落地全部过号（now-CallAt > A，恰等不过号）：
// miss 加 1，达 3 离队，否则回候诊且 q=CallAt+A，la 与等级不变。
// 返回本次落地的患者（含离队者），按落地顺序。
func (q *Queue) Settle(now int) []*Patient {
	q.examinedSettle = 0
	var landed []*Patient
	for q.miss.Len() > 0 {
		p := q.miss[0]
		q.examinedSettle++
		if now-p.CallAt <= q.a {
			break
		}
		heap.Pop(&q.miss)
		p.Miss++
		if p.Miss >= 3 {
			p.State = Gone
		} else {
			p.State = Waiting
			p.Q = p.CallAt + q.a
			heap.Push(&q.wait[p.Level], p)
		}
		landed = append(landed, p)
	}
	return landed
}

// Arrive 到诊：要求已叫号，转为就诊中。
func (q *Queue) Arrive(id string) error {
	p, ok := q.pats[id]
	if !ok {
		return ErrExistence
	}
	if p.State != Called {
		return ErrState
	}
	heap.Remove(&q.miss, p.idx)
	p.State = InService
	return nil
}

// Finish 完成就诊：要求就诊中，患者离队。
func (q *Queue) Finish(id string) error {
	p, ok := q.pats[id]
	if !ok {
		return ErrExistence
	}
	if p.State != InService {
		return ErrState
	}
	p.State = Gone
	return nil
}
