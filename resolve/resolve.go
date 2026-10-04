// Package resolve 按操作序列还原无实时时钟设备的遥测墙钟时间戳。
//
// 所有结果只由操作序列决定：服务端不读取自身时钟。同一 Resolver 可并发调用，
// 结果等价于某个串行执行顺序。
package resolve

import (
	"errors"
	"sync"

	"ontology/hold"
	"ontology/syncpt"
)

// Source 标记一条记录墙钟的来源。
type Source int

const (
	Interp Source = iota // 同步点恰等或前后同步点间线性插值
	Back                 // 只有后继同步点，按速率 1 回推
	Fwd                  // 封口时按最后同步点速率 1 外推
	Est                  // 借助后一启动启动时刻做跨启动估计
)

func (s Source) String() string {
	switch s {
	case Interp:
		return "Interp"
	case Back:
		return "Back"
	case Fwd:
		return "Fwd"
	default:
		return "Est"
	}
}

var (
	ErrInvalid  = errors.New("resolve: invalid argument")
	ErrNoDevice = errors.New("resolve: device not registered")
	ErrSealed   = errors.New("resolve: boot already sealed")
	ErrFull     = errors.New("resolve: pending record capacity full")
	ErrDupSync  = syncpt.ErrDupSync
	ErrSkew     = syncpt.ErrSkew
)

// Emit 是一次被还原输出：记录载荷、墙钟毫秒与来源。
type Emit struct {
	Payload any
	Wall    int64
	Source  Source
}

const maxK = 1_000_000_000
const maxW = 1_000_000_000_000_000

type boot struct {
	id     int64
	syncs  *syncpt.Set
	pend   *hold.Heap
	kmax   int64 // 记录出现过的最大 k
	startS int64 // 首次同步点确定的启动时刻 S=W-k
	open   bool  // 是否仍为该设备的开放启动
	estim  bool  // 是否已封口且全程无同步点（待估）
}

type device struct {
	mu      sync.Mutex
	pmax    int
	pending int // 该设备全部启动待定记录总数
	seq     int64
	// examined/released 累计最近一次 Sync 在本设备上释放待定记录时
	// 考察的堆顶数与实际释放数；不变量 examined <= released+1。
	examined int
	released int
	boots    []*boot // 按出现序号严格递增（可跳号）
	byID     map[int64]*boot
	maxBoot  int64
	seen     bool
}

// Resolver 是多设备时间戳还原器。
type Resolver struct {
	mu   sync.Mutex
	devs map[int64]*device
}

// New 创建空还原器。
func New() *Resolver {
	return &Resolver{devs: map[int64]*device{}}
}

// Register 注册一台设备并设定其待定记录上限 Pmax（1..1e6）。重复注册报 ErrInvalid。
func (r *Resolver) Register(dev int64, pmax int) error {
	if pmax < 1 || pmax > 1_000_000 {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devs[dev]; ok {
		return ErrInvalid
	}
	r.devs[dev] = &device{pmax: pmax, byID: map[int64]*boot{}}
	return nil
}

func validK(k int64) bool { return k >= 0 && k <= maxK }

func validBoot(b int64) bool { return b >= 1 && b <= 1_000_000 }

func validW(w int64) bool { return w >= 0 && w <= maxW }

// Record 处理一条遥测记录 (启动序号 b, 启动后毫秒 k)，返回本操作引发的输出。
func (r *Resolver) Record(dev, b, k int64, payload any) ([]Emit, error) {
	if !validBoot(b) || !validK(k) {
		return nil, ErrInvalid
	}
	d, err := r.lockDevice(dev)
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	var sealOut []Emit
	var sealed *boot
	var sealedDrained []hold.Record
	target, existed := d.byID[b]
	if !existed {
		if b <= d.maxBootSeen() {
			return nil, ErrSealed // 小于已出现最大序号
		}
		sealed, sealOut, sealedDrained = d.sealOpen()
		target = d.newBoot(b)
	} else if !target.open {
		return nil, ErrSealed
	}

	// ErrFull 在封口之后的待定数上判定；拒绝则整操作（含封口）不发生。
	if !canResolveNow(target, k) && d.pending >= d.pmax {
		if !existed {
			d.rollbackSeal(sealed, sealedDrained, target)
		}
		return nil, ErrFull
	}

	out := sealOut
	if k > target.kmax {
		target.kmax = k
	}
	out = append(out, d.ingest(target, k, payload)...)
	return out, nil
}

// Sync 处理一个同步点 (b, k, W)，返回本操作引发的输出。
// 输出次序：封口 Fwd -> 当前启动释放（Back/Interp）-> 估计链 Est（启动降序）。
func (r *Resolver) Sync(dev, b, k, w int64) ([]Emit, error) {
	if !validBoot(b) || !validK(k) || !validW(w) {
		return nil, ErrInvalid
	}
	d, err := r.lockDevice(dev)
	if err != nil {
		return nil, err
	}
	defer d.mu.Unlock()

	var sealOut []Emit
	var sealed *boot
	var sealedDrained []hold.Record
	target, existed := d.byID[b]
	if !existed {
		if b <= d.maxBootSeen() {
			return nil, ErrSealed
		}
		sealed, sealOut, sealedDrained = d.sealOpen()
		target = d.newBoot(b)
	} else if !target.open {
		return nil, ErrSealed
	}

	firstSync := target.syncs.Len() == 0
	if err := target.syncs.Add(k, w); err != nil {
		if !existed {
			d.rollbackSeal(sealed, sealedDrained, target)
		}
		return nil, err
	}

	out := sealOut
	rel, examined := d.release(target, k)
	d.released = len(rel)
	d.examined = examined
	out = append(out, rel...)

	if firstSync {
		out = append(out, d.estimateChain(target, w-k)...)
	}
	return out, nil
}

func (d *device) maxBootSeen() int64 {
	if !d.seen {
		return 0
	}
	return d.maxBoot
}

func (d *device) newBoot(id int64) *boot {
	bt := &boot{
		id:    id,
		syncs: syncpt.New(),
		pend:  hold.New(),
		open:  true,
	}
	d.boots = append(d.boots, bt)
	d.byID[id] = bt
	d.maxBoot = id
	d.seen = true
	return bt
}

// sealOpen 封口当前开放启动：有同步点则其余待定记录外推（Fwd）；
// 无同步点则标记为待估，记录继续待定。返回被封口的启动、输出与
// 从待定堆中抽出的原始记录（待估分支为 nil），供拒绝时整体回滚。
func (d *device) sealOpen() (sealed *boot, out []Emit, drained []hold.Record) {
	if !d.seen {
		return nil, nil, nil
	}
	old := d.boots[len(d.boots)-1]
	old.open = false
	if old.syncs.Len() == 0 {
		old.estim = true
		return old, nil, nil
	}
	last, _ := old.syncs.Last()
	drained = old.pend.DrainAll()
	d.pending -= len(drained)
	out = make([]Emit, 0, len(drained))
	for _, rec := range drained {
		out = append(out, Emit{Payload: rec.Payload, Wall: last.W + (rec.K - last.K), Source: Fwd})
	}
	return old, out, drained
}

// rollbackSeal 撤销一次因校验失败而不应发生的封口，并删除刚创建的新启动。
func (d *device) rollbackSeal(sealed *boot, drained []hold.Record, created *boot) {
	idx := len(d.boots) - 1
	d.boots = d.boots[:idx]
	delete(d.byID, created.id)
	if idx > 0 {
		d.maxBoot = d.boots[idx-1].id
	} else {
		d.seen = false
		d.maxBoot = 0
	}
	if sealed == nil {
		return
	}
	sealed.open = true
	sealed.estim = false
	for _, rec := range drained {
		sealed.pend.Push(rec)
	}
	d.pending += len(drained)
}

func canResolveNow(bt *boot, k int64) bool {
	if bt.syncs.Len() == 0 {
		return false
	}
	_, _, _, nextOK, _ := bt.syncs.Span(k)
	return nextOK
}

// ingest 处理目标开放启动上的一条记录：能立即确定则输出，否则入待定。
func (d *device) ingest(bt *boot, k int64, payload any) []Emit {
	if bt.syncs.Len() == 0 {
		d.hold(bt, k, payload)
		return nil
	}
	prev, prevOK, next, nextOK, exact := bt.syncs.Span(k)
	switch {
	case nextOK:
		if exact {
			return []Emit{{Payload: payload, Wall: next.W, Source: Interp}}
		}
		if prevOK {
			return []Emit{{Payload: payload, Wall: syncpt.Interp(k, prev, next), Source: Interp}}
		}
		return []Emit{{Payload: payload, Wall: next.W - (next.K - k), Source: Back}}
	default:
		d.hold(bt, k, payload)
		return nil
	}
}

func (d *device) hold(bt *boot, k int64, payload any) {
	d.seq++
	bt.pend.Push(hold.Record{K: k, Arrival: d.seq, Payload: payload})
	d.pending++
}

// release 释放目标启动内 k<=syncK 的待定记录，按 (k, 到达序) 输出。
// 考察数 <= 释放数+1，与该启动待定总数无关。
func (d *device) release(bt *boot, syncK int64) (out []Emit, examined int) {
	rs, examined := bt.pend.DrainLE(syncK)
	d.pending -= len(rs)
	out = make([]Emit, 0, len(rs))
	for _, rec := range rs {
		out = append(out, d.resolvePending(bt, rec))
	}
	return out, examined
}

func (d *device) resolvePending(bt *boot, rec hold.Record) Emit {
	prev, prevOK, next, nextOK, exact := bt.syncs.Span(rec.K)
	switch {
	case exact:
		return Emit{Payload: rec.Payload, Wall: next.W, Source: Interp}
	case prevOK && nextOK:
		return Emit{Payload: rec.Payload, Wall: syncpt.Interp(rec.K, prev, next), Source: Interp}
	case nextOK:
		return Emit{Payload: rec.Payload, Wall: next.W - (next.K - rec.K), Source: Back}
	default:
		panic("resolve: unreachable: released pending record has no next sync point")
	}
}

// estimateChain 从刚确定启动时刻 S 的启动沿出现序号向下估计连续的待估启动。
func (d *device) estimateChain(start *boot, startS int64) []Emit {
	var out []Emit
	idx := -1
	for i, bt := range d.boots {
		if bt == start {
			idx = i
			break
		}
	}
	nextS := startS
	for j := idx - 1; j >= 0; j-- {
		prev := d.boots[j]
		if !prev.estim {
			break
		}
		sj := nextS - 1 - prev.kmax
		prev.startS = sj
		prev.estim = false
		rs := prev.pend.DrainAll()
		d.pending -= len(rs)
		for _, rec := range rs {
			out = append(out, Emit{Payload: rec.Payload, Wall: sj + rec.K, Source: Est})
		}
		nextS = sj
	}
	return out
}

func (r *Resolver) lockDevice(dev int64) (*device, error) {
	r.mu.Lock()
	d, ok := r.devs[dev]
	r.mu.Unlock()
	if !ok {
		return nil, ErrNoDevice
	}
	d.mu.Lock()
	return d, nil
}
