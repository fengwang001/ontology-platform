package resolve

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// 朴素模拟器：完全独立于 syncpt/hold 包，用切片逐项重算规格语义。
// 仅用于与正式实现做差分对照。

type nPoint struct{ k, w int64 }

type nRec struct {
	k, arrival int64
	payload    string
}

type nBoot struct {
	id      int64
	syncs   []nPoint
	pending []nRec
	kmax    int64
	open    bool
	estim   bool
	s       int64
}

type nDevice struct {
	registered bool
	pmax       int
	pending    int
	seq        int64
	boots      []*nBoot // 按 id 升序
}

type naive struct {
	devs map[int64]*nDevice
}

func newNaive() *naive { return &naive{devs: map[int64]*nDevice{}} }

type nOp struct {
	kind         byte
	dev, b, k, w int64
	payload      string
}

type nEmit struct {
	payload string
	wall    int64
	source  Source
}

func (n *naive) logOp(o nOp) string {
	if o.kind == 'R' {
		return fmt.Sprintf("Record(dev=%d,b=%d,k=%d,p=%q)", o.dev, o.b, o.k, o.payload)
	}
	return fmt.Sprintf("Sync(dev=%d,b=%d,k=%d,W=%d)", o.dev, o.b, o.k, o.w)
}

func validBootNaive(b int64) bool { return b >= 1 && b <= 1_000_000 }
func validKNaive(k int64) bool    { return k >= 0 && k <= 1_000_000_000 }
func validWNaive(w int64) bool    { return w >= 0 && w <= 1_000_000_000_000_000 }

func (n *naive) Register(dev int64, pmax int) error {
	if pmax < 1 || pmax > 1_000_000 {
		return ErrInvalid
	}
	d := n.devs[dev]
	if d != nil {
		return ErrInvalid
	}
	n.devs[dev] = &nDevice{registered: true, pmax: pmax}
	return nil
}

func (n *naive) getBoot(d *nDevice, b int64) *nBoot {
	i := sort.Search(len(d.boots), func(i int) bool { return d.boots[i].id >= b })
	if i < len(d.boots) && d.boots[i].id == b {
		return d.boots[i]
	}
	return nil
}

func (d *nDevice) maxSeen() (int64, bool) {
	if len(d.boots) == 0 {
		return 0, false
	}
	return d.boots[len(d.boots)-1].id, true
}

func interpNaive(k int64, p, q nPoint) int64 {
	num := new(big.Int).Mul(big.NewInt(k-p.k), big.NewInt(q.w-p.w))
	num.Quo(num, big.NewInt(q.k-p.k))
	return p.w + num.Int64()
}

func spanNaive(pts []nPoint, k int64) (prev *nPoint, next *nPoint, exact bool) {
	i := sort.Search(len(pts), func(i int) bool { return pts[i].k >= k })
	if i < len(pts) {
		next = &pts[i]
		if pts[i].k == k {
			return next, next, true
		}
	}
	if i > 0 {
		prev = &pts[i-1]
	}
	return
}

func sortPending(rs []nRec) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].k != rs[j].k {
			return rs[i].k < rs[j].k
		}
		return rs[i].arrival < rs[j].arrival
	})
}

// step 执行一步，返回输出、错误与判定依据说明。
func (n *naive) step(o nOp) ([]nEmit, error, string) {
	// 校验顺序：invalid > nodevice > sealed > dup > skew > full
	if o.kind == 'R' {
		if !validBootNaive(o.b) || !validKNaive(o.k) {
			return nil, ErrInvalid, "invalid arg"
		}
	} else {
		if !validBootNaive(o.b) || !validKNaive(o.k) || !validWNaive(o.w) {
			return nil, ErrInvalid, "invalid arg"
		}
	}
	d := n.devs[o.dev]
	if d == nil || !d.registered {
		return nil, ErrNoDevice, "device not registered"
	}

	maxB, seen := d.maxSeen()
	bt := n.getBoot(d, o.b)
	if (bt != nil && !bt.open) || (bt == nil && seen && o.b < maxB) {
		return nil, ErrSealed, "boot sealed"
	}

	// 计算封口效果（仅预演，用于 ErrFull 判定与后续落地）。
	var sealedBoot *nBoot
	var fwd []nEmit
	var drained []nRec
	created := false
	if bt == nil {
		// 触发封口当前开放启动
		if seen {
			old := d.boots[len(d.boots)-1]
			sealedBoot = old
			if len(old.syncs) > 0 {
				last := old.syncs[len(old.syncs)-1]
				drained = append([]nRec(nil), old.pending...)
				sortPending(drained)
				for _, r := range drained {
					fwd = append(fwd, nEmit{r.payload, last.w + (r.k - last.k), Fwd})
				}
			}
		}
		bt = &nBoot{id: o.b, open: true, kmax: -1}
		created = true
	}

	if o.kind == 'S' {
		// dup/skew 检查（先于任何落地）
		i := sort.Search(len(bt.syncs), func(i int) bool { return bt.syncs[i].k >= o.k })
		var dup, skew bool
		if i < len(bt.syncs) && bt.syncs[i].k == o.k {
			dup = true
		}
		if !dup {
			if i > 0 && bt.syncs[i-1].w >= o.w {
				skew = true
			}
			if i < len(bt.syncs) && o.w >= bt.syncs[i].w {
				skew = true
			}
		}
		if dup {
			if created {
				n.abandonCreated(d, bt)
			}
			return nil, ErrDupSync, "dup sync k"
		}
		if skew {
			if created {
				n.abandonCreated(d, bt)
			}
			return nil, ErrSkew, "W skew against neighbor"
		}
	}

	// ErrFull（仅 Record 且必须进入待定；按封口之后的待定数判定）
	if o.kind == 'R' {
		willResolve := false
		if _, nxt, _ := spanNaive(bt.syncs, o.k); nxt != nil {
			willResolve = true
		}
		pendingAfterSeal := d.pending - len(drained)
		if !willResolve && pendingAfterSeal >= d.pmax {
			if created {
				n.abandonCreated(d, bt)
			}
			return nil, ErrFull, fmt.Sprintf("pending %d >= pmax %d after seal", pendingAfterSeal, d.pmax)
		}
	}

	// 正式落地
	if sealedBoot != nil {
		old := sealedBoot
		old.open = false
		if len(old.syncs) == 0 {
			old.estim = true
		} else {
			old.pending = nil
			d.pending -= len(drained)
		}
	}
	if created {
		d.boots = append(d.boots, bt)
		sort.Slice(d.boots, func(i, j int) bool { return d.boots[i].id < d.boots[j].id })
	}

	var out []nEmit
	out = append(out, fwd...)

	if o.kind == 'R' {
		if o.k > bt.kmax {
			bt.kmax = o.k
		}
		if prev, nxt, exact := spanNaive(bt.syncs, o.k); nxt != nil {
			switch {
			case exact:
				out = append(out, nEmit{o.payload, nxt.w, Interp})
			case prev != nil:
				out = append(out, nEmit{o.payload, interpNaive(o.k, *prev, *nxt), Interp})
			default:
				out = append(out, nEmit{o.payload, nxt.w - (nxt.k - o.k), Back})
			}
		} else {
			d.seq++
			bt.pending = append(bt.pending, nRec{k: o.k, arrival: d.seq, payload: o.payload})
			d.pending++
		}
		return out, nil, n.reason(o, "record")
	}

	// Sync 落地
	firstSync := len(bt.syncs) == 0
	bt.syncs = append(bt.syncs, nPoint{o.k, o.w})
	sort.Slice(bt.syncs, func(i, j int) bool { return bt.syncs[i].k < bt.syncs[j].k })

	var rel []nRec
	kept := bt.pending[:0]
	for _, r := range bt.pending {
		if r.k <= o.k {
			rel = append(rel, r)
		} else {
			kept = append(kept, r)
		}
	}
	bt.pending = kept
	sortPending(rel)
	d.pending -= len(rel)
	for _, r := range rel {
		prev, nxt, exact := spanNaive(bt.syncs, r.k)
		switch {
		case exact:
			out = append(out, nEmit{r.payload, nxt.w, Interp})
		case prev != nil:
			out = append(out, nEmit{r.payload, interpNaive(r.k, *prev, *nxt), Interp})
		default:
			out = append(out, nEmit{r.payload, nxt.w - (nxt.k - r.k), Back})
		}
	}

	if firstSync {
		startS := o.w - o.k
		bt.s = startS
		idx := sort.Search(len(d.boots), func(i int) bool { return d.boots[i].id >= bt.id })
		nextS := startS
		for j := idx - 1; j >= 0; j-- {
			pb := d.boots[j]
			if !pb.estim {
				break
			}
			sj := nextS - 1 - pb.kmax
			pb.s = sj
			pb.estim = false
			rs := append([]nRec(nil), pb.pending...)
			pb.pending = nil
			d.pending -= len(rs)
			sortPending(rs)
			for _, r := range rs {
				out = append(out, nEmit{r.payload, sj + r.k, Est})
			}
			nextS = sj
		}
	}
	return out, nil, n.reason(o, "sync")
}

func (n *naive) abandonCreated(d *nDevice, bt *nBoot) {
	// 预演阶段并未真正插入；若封口了旧启动需还原。
	if len(d.boots) > 0 {
		old := d.boots[len(d.boots)-1]
		if old.id < bt.id {
			old.open = true
			old.estim = false
		}
	}
}

func (n *naive) reason(o nOp, tag string) string {
	var b strings.Builder
	b.WriteString(tag)
	b.WriteString(" ok; order=seal(Fwd), direct/release(Interp|Back), estimate(Est desc)")
	return b.String()
}

func emitsToNaive(es []Emit) []nEmit {
	out := make([]nEmit, len(es))
	for i, e := range es {
		out[i] = nEmit{e.Payload.(string), e.Wall, e.Source}
	}
	return out
}

func formatNaive(es []nEmit) string {
	var parts []string
	for _, e := range es {
		parts = append(parts, fmt.Sprintf("(%s,%d,%s)", e.payload, e.wall, e.source))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

var _ = strconv.Itoa
