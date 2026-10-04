package yard

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/appt"
)

// 朴素模型：每次派位把全部等待车按优先序全量排序后逐车扫描，
// 与 README 规格一一对应；与索引堆实现做黑盒对照。

type nSlot struct {
	kind   Kind
	s      int64
	voided bool
}

type nWaiter struct {
	truck  string
	kind   Kind
	seq    int64
	ciTime int64
	s      int64
	ontime bool
}

type naive struct {
	cfg                 appt.Config
	lastNow             int64
	nextSeq             int64
	active              map[string]nSlot
	quota               map[[3]any]int
	dockKind            map[string]Kind
	freeDry, freeReefer []string
	occupant            map[string]string // dock -> truck
	serving             map[string]string // truck -> dock
	seen                map[string]bool   // 签到过
	waiting             map[string]*nWaiter
}

func newNaive(cfg appt.Config) *nWaiterHolder {
	return &nWaiterHolder{n: &naive{
		cfg: cfg, active: map[string]nSlot{}, quota: map[[3]any]int{},
		dockKind: map[string]Kind{}, occupant: map[string]string{},
		serving: map[string]string{}, seen: map[string]bool{},
		waiting: map[string]*nWaiter{},
	}}
}

type nWaiterHolder struct{ n *naive }

type rec struct {
	o    op
	out  []Assignment
	err  error
	nout []Assignment
	nerr error
}

type op struct {
	kind  int // 0 book 1 adddock 2 checkin 3 depart
	truck string
	dock  string
	k     Kind
	s     int64
	now   int64
}

func validNaiveID(x string) bool { return len(x) >= 1 && len(x) <= 32 }

func (h *nWaiterHolder) apply(o op) ([]Assignment, error) {
	n := h.n
	switch o.kind {
	case 0:
		if !validNaiveID(o.truck) || (o.k != Dry && o.k != Reefer) || o.s < 0 ||
			o.s%n.cfg.S != 0 || o.s < o.now {
			return nil, appt.ErrInvalid
		}
		if o.now < n.lastNow {
			return nil, appt.ErrClock
		}
		if _, ok := n.active[o.truck]; ok {
			return nil, appt.ErrDuplicate
		}
		key := [3]any{o.s, int(o.k), ""}
		_ = key
		var kk [2]any
		_ = kk
		if n.quota[qkey(o.s, o.k)] >= n.cfg.K {
			return nil, appt.ErrCapacity
		}
		n.active[o.truck] = nSlot{kind: o.k, s: o.s}
		n.quota[qkey(o.s, o.k)]++
		n.lastNow = o.now
		return nil, nil
	case 1:
		if !validNaiveID(o.dock) || (o.k != Dry && o.k != Reefer) {
			return nil, appt.ErrInvalid
		}
		if o.now < n.lastNow {
			return nil, appt.ErrClock
		}
		if _, ok := n.dockKind[o.dock]; ok {
			return nil, appt.ErrDuplicate
		}
		n.dockKind[o.dock] = o.k
		if o.k == Dry {
			n.freeDry = append(n.freeDry, o.dock)
		} else {
			n.freeReefer = append(n.freeReefer, o.dock)
		}
		n.lastNow = o.now
		return n.dispatch(o.now), nil
	case 2:
		if !validNaiveID(o.truck) || (o.k != Dry && o.k != Reefer) {
			return nil, appt.ErrInvalid
		}
		if o.now < n.lastNow {
			return nil, appt.ErrClock
		}
		if n.seen[o.truck] {
			return nil, appt.ErrState
		}
		ontime := false
		var s int64
		if sl, ok := n.active[o.truck]; ok {
			if sl.kind != o.k {
				return nil, appt.ErrState
			}
			if o.now >= sl.s-n.cfg.E && o.now <= sl.s+n.cfg.L {
				ontime = true
				s = sl.s
				delete(n.active, o.truck) // 消耗，不退名额
			} else {
				delete(n.active, o.truck) // 作废，不退名额
			}
		}
		n.nextSeq++
		n.waiting[o.truck] = &nWaiter{
			truck: o.truck, kind: o.k, seq: n.nextSeq,
			ciTime: o.now, s: s, ontime: ontime,
		}
		n.seen[o.truck] = true
		n.lastNow = o.now
		return n.dispatch(o.now), nil
	case 3:
		if !validNaiveID(o.truck) {
			return nil, appt.ErrInvalid
		}
		if o.now < n.lastNow {
			return nil, appt.ErrClock
		}
		dock, ok := n.serving[o.truck]
		if !n.seen[o.truck] {
			return nil, appt.ErrNotFound
		}
		if !ok {
			return nil, appt.ErrState
		}
		k := n.dockKind[dock]
		if k == Dry {
			n.freeDry = append(n.freeDry, dock)
		} else {
			n.freeReefer = append(n.freeReefer, dock)
		}
		delete(n.occupant, dock)
		delete(n.serving, o.truck)
		n.lastNow = o.now
		return n.dispatch(o.now), nil
	}
	return nil, nil
}

func qkey(s int64, k Kind) [3]any { return [3]any{s, int(k), 0} }

func (n *naive) dispatch(now int64) []Assignment {
	var out []Assignment
	for {
		var ws []*nWaiter
		for _, w := range n.waiting {
			ws = append(ws, w)
		}
		sort.SliceStable(ws, func(i, j int) bool {
			return n.rankLess(ws[i], ws[j], now)
		})
		var chosen *nWaiter
		var borrow bool
		anyReeferWaiting := false
		for _, w := range ws {
			if w.kind == Reefer {
				anyReeferWaiting = true
			}
		}
		for _, w := range ws {
			if w.kind == Reefer {
				if len(n.freeReefer) > 0 {
					chosen = w
					borrow = false
					break
				}
				continue
			}
			if len(n.freeDry) > 0 {
				chosen = w
				break
			}
			if len(n.freeReefer) > 0 && !anyReeferWaiting {
				chosen = w
				borrow = true
				break
			}
		}
		if chosen == nil {
			return out
		}
		var d string
		if chosen.kind == Reefer {
			d = popMin(&n.freeReefer)
		} else if len(n.freeDry) > 0 {
			d = popMin(&n.freeDry)
		} else if borrow {
			d = popMin(&n.freeReefer)
		}
		delete(n.waiting, chosen.truck)
		n.serving[chosen.truck] = d
		n.occupant[d] = chosen.truck
		out = append(out, Assignment{Truck: []byte(chosen.truck), Dock: []byte(d)})
	}
}

func (n *naive) rankLess(a, b *nWaiter, now int64) bool {
	tier := func(w *nWaiter) int {
		if !w.ontime && now-w.ciTime >= n.cfg.Wmax {
			return 0
		}
		if w.ontime {
			return 1
		}
		return 2
	}
	ta, tb := tier(a), tier(b)
	if ta != tb {
		return ta < tb
	}
	if ta == 1 {
		if a.s != b.s {
			return a.s < b.s
		}
	}
	return a.seq < b.seq
}

func popMin(xs *[]string) string {
	idx := 0
	for i := 1; i < len(*xs); i++ {
		if (*xs)[i] < (*xs)[idx] {
			idx = i
		}
	}
	v := (*xs)[idx]
	*xs = append((*xs)[:idx], (*xs)[idx+1:]...)
	return v
}

func TestRandomDifferential(t *testing.T) {
	cfg := appt.Config{S: 20, E: 10, L: 8, Wmax: 25, K: 3}
	if testing.Verbose() {
		t.Logf("开始 1500 组随机操作序列，与朴素全量排序扫描模型对照（判定依据：错误类别 + 每步派位清单逐项相等 + 终态月台占用）")
	}
	for seed := int64(1); seed <= 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		y := New(cfg)
		nh := newNaive(cfg)

		var logBuf strings.Builder
		fmt.Fprintf(&logBuf, "seed=%d cfg=%+v\n", seed, cfg)

		const N = 50
		truckNames := make([]string, 0, 12)
		for i := 0; i < 12; i++ {
			truckNames = append(truckNames, fmt.Sprintf("t%02d", i))
		}
		dockNames := make([]string, 0, 8)
		for i := 0; i < 8; i++ {
			dockNames = append(dockNames, fmt.Sprintf("d%02d", i))
		}
		var now int64
		recs := make([]rec, 0, N)
		for step := 0; step < N; step++ {
			roll := rng.Intn(10)
			var o op
			switch {
			case roll < 3:
				o = op{kind: 0, truck: truckNames[rng.Intn(len(truckNames))],
					k:   Kind(rng.Intn(2)),
					s:   int64(rng.Intn(10)) * cfg.S,
					now: now}
			case roll < 5:
				o = op{kind: 1, dock: dockNames[rng.Intn(len(dockNames))],
					k: Kind(rng.Intn(2)), now: now}
			case roll < 9:
				o = op{kind: 2, truck: truckNames[rng.Intn(len(truckNames))],
					k: Kind(rng.Intn(2)), now: now}
			default:
				o = op{kind: 3, truck: truckNames[rng.Intn(len(truckNames))], now: now}
			}
			var out []Assignment
			var err error
			switch o.kind {
			case 0:
				err = y.Book([]byte(o.truck), o.k, o.s, o.now)
			case 1:
				out, err = y.AddDock([]byte(o.dock), o.k, o.now)
			case 2:
				out, err = y.CheckIn([]byte(o.truck), o.k, o.now)
			case 3:
				out, err = y.Depart([]byte(o.truck), o.now)
			}
			nout, nerr := nh.apply(o)
			recs = append(recs, rec{o: o, out: out, err: err, nout: nout, nerr: nerr})
			if errClass(err) != errClass(nerr) {
				dumpLog(t, logBuf, recs, "错误类别不一致")
			}
			if err == nil && !assignEq(out, nout) {
				dumpLog(t, logBuf, recs, "派位清单不一致")
			}
			fmt.Fprintf(&logBuf, "step=%d op=%+v -> out=%v err=%v | naive=%v err=%v\n",
				step, o, fmtAssign(out), err, fmtAssign(nout), nerr)
			if testing.Verbose() && seed <= 3 {
				t.Logf("seed=%d step=%d 输入 op=%+v | 实现输出=%v err=%v 朴素输出=%v err=%v（判定：%s）",
					seed, step, o, fmtAssign(out), errClass(err), fmtAssign(nout), errClass(nerr),
					map[bool]string{true: "一致", false: "见上"}[errClass(err) == errClass(nerr) && assignEq(out, nout)])
			}
			if err == nil && o.now > now {
				now = o.now
			}
			if rng.Intn(3) == 0 {
				now += int64(rng.Intn(40))
			}
		}
		// 终态不变量：每个月台至多一辆车。
		y.mu.Lock()
		for d, tr := range y.dockOf {
			if got := y.serving[tr]; !bytes.Equal(got, []byte(d)) {
				t.Fatalf("seed %d dockOf/serving mismatch", seed)
			}
		}
		y.mu.Unlock()
		if testing.Verbose() {
			t.Logf("seed=%d 通过，共 %d 步，判定：错误类别与派位清单均与朴素模型一致", seed, N)
		}
	}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, appt.ErrInvalid):
		return "invalid"
	case errors.Is(err, appt.ErrClock):
		return "clock"
	case errors.Is(err, appt.ErrNotFound):
		return "notfound"
	case errors.Is(err, appt.ErrState):
		return "state"
	case errors.Is(err, appt.ErrDuplicate):
		return "duplicate"
	case errors.Is(err, appt.ErrCapacity):
		return "capacity"
	default:
		return "unknown"
	}
}

func assignEq(a, b []Assignment) bool { return eqAssign(a, b) }

func fmtAssign(a []Assignment) string {
	parts := make([]string, len(a))
	for i := range a {
		parts[i] = fmt.Sprintf("(%s,%s)", a[i].Truck, a[i].Dock)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func dumpLog(t *testing.T, logBuf strings.Builder, recs []rec, reason string) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(reason + "\n")
	for _, r := range recs {
		fmt.Fprintf(&sb, "op=%+v got=%v(%v) naive=%v(%v)\n",
			r.o, fmtAssign(r.out), errClass(r.err), fmtAssign(r.nout), errClass(r.nerr))
	}
	t.Fatal(sb.String())
}
