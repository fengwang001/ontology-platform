package blockalloc

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/natlog"
)

// 朴素模型：严格按题目规则逐步独立重写，不与实现共享任何代码。

type nBlock struct {
	sub       int64
	addr, j   int
	ports     map[int]bool
	idle      bool
	idleSince int64
}

type nModel struct {
	a, l, h, s, k, m int
	t                int64
	lastNow          int64
	drained          map[int]bool
	blocks           map[[2]int]*nBlock
	logs             []natlog.Entry
	seq              int64
}

func newNModel(a, l, h, s, m int, tt int64) *nModel {
	return &nModel{
		a: a, l: l, h: h, s: s, k: (h - l + 1) / s, m: m, t: tt,
		drained: map[int]bool{},
		blocks:  map[[2]int]*nBlock{},
	}
}

func (n *nModel) blockRange(j int) (int, int) {
	lo := n.l + j*n.s
	return lo, lo + n.s - 1
}

func (n *nModel) due(now int64) []*nBlock {
	var out []*nBlock
	for _, b := range n.blocks {
		if b.idle && b.idleSince+n.t <= now {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		rx, ry := x.idleSince+n.t, y.idleSince+n.t
		if rx != ry {
			return rx < ry
		}
		if x.addr != y.addr {
			return x.addr < y.addr
		}
		return x.j < y.j
	})
	return out
}

func (n *nModel) applyDue(bs []*nBlock) {
	for _, b := range bs {
		lo, hi := n.blockRange(b.j)
		n.seq++
		n.logs = append(n.logs, natlog.Entry{
			Seq: n.seq, Kind: natlog.Free, Sub: b.sub,
			Addr: b.addr, Lo: lo, Hi: hi, At: b.idleSince + n.t,
		})
		delete(n.blocks, [2]int{b.addr, b.j})
	}
}

func (n *nModel) blocksInAllocOrder(sub int64) []*nBlock {
	order := map[[2]int]int64{}
	for _, e := range n.logs {
		if e.Kind == natlog.Alloc {
			order[[2]int{e.Addr, (e.Lo - n.l) / n.s}] = e.Seq
		}
	}
	var out []*nBlock
	for _, b := range n.blocks {
		if b.sub == sub {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return order[[2]int{out[i].addr, out[i].j}] < order[[2]int{out[j].addr, out[j].j}]
	})
	return out
}

func (n *nModel) bindCount(addr int, released map[*nBlock]bool) int {
	subs := map[int64]bool{}
	for _, b := range n.blocks {
		if b.addr == addr && !released[b] {
			subs[b.sub] = true
		}
	}
	return len(subs)
}

func (n *nModel) firstFree(addr int, released map[*nBlock]bool) int {
	for j := 0; j < n.k; j++ {
		b := n.blocks[[2]int{addr, j}]
		if b == nil || released[b] {
			return j
		}
	}
	return -1
}

type nResult struct {
	addr, port int
	err        error
}

func (n *nModel) open(sub int64, now int64) nResult {
	if sub < 1 || sub > 1_000_000_000 || now < 0 || now > 1_000_000_000_000 {
		return nResult{err: ErrInvalidArgument}
	}
	if now < n.lastNow {
		return nResult{err: ErrClockWarp}
	}
	due := n.due(now)
	rel := map[*nBlock]bool{}
	for _, b := range due {
		rel[b] = true
	}

	for _, b := range n.blocksInAllocOrder(sub) {
		if rel[b] || len(b.ports) >= n.s {
			continue
		}
		lo, _ := n.blockRange(b.j)
		port := lo
		for b.ports[port] {
			port++
		}
		n.applyDue(due)
		nb := n.blocks[[2]int{b.addr, b.j}]
		nb.ports[port] = true
		nb.idle = false
		n.lastNow = now
		return nResult{addr: nb.addr, port: port}
	}

	effBlocks, boundAddr := 0, -1
	for _, b := range n.blocks {
		if b.sub == sub && !rel[b] {
			effBlocks++
			boundAddr = b.addr
		}
	}

	addr := -1
	if boundAddr >= 0 {
		if effBlocks >= n.m {
			return nResult{err: ErrBlockLimit}
		}
		if n.drained[boundAddr] {
			return nResult{err: ErrAddrDrained}
		}
		if n.firstFree(boundAddr, rel) < 0 {
			return nResult{err: ErrAddrExhausted}
		}
		addr = boundAddr
	} else {
		bestAddr, bestBound := -1, 0
		for a := 0; a < n.a; a++ {
			if n.drained[a] || n.firstFree(a, rel) < 0 {
				continue
			}
			cnt := n.bindCount(a, rel)
			if bestAddr < 0 || cnt < bestBound {
				bestAddr, bestBound = a, cnt
			}
		}
		if bestAddr < 0 {
			return nResult{err: ErrPoolExhausted}
		}
		addr = bestAddr
	}

	n.applyDue(due)
	j := n.firstFree(addr, map[*nBlock]bool{})
	lo, hi := n.blockRange(j)
	n.seq++
	n.logs = append(n.logs, natlog.Entry{
		Seq: n.seq, Kind: natlog.Alloc, Sub: sub, Addr: addr, Lo: lo, Hi: hi, At: now,
	})
	n.blocks[[2]int{addr, j}] = &nBlock{
		sub: sub, addr: addr, j: j, ports: map[int]bool{lo: true},
	}
	n.lastNow = now
	return nResult{addr: addr, port: lo}
}

func (n *nModel) close(sub int64, addr, port int, now int64) error {
	if sub < 1 || sub > 1_000_000_000 || now < 0 || now > 1_000_000_000_000 ||
		addr < 0 || addr >= n.a || port < n.l || port > n.h {
		return ErrInvalidArgument
	}
	if now < n.lastNow {
		return ErrClockWarp
	}
	due := n.due(now)
	rel := map[*nBlock]bool{}
	for _, b := range due {
		rel[b] = true
	}
	j := (port - n.l) / n.s
	b := n.blocks[[2]int{addr, j}]
	if b == nil || b.sub != sub || rel[b] || !b.ports[port] {
		return ErrNoSession
	}
	n.applyDue(due)
	nb := n.blocks[[2]int{addr, j}]
	delete(nb.ports, port)
	if len(nb.ports) == 0 {
		nb.idle = true
		nb.idleSince = now
	}
	n.lastNow = now
	return nil
}

func (n *nModel) drain(addr int, now int64) error {
	if addr < 0 || addr >= n.a || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if now < n.lastNow {
		return ErrClockWarp
	}
	n.applyDue(n.due(now))
	n.drained[addr] = true
	n.lastNow = now
	return nil
}

func sameErr(a, b error) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return errors.Is(a, b)
	}
}

func reason(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrClockWarp):
		return "clock-warp"
	case errors.Is(err, ErrBlockLimit):
		return "block-limit"
	case errors.Is(err, ErrAddrDrained):
		return "addr-drained"
	case errors.Is(err, ErrAddrExhausted):
		return "addr-exhausted"
	case errors.Is(err, ErrPoolExhausted):
		return "pool-exhausted"
	case errors.Is(err, ErrNoSession):
		return "no-session"
	default:
		return err.Error()
	}
}

type opKind int

const (
	opOpen opKind = iota
	opClose
	opDrain
)

type op struct {
	kind       opKind
	sub        int64
	addr, port int
	now        int64
}

func (o op) String() string {
	switch o.kind {
	case opOpen:
		return fmt.Sprintf("Open(%d,%d)", o.sub, o.now)
	case opClose:
		return fmt.Sprintf("Close(%d,%d,%d,%d)", o.sub, o.addr, o.port, o.now)
	default:
		return fmt.Sprintf("Drain(%d,%d)", o.addr, o.now)
	}
}

// naiveLookup 仅凭日志重放回答 t 时刻端口归属。
func naiveLookup(logs []natlog.Entry, addr, port int, t int64) (int64, error) {
	var owner int64 = -1
	for _, e := range logs {
		if e.Addr != addr || port < e.Lo || port > e.Hi {
			continue
		}
		if e.Kind == natlog.Alloc && e.At <= t {
			owner = e.Sub
		}
		if e.Kind == natlog.Free && e.At <= t {
			owner = -1
		}
	}
	if owner < 0 {
		return 0, natlog.ErrUnallocated
	}
	return owner, nil
}

func TestRandomAgainstNaive(t *testing.T) {
	const total = 1500
	rng := rand.New(rand.NewSource(20261005))
	for c := 0; c < total; c++ {
		a := 1 + rng.Intn(3)
		s := []int{8, 16, 32}[rng.Intn(3)]
		k := 1 + rng.Intn(4)
		l := 1024
		h := l + k*s - 1
		m := 1 + rng.Intn(3)
		tt := int64(rng.Intn(8))
		nSubs := 2 + rng.Intn(6)

		al, err := New(a, l, h, s, m, tt)
		if err != nil {
			t.Fatalf("case %d New: %v", c, err)
		}
		nm := newNModel(a, l, h, s, m, tt)

		var trace strings.Builder
		now := int64(0)
		steps := 40 + rng.Intn(80)
		for step := 0; step < steps; step++ {
			now += int64(rng.Intn(4))
			o := op{now: now}
			switch r := rng.Float64(); {
			case r < 0.6:
				o.kind = opOpen
				o.sub = int64(1 + rng.Intn(nSubs))
			case r < 0.9:
				o.kind = opClose
				o.sub = int64(1 + rng.Intn(nSubs))
				o.addr = rng.Intn(a)
				o.port = l + rng.Intn(k*s)
			default:
				o.kind = opDrain
				o.addr = rng.Intn(a)
			}
			// 约 5% 注入非法参数或时钟回退。
			if rng.Intn(20) == 0 {
				switch rng.Intn(3) {
				case 0:
					o.sub = 0
					o.kind = opOpen
				case 1:
					o.now = now - int64(1+rng.Intn(5))
				case 2:
					o.port = h + 10
					o.kind = opClose
				}
			}

			fmt.Fprint(&trace, o)
			switch o.kind {
			case opOpen:
				ga, gp, ge := al.Open(o.sub, o.now)
				nr := nm.open(o.sub, o.now)
				if !sameErr(ge, nr.err) || (ge == nil && (ga != nr.addr || gp != nr.port)) {
					t.Fatalf("case %d MISMATCH %s\n got=(%d,%d,%s)\nwant=(%d,%d,%s)\ntrace:\n%s",
						c, o, ga, gp, reason(ge), nr.addr, nr.port, reason(nr.err), trace.String())
				}
				fmt.Fprintf(&trace, " => (%d,%d,%s)\n", ga, gp, reason(ge))
			case opClose:
				ge := al.Close(o.sub, o.addr, o.port, o.now)
				ne := nm.close(o.sub, o.addr, o.port, o.now)
				if !sameErr(ge, ne) {
					t.Fatalf("case %d MISMATCH %s got=%s want=%s\ntrace:\n%s",
						c, o, reason(ge), reason(ne), trace.String())
				}
				fmt.Fprintf(&trace, " => %s\n", reason(ge))
			case opDrain:
				ge := al.Drain(o.addr, o.now)
				ne := nm.drain(o.addr, o.now)
				if !sameErr(ge, ne) {
					t.Fatalf("case %d MISMATCH %s got=%s want=%s\ntrace:\n%s",
						c, o, reason(ge), reason(ne), trace.String())
				}
				fmt.Fprintf(&trace, " => %s\n", reason(ge))
			}

			gs := al.Log().Entries()
			if len(gs) != len(nm.logs) {
				t.Fatalf("case %d log len %d!=%d\ntrace:\n%s", c, len(gs), len(nm.logs), trace.String())
			}
			for i := range gs {
				if gs[i] != nm.logs[i] {
					t.Fatalf("case %d log[%d] %+v != %+v\ntrace:\n%s", c, i, gs[i], nm.logs[i], trace.String())
				}
			}

			// 抽样 Lookup：实现的二分查询 vs 朴素日志重放。
			maxNow := nm.lastNow
			if maxNow > 0 && rng.Intn(3) == 0 {
				la := rng.Intn(a)
				lp := l + rng.Intn(k*s)
				lt := maxNow - int64(rng.Intn(int(maxNow)+1))
				gsub, gerr := al.Log().Lookup(la, lp, lt)
				nsub, nerr := naiveLookup(nm.logs, la, lp, lt)
				if !sameErr(gerr, nerr) || (gerr == nil && gsub != nsub) {
					t.Fatalf("case %d Lookup(%d,%d,%d) got=(%d,%v) want=(%d,%v)\ntrace:\n%s",
						c, la, lp, lt, gsub, gerr, nsub, nerr, trace.String())
				}
			}
		}
		if c < 3 && testing.Verbose() {
			t.Logf("case %d (a=%d s=%d k=%d m=%d T=%d) trace:\n%s", c, a, s, k, m, tt, trace.String())
		}
	}
}

// TestProbesIndependentOfSubs：同样几何与操作下，100 与 10000 订户档位
// 的 probes 完全一致，且每次 Open 不超过 M+A+K 加本次落地释放数。
func TestProbesIndependentOfSubs(t *testing.T) {
	type pop struct {
		sub     int64
		now     int64
		doClose bool
	}
	rng := rand.New(rand.NewSource(424242))
	var pops []pop
	nowGen := int64(0)
	for step := 0; step < 400; step++ {
		nowGen += int64(rng.Intn(3))
		pops = append(pops, pop{
			sub:     int64(1 + rng.Intn(10000)),
			now:     nowGen,
			doClose: rng.Intn(2) == 0,
		})
	}

	run := func(nSubs int64) []int64 {
		const a, l, s, m = 4, 1024, 16, 4
		h := l + 8*s - 1 // K=8
		const tt = int64(5)
		al, err := New(a, l, h, s, m, tt)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		prevFree := 0
		for step, p := range pops {
			// 两档都只使用 1..6 号订户，编号基数随档位平移，行为完全一致。
			sub := 1 + (p.sub-1)%6
			ad, pt, oerr := al.Open(sub, p.now)
			nFree := 0
			for _, e := range al.Log().Entries() {
				if e.Kind == natlog.Free {
					nFree++
				}
			}
			released := nFree - prevFree
			prevFree = nFree
			if oerr == nil {
				pr := al.Probes()
				got = append(got, pr)
				bound := int64(m + a + (h-l+1)/s + released)
				if pr > bound {
					t.Fatalf("nSubs=%d step=%d probes=%d > M+A+K+released=%d",
						nSubs, step, pr, bound)
				}
			}
			if oerr == nil && p.doClose {
				if cerr := al.Close(sub, ad, pt, p.now); cerr != nil {
					t.Fatalf("close: %v", cerr)
				}
			}
		}
		return got
	}

	g100 := run(100)
	g10000 := run(10000)
	if len(g100) != len(g10000) {
		t.Fatalf("accepted open counts differ: %d vs %d", len(g100), len(g10000))
	}
	for i := range g100 {
		if g100[i] != g10000[i] {
			t.Fatalf("probes differ at %d: 100 subs=%d, 10000 subs=%d", i, g100[i], g10000[i])
		}
	}
	if testing.Verbose() {
		t.Logf("probes across %d accepted opens (identical for 100 and 10000 subs): %v",
			len(g100), g100)
	}
}
