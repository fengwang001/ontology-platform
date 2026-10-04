// Package wip 按工序与返工次数分层的在制品流转账。
//
// 队列格 queue[i][k] 表示在工序 i 待加工、已返工 k 次的件数，
// 用稀疏 map 存储（只存非空格），并维护在制总量，
// 因此单次操作触碰的队列格数与工序数 n、返工上限 R 无关。
package wip

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hold"
	"ontology/routing"
)

// 拒绝原因按优先级：参数非法 > 无权限 > 不存在 > 状态不符 > 数量超出 > 超出返工上限。
// 均可用 errors.Is 区分。
var (
	ErrInvalidParam = errors.New("wip: invalid parameter")
	ErrPermission   = errors.New("wip: permission denied")
	ErrNotFound     = errors.New("wip: not found")
	ErrState        = errors.New("wip: invalid state")
	ErrOverflow     = errors.New("wip: quantity exceeds queue cell")
	ErrReworkLimit  = errors.New("wip: rework limit exceeded")
)

// MaxQ 单张工单投入量上限。
const MaxQ = 1_000_000_000

type cell struct{ i, k int }

type order struct {
	rt       *routing.Route
	q        int64
	queue    map[cell]int64 // 稀疏：只存非空格
	inProc   int64          // 在制总量 = 全部队列格之和，O(1) 维护
	done     int64
	scrapped int64
	closed   bool
	gate     *hold.Gate
}

// QEAuth 判定 operator 是否具备 QE 权限（用于 Resume）。
type QEAuth func(operator string) bool

// Ledger 在制流转账。全局互斥锁保证所有操作线性化，
// 并发调用等价于某个串行顺序，相同操作序列重放得到相同结果。
type Ledger struct {
	mu        sync.Mutex
	routes    *routing.Registry
	yield     int64
	minSample int64
	qe        QEAuth
	orders    map[string]*order
	touched   int64 // 非导出计数器：累计触碰的队列格数，证明与 n、R 无关
}

// NewLedger 创建流转账。yield（1..100）与 minSample（1..1e9）为首过良率
// 门禁构造参数；qe 为 nil 时视为无人具备 QE 权限。
func NewLedger(routes *routing.Registry, yield, minSample int64, qe QEAuth) (*Ledger, error) {
	if routes == nil {
		return nil, fmt.Errorf("%w: nil routing registry", ErrInvalidParam)
	}
	if _, err := hold.NewGate(yield, minSample); err != nil {
		return nil, err
	}
	if qe == nil {
		qe = func(string) bool { return false }
	}
	return &Ledger{
		routes:    routes,
		yield:     yield,
		minSample: minSample,
		qe:        qe,
		orders:    make(map[string]*order),
	}, nil
}

// Open 开立工单：投入量 Q 为 1..1e9，初始 queue[1][0]=Q。
func (l *Ledger) Open(wo, route string, q int64) error {
	if wo == "" || route == "" || q < 1 || q > MaxQ {
		return fmt.Errorf("%w: Open wo=%q route=%q q=%d", ErrInvalidParam, wo, route, q)
	}
	rt, err := l.routes.Get(route)
	if err != nil {
		return fmt.Errorf("%w: route %q", ErrNotFound, route)
	}
	gate, err := hold.NewGate(l.yield, l.minSample)
	if err != nil {
		return err // 构造时已校验，不会发生
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.orders[wo]; ok {
		return fmt.Errorf("%w: work order %q already exists", ErrState, wo)
	}
	o := &order{rt: rt, q: q, queue: make(map[cell]int64), gate: gate}
	l.touched++
	o.queue[cell{1, 0}] = q
	o.inProc = q
	l.orders[wo] = o
	return nil
}

// Report 报工：良品流入 queue[i+1][k]（末工序计入 done），报废计入
// scrapped，返工件流入 queue[back[i]][k+1]。检验点且 k=0 时落账后做
// 首过良率判定，触发挂起的本次报工本身有效。
func (l *Ledger) Report(wo string, i, k int, good, scrap, rework int64) error {
	// 1. 参数非法（不依赖路线的部分）
	if wo == "" || good < 0 || scrap < 0 || rework < 0 || good+scrap+rework < 1 ||
		i < 1 || i > routing.MaxOps || k < 0 || k > routing.MaxReworkLimit {
		return fmt.Errorf("%w: Report wo=%q i=%d k=%d g/s/r=%d/%d/%d",
			ErrInvalidParam, wo, i, k, good, scrap, rework)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// 2. 不存在
	o, ok := l.orders[wo]
	if !ok {
		return fmt.Errorf("%w: work order %q", ErrNotFound, wo)
	}
	// 3. 状态不符
	if o.closed {
		return fmt.Errorf("%w: work order %q closed", ErrState, wo)
	}
	if o.gate.Suspended() {
		return fmt.Errorf("%w: work order %q suspended", ErrState, wo)
	}
	// 1b. 参数非法（依赖路线的部分：i 或 k 越界）
	if i > o.rt.N || k > o.rt.R {
		return fmt.Errorf("%w: i=%d k=%d out of route bounds n=%d R=%d",
			ErrInvalidParam, i, k, o.rt.N, o.rt.R)
	}
	sum := good + scrap + rework
	src := cell{i, k}
	l.touched++
	srcQty := o.queue[src]
	// 4. 数量超出
	if sum > srcQty {
		return fmt.Errorf("%w: cell(%d,%d) has %d, need %d", ErrOverflow, i, k, srcQty, sum)
	}
	// 5. 超出返工上限：k 已等于 R 的件只能报良品或报废
	if rework > 0 && k == o.rt.R {
		return fmt.Errorf("%w: k=%d already at R=%d", ErrReworkLimit, k, o.rt.R)
	}
	// 落账：至多触碰 3 个队列格（源格、良品目的格、返工目的格）
	o.queue[src] = srcQty - sum
	if srcQty == sum {
		delete(o.queue, src)
	}
	o.inProc -= sum
	if good > 0 {
		if i == o.rt.N {
			o.done += good
		} else {
			l.touched++
			dst := cell{i + 1, k}
			o.queue[dst] += good
			o.inProc += good
		}
	}
	o.scrapped += scrap
	if rework > 0 {
		l.touched++
		dst := cell{o.rt.Back[i-1], k + 1}
		o.queue[dst] += rework
		o.inProc += rework
	}
	// 首过良率门禁：仅检验点且 k=0 层的报工进入统计并触发判定
	if o.rt.Insp[i-1] && k == 0 {
		o.gate.Record(i, good, sum)
	}
	return nil
}

// Split 把 queue[i][k] 中的 qty 件拆到新工单：新工单沿用同一路线，
// 投入量为 qty，仅 queue[i][k]=qty，done、scrapped 与检验统计均为 0，
// 且不处于挂起；原工单的 Q 与该格各减 qty。
func (l *Ledger) Split(wo, newWo string, i, k int, qty int64) error {
	// 1. 参数非法（不依赖路线的部分）
	if wo == "" || newWo == "" || qty < 1 ||
		i < 1 || i > routing.MaxOps || k < 0 || k > routing.MaxReworkLimit {
		return fmt.Errorf("%w: Split wo=%q newWo=%q i=%d k=%d qty=%d",
			ErrInvalidParam, wo, newWo, i, k, qty)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// 2. 不存在
	o, ok := l.orders[wo]
	if !ok {
		return fmt.Errorf("%w: work order %q", ErrNotFound, wo)
	}
	// 3. 状态不符
	if o.closed {
		return fmt.Errorf("%w: work order %q closed", ErrState, wo)
	}
	if o.gate.Suspended() {
		return fmt.Errorf("%w: work order %q suspended", ErrState, wo)
	}
	if _, dup := l.orders[newWo]; dup {
		return fmt.Errorf("%w: work order %q already exists", ErrState, newWo)
	}
	// 1b. 参数非法（依赖路线的部分）
	if i > o.rt.N || k > o.rt.R {
		return fmt.Errorf("%w: i=%d k=%d out of route bounds n=%d R=%d",
			ErrInvalidParam, i, k, o.rt.N, o.rt.R)
	}
	src := cell{i, k}
	l.touched++
	srcQty := o.queue[src]
	// 4. 数量超出
	if qty > srcQty {
		return fmt.Errorf("%w: cell(%d,%d) has %d, need %d", ErrOverflow, i, k, srcQty, qty)
	}
	gate, err := hold.NewGate(l.yield, l.minSample)
	if err != nil {
		return err // 不会发生
	}
	no := &order{rt: o.rt, q: qty, queue: make(map[cell]int64), gate: gate}
	l.touched++
	no.queue[src] = qty
	no.inProc = qty
	o.queue[src] = srcQty - qty
	if srcQty == qty {
		delete(o.queue, src)
	}
	o.inProc -= qty
	o.q -= qty
	l.orders[newWo] = no
	return nil
}

// Close 关闭工单：在制总量为 0 才可关闭（用维护的 inProc 判定，
// 触碰 0 个队列格），返回 done、scrapped 与欠产 Q−done。
func (l *Ledger) Close(wo string) (done, scrapped, shortfall int64, err error) {
	if wo == "" {
		return 0, 0, 0, fmt.Errorf("%w: Close wo empty", ErrInvalidParam)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	o, ok := l.orders[wo]
	if !ok {
		return 0, 0, 0, fmt.Errorf("%w: work order %q", ErrNotFound, wo)
	}
	if o.closed {
		return 0, 0, 0, fmt.Errorf("%w: work order %q closed", ErrState, wo)
	}
	if o.gate.Suspended() {
		return 0, 0, 0, fmt.Errorf("%w: work order %q suspended", ErrState, wo)
	}
	if o.inProc != 0 {
		return 0, 0, 0, fmt.Errorf("%w: work order %q still has %d in process", ErrState, wo, o.inProc)
	}
	o.closed = true
	return o.done, o.scrapped, o.q - o.done, nil
}

// Resume 解除挂起：需 QE 权限；恢复后该工单所有检验点的首过统计
// 清零重新累计。未挂起却 Resume 报状态不符。
func (l *Ledger) Resume(wo, operator string) error {
	if wo == "" || operator == "" {
		return fmt.Errorf("%w: Resume wo=%q operator=%q", ErrInvalidParam, wo, operator)
	}
	if !l.qe(operator) {
		return fmt.Errorf("%w: operator %q lacks QE", ErrPermission, operator)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	o, ok := l.orders[wo]
	if !ok {
		return fmt.Errorf("%w: work order %q", ErrNotFound, wo)
	}
	if o.closed {
		return fmt.Errorf("%w: work order %q closed", ErrState, wo)
	}
	if !o.gate.Suspended() {
		return fmt.Errorf("%w: work order %q not suspended", ErrState, wo)
	}
	o.gate.Resume()
	return nil
}

// Snapshot 工单的一致快照（审计与测试用）。
type Snapshot struct {
	Q         int64
	Done      int64
	Scrapped  int64
	InProc    int64
	Cells     map[[2]int]int64 // [i, k] -> 件数，只含非空格
	Suspended bool
	Closed    bool
}

// Snapshot 返回工单当前状态的一致快照。
func (l *Ledger) Snapshot(wo string) (Snapshot, error) {
	if wo == "" {
		return Snapshot{}, fmt.Errorf("%w: Snapshot wo empty", ErrInvalidParam)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	o, ok := l.orders[wo]
	if !ok {
		return Snapshot{}, fmt.Errorf("%w: work order %q", ErrNotFound, wo)
	}
	s := Snapshot{
		Q:         o.q,
		Done:      o.done,
		Scrapped:  o.scrapped,
		InProc:    o.inProc,
		Suspended: o.gate.Suspended(),
		Closed:    o.closed,
		Cells:     make(map[[2]int]int64, len(o.queue)),
	}
	for c, v := range o.queue {
		s.Cells[[2]int{c.i, c.k}] = v
	}
	return s, nil
}
