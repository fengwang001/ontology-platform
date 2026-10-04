package bloodstock

import (
	"container/heap"
	"errors"
	"sort"
)

// 拒绝原因（跨包共用，issue 按规定次序映射）。
var (
	ErrInvalid      = errors.New("bloodstock: 参数非法")
	ErrClock        = errors.New("bloodstock: 时钟回退")
	ErrNotFound     = errors.New("bloodstock: 患者或血袋不存在")
	ErrDuplicate    = errors.New("bloodstock: 编号重复")
	ErrUnauthorized = errors.New("bloodstock: 无资格")
	ErrState        = errors.New("bloodstock: 状态不符")
	ErrStock        = errors.New("bloodstock: 库存不足")
	ErrTimeout      = errors.New("bloodstock: 超时不可退")
)

type ABO uint8

const (
	A ABO = iota
	B
	AB
	O
)

func (a ABO) String() string {
	switch a {
	case A:
		return "A"
	case B:
		return "B"
	case AB:
		return "AB"
	default:
		return "O"
	}
}

type Rh uint8

const (
	RhPos Rh = iota
	RhNeg
)

func (r Rh) String() string {
	if r == RhNeg {
		return "阴"
	}
	return "阳"
}

type Status uint8

const (
	Available Status = iota
	Reserved
	Issued
	Discarded
)

func (s Status) String() string {
	switch s {
	case Reserved:
		return "预留"
	case Issued:
		return "已发"
	case Discarded:
		return "报废"
	default:
		return "可用"
	}
}

func ParseABO(s string) (ABO, bool) {
	switch s {
	case "A":
		return A, true
	case "B":
		return B, true
	case "AB":
		return AB, true
	case "O":
		return O, true
	default:
		return 0, false
	}
}

func ParseRh(s string) (Rh, bool) {
	switch s {
	case "阳", "+":
		return RhPos, true
	case "阴", "-":
		return RhNeg, true
	default:
		return 0, false
	}
}

type Bag struct {
	ABO      ABO
	Rh       Rh
	Exp      int64
	Status   Status
	Patient  string
	HoldAt   int64
	IssuedAt int64
}

// Group 血型组（ABO,Rh）。
type Group struct {
	ABO ABO
	Rh  Rh
}

// Store 血袋库存、效期事件与血型组堆。非并发安全，由 issue.Manager 上锁调用。
type Store struct {
	M int64
	H int64

	bags map[string]*Bag

	expireAt  map[int64]map[string]struct{}
	releaseAt map[int64]map[string]struct{}
	timeHeap  int64Heap
	pushed    map[int64]struct{}

	groups [4][2]*bagHeap
}

type LandResult struct {
	Landed int
	Popped int
}

type PickResult struct {
	Bags     []string
	Examined int
	Expired  int
}

type heapEntry struct {
	exp int64
	bag string
}

// bagHeap 按 (exp, bag 字节序) 升序；条目在血袋生命周期内常驻。
type bagHeap struct {
	items []heapEntry
	store *Store
}

func (h *bagHeap) Len() int { return len(h.items) }
func (h *bagHeap) Less(i, j int) bool {
	if h.items[i].exp != h.items[j].exp {
		return h.items[i].exp < h.items[j].exp
	}
	return h.items[i].bag < h.items[j].bag
}
func (h *bagHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *bagHeap) Push(x any)    { h.items = append(h.items, x.(heapEntry)) }
func (h *bagHeap) Pop() any {
	old := h.items
	last := old[len(old)-1]
	h.items = old[:len(old)-1]
	return last
}

type int64Heap []int64

func (h int64Heap) Len() int           { return len(h) }
func (h int64Heap) Less(i, j int) bool { return h[i] < h[j] }
func (h int64Heap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *int64Heap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *int64Heap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// projectedStatus 返回「若在 now 落地一次」后血袋的投影状态（只读）。
func projectedStatus(b *Bag, now, H int64) Status {
	switch b.Status {
	case Available:
		if now >= b.Exp {
			return Discarded
		}
		return Available
	case Reserved:
		if now >= b.Exp {
			return Discarded // 同一刻报废先于释放
		}
		if now >= b.HoldAt+H {
			return Available
		}
		return Reserved
	default:
		return b.Status // Issued/Discarded 不参与落地
	}
}

// Projected 暴露投影状态供 issue 层只读判定。
func Projected(b *Bag, now, H int64) Status { return projectedStatus(b, now, H) }

func NewStore(M, H int64) *Store {
	s := &Store{
		M:         M,
		H:         H,
		bags:      map[string]*Bag{},
		expireAt:  map[int64]map[string]struct{}{},
		releaseAt: map[int64]map[string]struct{}{},
		pushed:    map[int64]struct{}{},
	}
	for a := A; a <= O; a++ {
		for r := RhPos; r <= RhNeg; r++ {
			s.groups[a][r] = &bagHeap{store: s}
		}
	}
	return s
}

func (s *Store) addEvent(at int64) {
	if _, ok := s.pushed[at]; !ok {
		s.pushed[at] = struct{}{}
		heap.Push(&s.timeHeap, at)
	}
}

func bucketAdd(m map[int64]map[string]struct{}, at int64, bag string) {
	set := m[at]
	if set == nil {
		set = map[string]struct{}{}
		m[at] = set
	}
	set[bag] = struct{}{}
}

func bucketDel(m map[int64]map[string]struct{}, at int64, bag string) {
	if set := m[at]; set != nil {
		delete(set, bag)
		if len(set) == 0 {
			delete(m, at)
		}
	}
}

func (s *Store) AddBag(bag string, abo ABO, rh Rh, exp int64) error {
	if _, dup := s.bags[bag]; dup {
		return ErrDuplicate
	}
	s.bags[bag] = &Bag{ABO: abo, Rh: rh, Exp: exp, Status: Available}
	bucketAdd(s.expireAt, exp, bag)
	s.addEvent(exp)
	heap.Push(s.groups[abo][rh], heapEntry{exp: exp, bag: bag})
	return nil
}

// Land 按 (时刻, 报废先于释放, 血袋号) 落地全部 now 之前（含）的到期事件。
// 只在操作被接受后调用；已发血袋的事件已在 Issue 时撤销，桶中不会出现。
// 空时刻桶不计入取出事件数，故 Popped-Landed 恒为 0（<=1）。
func (s *Store) Land(now int64) LandResult {
	res := LandResult{}
	for len(s.timeHeap) > 0 && s.timeHeap[0] <= now {
		at := heap.Pop(&s.timeHeap).(int64)
		delete(s.pushed, at)
		expSet := s.expireAt[at]
		relSet := s.releaseAt[at]
		if len(expSet) == 0 && len(relSet) == 0 {
			continue
		}
		res.Popped += len(expSet) + len(relSet)

		names := make([]string, 0, len(expSet))
		for b := range expSet {
			names = append(names, b)
		}
		sort.Strings(names)
		for _, name := range names {
			b := s.bags[name]
			if b == nil || b.Status != Available && b.Status != Reserved {
				continue
			}
			if b.Status == Reserved {
				b.Patient = ""
				b.HoldAt = 0
			}
			b.Status = Discarded
			res.Landed++
		}
		delete(s.expireAt, at)

		names = names[:0]
		for b := range relSet {
			names = append(names, b)
		}
		sort.Strings(names)
		for _, name := range names {
			b := s.bags[name]
			if b == nil || b.Status != Reserved {
				continue // 同一刻已报废（exp 先处理）或事件陈旧
			}
			b.Status = Available
			b.Patient = ""
			b.HoldAt = 0
			res.Landed++
		}
		delete(s.releaseAt, at)
	}
	return res
}

// Pick 只读地按血型组次序选择 n 袋（依据落地投影状态）。
// 任何返回（成功或库存不足）都不改变库存与堆：成功时条目留在堆中成为「预留」
// 陈旧条目（靠状态过滤），失败时本次从堆顶弹出的条目全部原样回压。
// examined = 考察血袋数 + 空组探测数 <= n + expired + 8。
func (s *Store) Pick(now int64, groups []Group, n int) (PickResult, error) {
	res := PickResult{Bags: make([]string, 0, n)}
	cutoff := now + s.M
	type popped struct {
		g Group
		e heapEntry
	}
	var taken []popped
	for _, g := range groups {
		if len(res.Bags) == n {
			break
		}
		h := s.groups[g.ABO][g.Rh]
		probed := false
		for len(res.Bags) < n {
			if len(h.items) == 0 {
				if !probed {
					res.Examined++
					probed = true
				}
				break
			}
			top := h.items[0]
			heap.Pop(h)
			taken = append(taken, popped{g: g, e: top})
			b := s.bags[top.bag]
			pst := projectedStatus(b, now, s.H)
			if pst == Discarded || pst == Issued || pst == Reserved {
				continue // 已报废/已发/仍为他人有效预留（释放时刻未到），均不可候选，不计考察
			}
			probed = true
			res.Examined++
			if top.exp <= cutoff {
				res.Expired++ // exp 恰等 now+M 亦排除
				continue
			}
			res.Bags = append(res.Bags, top.bag)
		}
	}
	if len(res.Bags) < n {
		// 零副作用：把本次弹出的全部条目（含陈旧堆顶）原样回压。
		for _, p := range taken {
			heap.Push(s.groups[p.g.ABO][p.g.Rh], p.e)
		}
		res.Bags = nil
		return res, ErrStock
	}
	// 干跑期间不能产生任何写副作用：成功也先原样回压。真正落地后由
	// PruneStale 清除陈旧堆顶，再由 Reserve 把选中袋标记为预留（其条目变陈旧）。
	for _, p := range taken {
		heap.Push(s.groups[p.g.ABO][p.g.Rh], p.e)
	}
	return res, nil
}

// Reserve 将 Pick 选中的血袋转为预留（调用前已 Land，投影与实际一致）。
func (s *Store) Reserve(bag, patient string, now int64) {
	b := s.bags[bag]
	b.Status = Reserved
	b.Patient = patient
	b.HoldAt = now
	rel := now + s.H
	if rel < b.Exp {
		bucketAdd(s.releaseAt, rel, bag)
		s.addEvent(rel)
	}
}

// PruneStale 在「操作已接受、Land 已完成」后调用：沿 8 个组堆堆顶
// 永久清除持有袋已报废/已发的陈旧条目。只在接受路径执行，故不影响
// 被拒绝操作的零副作用（干跑 Pick 弹出的条目自身会全部回压）。
func (s *Store) PruneStale() {
	for a := A; a <= O; a++ {
		for r := RhPos; r <= RhNeg; r++ {
			h := s.groups[a][r]
			for len(h.items) > 0 {
				top := h.items[0]
				b := s.bags[top.bag]
				if b != nil && (b.Status == Available || b.Status == Reserved) {
					break
				}
				heap.Pop(h)
			}
		}
	}
}

func (s *Store) Release(bag string) {
	b := s.bags[bag]
	if b == nil {
		return
	}
	if b.Status == Reserved {
		if rel := b.HoldAt + s.H; rel < b.Exp {
			bucketDel(s.releaseAt, rel, bag)
		}
		b.Status = Available
	}
	b.Patient = ""
	b.HoldAt = 0
}

// ReleasePatient 释放某患者全部预留（鉴定转存疑时用），返回释放袋数。
func (s *Store) ReleasePatient(patient string) int {
	n := 0
	for key, b := range s.bags {
		if b.Status == Reserved && b.Patient == patient {
			s.Release(key)
			n++
		}
	}
	return n
}

func (s *Store) Issue(bag string, now int64) {
	b := s.bags[bag]
	if b.HoldAt != 0 {
		if rel := b.HoldAt + s.H; rel < b.Exp {
			bucketDel(s.releaseAt, rel, bag)
		}
	}
	bucketDel(s.expireAt, b.Exp, bag) // 已发血袋不参与到期落地
	b.Status = Issued
	b.IssuedAt = now
	b.Patient = ""
	b.HoldAt = 0
}

// Return 已发血袋退回（调用方已做 30 分钟判断）。now>=exp 直接报废，否则回可用。
func (s *Store) Return(now int64, bag string) {
	b := s.bags[bag]
	b.IssuedAt = 0
	if now >= b.Exp {
		b.Status = Discarded
		return
	}
	b.Status = Available
	bucketAdd(s.expireAt, b.Exp, bag)
	s.addEvent(b.Exp)
}

func (s *Store) Discard(bag string) {
	b := s.bags[bag]
	if b.Status == Reserved && b.HoldAt != 0 {
		if rel := b.HoldAt + s.H; rel < b.Exp {
			bucketDel(s.releaseAt, rel, bag)
		}
	}
	if b.Status != Issued {
		bucketDel(s.expireAt, b.Exp, bag)
	}
	b.Status = Discarded
	b.Patient = ""
	b.HoldAt = 0
	b.IssuedAt = 0
}

func (s *Store) Get(bag string) (*Bag, bool) {
	b, ok := s.bags[bag]
	return b, ok
}

func (s *Store) Snapshot() map[string]Bag {
	out := make(map[string]Bag, len(s.bags))
	for k, v := range s.bags {
		out[k] = *v
	}
	return out
}
