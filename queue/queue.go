package queue

import "ontology/triage"

// Treap 是导出的、以 uint64 键排序的确定性最小堆平衡树。
// room 包用它维护按 (callAt+A, 登记号) 排序的已叫号者。
type Treap[V any] struct {
	root *tNode[V]
	seed uint64
}

type tNode[V any] struct {
	key  uint64
	val  V
	prio uint64
	l, r *tNode[V]
}

func NewTreap[V any]() *Treap[V] {
	return &Treap[V]{seed: 0x9E3779B97F4A7C15}
}

func (t *Treap[V]) rand() uint64 {
	x := t.seed
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	t.seed = x
	return x
}

func (t *Treap[V]) Insert(key uint64, val V) {
	t.root = t.insert(t.root, key, val)
}

func (t *Treap[V]) insert(n *tNode[V], key uint64, val V) *tNode[V] {
	if n == nil {
		return &tNode[V]{key: key, val: val, prio: t.rand()}
	}
	if key < n.key {
		n.l = t.insert(n.l, key, val)
		if n.l.prio < n.prio {
			n = rotR(n)
		}
	} else {
		n.r = t.insert(n.r, key, val)
		if n.r.prio < n.prio {
			n = rotL(n)
		}
	}
	return n
}

func rotL[V any](n *tNode[V]) *tNode[V] {
	r := n.r
	n.r = r.l
	r.l = n
	return r
}

func rotR[V any](n *tNode[V]) *tNode[V] {
	l := n.l
	n.l = l.r
	l.r = n
	return l
}

func (t *Treap[V]) Delete(key uint64) {
	t.root = t.del(t.root, key)
}

func (t *Treap[V]) del(n *tNode[V], key uint64) *tNode[V] {
	if n == nil {
		return nil
	}
	if key < n.key {
		n.l = t.del(n.l, key)
	} else if key > n.key {
		n.r = t.del(n.r, key)
	} else {
		switch {
		case n.l == nil:
			return n.r
		case n.r == nil:
			return n.l
		case n.l.prio < n.r.prio:
			n = rotR(n)
			n.r = t.del(n.r, key)
		default:
			n = rotL(n)
			n.l = t.del(n.l, key)
		}
	}
	return n
}

// PopFirst 删除并返回最小键元素，空树时 ok=false。
func (t *Treap[V]) PopFirst() (key uint64, val V, ok bool) {
	if t.root == nil {
		return 0, val, false
	}
	n := t.root
	for n.l != nil {
		n = n.l
	}
	key, val = n.key, n.val
	t.root = t.del(t.root, key)
	return key, val, true
}

func (t *Treap[V]) FirstKey() (uint64, bool) {
	if t.root == nil {
		return 0, false
	}
	n := t.root
	for n.l != nil {
		n = n.l
	}
	return n.key, true
}

func (t *Treap[V]) Len() int {
	var count func(*tNode[V]) int
	count = func(n *tNode[V]) int {
		if n == nil {
			return 0
		}
		return 1 + count(n.l) + count(n.r)
	}
	return count(t.root)
}

// WaitKey 把排序键 (q, regNo) 打包为 uint64，二者均 <=1e9。
func WaitKey(q, regNo int) uint64 {
	return uint64(q)<<32 | uint64(regNo)
}

type ID string

type Status int

const (
	Waiting Status = iota
	Called
	Gone
	Finished
)

type Entry struct {
	Patient ID
	RegNo   int
	Level   int
	Q       int
	LA      int
	Miss    int
	Arrived bool
	CallAt  int
	Room    any
	Status  Status
}

type Q struct {
	r        [5]int
	grace    int
	nextReg  int
	patients map[ID]*Entry
	wait     [5]*Treap[*Entry]

	// examined 累计 FirstEligible 中“考察”的候诊者数（仅在被叫号路径计数）。
	examined int
}

func New(r1, r2, r3, r4, grace int) *Q {
	q := &Q{patients: make(map[ID]*Entry), nextReg: 1}
	q.r[1], q.r[2], q.r[3], q.r[4] = r1, r2, r3, r4
	q.grace = grace
	for i := range q.wait {
		q.wait[i] = NewTreap[*Entry]()
	}
	return q
}

func (q *Q) Grace() int {
	return q.grace
}

func (q *Q) Register(now int, patient ID, v triage.Vitals) *Entry {
	e := &Entry{Patient: patient, RegNo: q.nextReg, Level: triage.Level(v), Q: now, LA: now, Status: Waiting}
	q.nextReg++
	q.patients[patient] = e
	q.wait[e.Level].Insert(WaitKey(e.Q, e.RegNo), e)
	return e
}

// Reassess 返回新条目与旧等级。
func (q *Q) Reassess(now int, patient ID, v triage.Vitals) (*Entry, int) {
	e := q.patients[patient]
	old := e.Level
	nl := triage.Level(v)
	e.LA = now
	if nl == old {
		return e, old
	}
	q.wait[old].Delete(WaitKey(e.Q, e.RegNo))
	switch {
	case nl < old:
		// 升级更急：q 保留。
	case nl > old:
		e.Q = now
	}
	e.Level = nl
	q.wait[nl].Insert(WaitKey(e.Q, e.RegNo), e)
	return e, old
}

func (q *Q) Overdue(now int, e *Entry) bool {
	return now-e.LA > q.r[e.Level]
}

// FirstEligible 按 levels 给定的优先级（1 在前）、级内按 (q, regNo)
// 升序考察候诊者。visit 返回 true 表示接受该患者并停止；每个被 visit
// 的候诊者计入 examined。
func (q *Q) FirstEligible(now int, levels []int, visit func(e *Entry) bool) *Entry {
	for _, lv := range levels {
		tr := q.wait[lv]
		n := tr.root
		var stack []*tNode[*Entry]
		for n != nil || len(stack) > 0 {
			for n != nil {
				stack = append(stack, n)
				n = n.l
			}
			n = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			e := n.val
			q.examined++
			if visit(e) {
				return e
			}
			n = n.r
		}
	}
	return nil
}

func (q *Q) MarkCalled(e *Entry) {
	q.wait[e.Level].Delete(WaitKey(e.Q, e.RegNo))
	e.Status = Called
	e.Arrived = false
}

func (q *Q) MarkArrived(e *Entry) {
	e.Arrived = true
}

func (q *Q) Finish(e *Entry) {
	e.Status = Finished
}

func (q *Q) Return(e *Entry, qtime int) bool {
	e.Miss++
	if e.Miss >= 3 {
		e.Status = Gone
		return true
	}
	e.Status = Waiting
	e.Arrived = false
	e.Q = qtime
	q.wait[e.Level].Insert(WaitKey(e.Q, e.RegNo), e)
	return false
}

func (q *Q) Get(patient ID) (*Entry, bool) {
	e, ok := q.patients[patient]
	return e, ok
}

func (q *Q) Examined() int {
	return q.examined
}

func (q *Q) ResetExamined() {
	q.examined = 0
}
