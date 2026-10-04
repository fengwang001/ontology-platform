package blockalloc

import (
	"errors"
	"math/bits"
	"sort"
	"sync"

	"ontology/natlog"
	"ontology/pubpool"
)

var errInvalid = errors.New("invalid argument")
var errWarp = errors.New("clock moved backwards")
var errLimit = errors.New("subscriber block limit reached")
var errDrained = errors.New("bound address drained")
var errExhausted = errors.New("bound address exhausted")
var errPool = errors.New("pool exhausted")
var errNoSession = errors.New("no such session")

// 各类错误可用 errors.Is 区分。
var (
	ErrInvalidArgument = errInvalid
	ErrClockWarp       = errWarp
	ErrBlockLimit      = errLimit
	ErrAddrDrained     = errDrained
	ErrAddrExhausted   = errExhausted
	ErrPoolExhausted   = errPool
	ErrNoSession       = errNoSession
)

type block struct {
	addr  int
	j     int
	lo    int
	hi    int
	sub   int64
	ports []uint64 // 会话在位端口位图（块内偏移），每字 64 端口
	idle  bool
	since int64
}

type subscriber struct {
	addr   int
	blocks []*block // 按分配先后
}

type pendingFree struct {
	b  *block
	at int64
}

// Allocator 是运营商级端口块分配器。所有方法可并发调用。
type Allocator struct {
	mu      sync.Mutex
	pool    *pubpool.Pool
	log     *natlog.Logger
	subs    map[int64]*subscriber
	m       int
	t       int64
	lastNow int64
	bound   []int // 每地址绑定订户数（与 blocks 一致维护）
	probes  int64 // 最近一次 Open 的考察计数
}

func New(a, l, h, s, m int, t int64) (*Allocator, error) {
	if m < 1 || m > 8 || t < 0 || t > 1_000_000_000 {
		return nil, errInvalid
	}
	p, err := pubpool.New(a, l, h, s)
	if err != nil {
		return nil, errInvalid
	}
	return &Allocator{
		pool:  p,
		log:   natlog.New(),
		subs:  make(map[int64]*subscriber),
		m:     m,
		t:     t,
		bound: make([]int, a),
	}, nil
}

func (al *Allocator) Log() *natlog.Logger { return al.log }
func (al *Allocator) Pool() *pubpool.Pool { return al.pool }

// Probes 返回最近一次 Open 考察的订户/块/地址记录数与落地释放数之和。
func (al *Allocator) Probes() int64 {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.probes
}

func validSub(sub int64) bool { return sub >= 1 && sub <= 1_000_000_000 }
func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

// dueFrees 收集 releaseAt<=now 的空闲块（假想释放），按（时刻,地址,块号）排序。
func (al *Allocator) dueFrees(now int64) []pendingFree {
	var out []pendingFree
	for _, sr := range al.subs {
		for _, b := range sr.blocks {
			if b.idle && b.since+al.t <= now {
				out = append(out, pendingFree{b: b, at: b.since + al.t})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.at != y.at {
			return x.at < y.at
		}
		if x.b.addr != y.b.addr {
			return x.b.addr < y.b.addr
		}
		return x.b.j < y.b.j
	})
	return out
}

// commitFrees 把假想释放落地：写 FREE 日志、清块位图、解绑无块订户。
func (al *Allocator) commitFrees(pending []pendingFree) {
	for _, p := range pending {
		b := p.b
		sr := al.subs[b.sub]
		al.log.Append(natlog.Free, b.sub, b.addr, b.lo, b.hi, p.at)
		al.pool.MarkFree(b.addr, b.j)
		al.removeBlock(sr, b)
	}
}

func (al *Allocator) removeBlock(sr *subscriber, b *block) {
	idx := -1
	for i, x := range sr.blocks {
		if x == b {
			idx = i
			break
		}
	}
	if idx >= 0 {
		sr.blocks = append(sr.blocks[:idx], sr.blocks[idx+1:]...)
	}
	if len(sr.blocks) == 0 {
		al.bound[sr.addr]--
		delete(al.subs, b.sub)
	}
}

func (al *Allocator) isPending(pending []pendingFree, b *block) bool {
	for _, p := range pending {
		if p.b == b {
			return true
		}
	}
	return false
}

// effBound 返回假想释放后地址 a 的绑定订户数。
func (al *Allocator) effBound(a int, pending []pendingFree) int {
	n := al.bound[a]
	seen := map[int64]bool{}
	for _, p := range pending {
		if p.b.addr != a {
			continue
		}
		sr := al.subs[p.b.sub]
		if sr == nil || seen[p.b.sub] {
			continue
		}
		last := true
		for _, x := range sr.blocks {
			if x != p.b && !al.isPending(pending, x) {
				last = false
				break
			}
		}
		if last {
			seen[p.b.sub] = true
			n--
		}
	}
	return n
}

// effFirstFree 返回假想释放后地址 a 编号最小的空闲块；无则 -1。
func (al *Allocator) effFirstFree(a int, pending []pendingFree) int {
	rel := map[int]bool{}
	for _, p := range pending {
		if p.b.addr == a {
			rel[p.b.j] = true
		}
	}
	for j := 0; j < al.pool.K(); j++ {
		if rel[j] || !al.pool.IsUsed(a, j) {
			return j
		}
	}
	return -1
}

func (al *Allocator) Open(sub int64, now int64) (int, int, error) {
	al.mu.Lock()
	defer al.mu.Unlock()

	if !validSub(sub) || !validNow(now) {
		return 0, 0, errInvalid
	}
	if now < al.lastNow {
		return 0, 0, errWarp
	}

	pending := al.dueFrees(now)
	var probes int64
	defer func() { al.probes = probes }()

	sr := al.subs[sub]
	if sr != nil {
		probes++ // 订户记录
	}

	// 1) 现有块中按分配先后找第一个有空闲端口的块。
	if sr != nil {
		for _, b := range sr.blocks {
			if al.isPending(pending, b) {
				continue
			}
			probes++ // 块记录
			if off, ok := b.firstFree(al.pool.S()); ok {
				al.commitFrees(pending)
				probes += int64(len(pending))
				al.lastNow = now
				al.log.Touch(now)
				b.markUsed(off)
				b.idle = false
				return b.addr, b.lo + off, nil
			}
		}
	}

	// 2) 需要新块。拒绝路径同样计入落地释放数（假想考察）。
	// 订户原有块全部到期时，假想状态下其已解绑，按无绑定选址。
	var addr int
	effBlocks := 0
	if sr != nil {
		effBlocks = len(sr.blocks)
		for _, p := range pending {
			if p.b.sub == sub {
				effBlocks--
			}
		}
	}
	switch {
	case sr != nil && effBlocks > 0:
		if effBlocks >= al.m {
			probes += int64(len(pending))
			return 0, 0, errLimit
		}
		addr = sr.addr
		if al.pool.Drained(addr) {
			probes += int64(len(pending))
			return 0, 0, errDrained
		}
		if al.effFirstFree(addr, pending) < 0 {
			probes += int64(len(pending))
			return 0, 0, errExhausted
		}
	default:
		bestAddr, bestBound := -1, 0
		for a := 0; a < al.pool.A(); a++ {
			probes++ // 地址记录
			if al.pool.Drained(a) {
				continue
			}
			j := al.effFirstFree(a, pending)
			if j < 0 {
				continue
			}
			n := al.effBound(a, pending)
			if bestAddr < 0 || n < bestBound {
				bestAddr, bestBound = a, n
			}
		}
		if bestAddr < 0 {
			probes += int64(len(pending))
			return 0, 0, errPool
		}
		addr = bestAddr
	}

	// 提交假想释放，再取新块。
	al.commitFrees(pending)
	probes += int64(len(pending))

	// commitFrees 可能解绑了本订户（其旧块全部到期），重新取记录。
	sr = al.subs[sub]
	j := al.pool.FirstFree(addr)
	lo, hi := al.pool.BlockRange(j)
	nb := newBlock(addr, j, lo, hi, sub, al.pool.S())
	nb.markUsed(0)
	al.pool.MarkUsed(addr, j)
	al.log.Append(natlog.Alloc, sub, addr, lo, hi, now)

	if sr == nil {
		sr = &subscriber{addr: addr}
		al.subs[sub] = sr
	}
	sr.blocks = append(sr.blocks, nb)
	al.bound[addr]++

	al.lastNow = now
	al.log.Touch(now)
	return addr, lo, nil
}

func newBlock(addr, j, lo, hi int, sub int64, s int) *block {
	return &block{
		addr:  addr,
		j:     j,
		lo:    lo,
		hi:    hi,
		sub:   sub,
		ports: make([]uint64, (s+63)/64),
	}
}

// firstFree 返回块内最小空闲端口偏移。
func (b *block) firstFree(s int) (int, bool) {
	fullWords := s / 64
	for w := 0; w < fullWords; w++ {
		if b.ports[w] != ^uint64(0) {
			return w*64 + bits.TrailingZeros64(^b.ports[w]), true
		}
	}
	if rem := s & 63; rem != 0 {
		v := b.ports[fullWords]
		mask := ^uint64(0) << uint(rem)
		if v|mask != ^uint64(0) {
			return fullWords*64 + bits.TrailingZeros64(^(v | mask)), true
		}
	}
	return 0, false
}

func (b *block) markUsed(off int) { b.ports[off>>6] |= 1 << uint(off&63) }

func (b *block) markFree(s, off int) bool {
	b.ports[off>>6] &^= 1 << uint(off&63)
	for w := range b.ports {
		if b.ports[w] != 0 {
			return false
		}
	}
	return true
}

func (b *block) inUse(off int) bool {
	return b.ports[off>>6]&(1<<uint(off&63)) != 0
}

func (al *Allocator) Close(sub int64, addr, port int, now int64) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if !validSub(sub) || !validNow(now) ||
		addr < 0 || addr >= al.pool.A() ||
		port < al.pool.L() || port > al.pool.H() {
		return errInvalid
	}
	if now < al.lastNow {
		return errWarp
	}

	pending := al.dueFrees(now)

	sr := al.subs[sub]
	var target *block
	off := 0
	if sr != nil && sr.addr == addr {
		j := al.pool.BlockOf(port)
		for _, b := range sr.blocks {
			if al.isPending(pending, b) {
				continue
			}
			if b.j == j {
				target = b
				off = port - b.lo
				break
			}
		}
	}
	if target == nil || !target.inUse(off) {
		return errNoSession
	}

	al.commitFrees(pending)

	if target.markFree(al.pool.S(), off) {
		target.idle = true
		target.since = now
	}
	al.lastNow = now
	al.log.Touch(now)
	return nil
}

func (al *Allocator) Drain(addr int, now int64) error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if addr < 0 || addr >= al.pool.A() || !validNow(now) {
		return errInvalid
	}
	if now < al.lastNow {
		return errWarp
	}

	pending := al.dueFrees(now)
	al.commitFrees(pending)
	if err := al.pool.Drain(addr); err != nil {
		return errInvalid
	}
	al.lastNow = now
	al.log.Touch(now)
	return nil
}
