package booking

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/slotpool"
)

// naiveSystem 是按题目规则直接写成的逐步朴素模拟：
// 每次操作前线性扫描全部预约落地，无堆、无索引，作为判定依据。
type naiveSystem struct {
	r, e, g, c, k, w int64
	maxNow, seq      int64
	slots            map[string]*naiveSlot
	cred             map[string][]int64
}

type naiveSlot struct {
	start    int64
	cap, on  int
	active   map[string]*naiveBooking
	waitlist []string
}

type naiveBooking struct {
	patient, slot string
	ch            slotpool.Channel
	seq           int64
	checked       bool
}

func newNaive(r, e, g, c, k, w int64) *naiveSystem {
	return &naiveSystem{r: r, e: e, g: g, c: c, k: k, w: w,
		slots: map[string]*naiveSlot{}, cred: map[string][]int64{}}
}

func (n *naiveSystem) counts(sl *naiveSlot) (uo, us int) {
	for _, b := range sl.active {
		if b.ch == slotpool.Online {
			uo++
		} else {
			us++
		}
	}
	return
}

func (n *naiveSystem) banned(now int64, p string) bool {
	cnt := 0
	for _, t := range n.cred[p] {
		if now-t < n.w {
			cnt++
		}
	}
	return cnt >= int(n.k)
}

// land 按 (dead, seq) 升序落地所有 dead < now 的未签到预约。
func (n *naiveSystem) land(now int64) {
	type db struct {
		dead int64
		b    *naiveBooking
	}
	var due []db
	for _, sl := range n.slots {
		dead := sl.start + n.g
		if dead >= now {
			continue
		}
		for _, b := range sl.active {
			if !b.checked {
				due = append(due, db{dead, b})
			}
		}
	}
	sort.SliceStable(due, func(i, j int) bool {
		if due[i].dead != due[j].dead {
			return due[i].dead < due[j].dead
		}
		return due[i].b.seq < due[j].b.seq
	})
	for _, d := range due {
		b := d.b
		sl := n.slots[b.slot]
		if _, ok := sl.active[b.patient]; !ok {
			continue
		}
		delete(sl.active, b.patient)
		n.cred[b.patient] = append(n.cred[b.patient], sl.start+n.g)
		n.promote(b.slot, now)
	}
	for _, sl := range n.slots {
		if sl.start+n.g < now {
			sl.waitlist = nil
		}
	}
	n.maxNow = now
}

func (n *naiveSystem) promote(slot string, now int64) {
	sl := n.slots[slot]
	if len(sl.waitlist) == 0 {
		return
	}
	uo, us := n.counts(sl)
	if uo+us >= sl.cap {
		return
	}
	p := sl.waitlist[0]
	sl.waitlist = sl.waitlist[1:]
	n.seq++
	b := &naiveBooking{patient: p, slot: slot, ch: slotpool.OnSite, seq: n.seq}
	if now > sl.start {
		b.checked = true
	}
	sl.active[p] = b
}

func errName(err error) string {
	switch err {
	case nil:
		return "ok"
	case ErrInvalid:
		return "Invalid"
	case ErrClockBack:
		return "ClockBack"
	case ErrSlotMissing:
		return "SlotMissing"
	case ErrOpen:
		return "Open"
	case ErrDuplicate:
		return "Duplicate"
	case ErrBanned:
		return "Banned"
	case ErrNoQuota:
		return "NoQuota"
	case ErrTooEarly:
		return "TooEarly"
	case ErrNoBooking:
		return "NoBooking"
	case ErrAlreadyIn:
		return "AlreadyIn"
	case ErrLateCancel:
		return "LateCancel"
	case ErrNotWaitable:
		return "NotWaitable"
	}
	return err.Error()
}

func (n *naiveSystem) addSlot(now int64, slot string, start int64, cap, on int) error {
	if slot == "" || start <= now || cap < 1 || cap > 1000 || on < 0 || on > cap {
		return ErrInvalid
	}
	if now < n.maxNow {
		return ErrClockBack
	}
	n.land(now)
	if _, ok := n.slots[slot]; ok {
		return ErrInvalid
	}
	n.slots[slot] = &naiveSlot{start: start, cap: cap, on: on, active: map[string]*naiveBooking{}}
	return nil
}

func (n *naiveSystem) pre(now int64, p, slot string) (*naiveSlot, error) {
	if p == "" || slot == "" {
		return nil, ErrInvalid
	}
	if now < n.maxNow {
		return nil, ErrClockBack
	}
	sl, ok := n.slots[slot]
	if !ok {
		return nil, ErrSlotMissing
	}
	return sl, nil
}

func (n *naiveSystem) waiting(sl *naiveSlot, p string) bool {
	for _, q := range sl.waitlist {
		if q == p {
			return true
		}
	}
	return false
}

func (n *naiveSystem) book(now int64, p, slot string, ch slotpool.Channel) (int64, error) {
	if ch != slotpool.Online && ch != slotpool.OnSite {
		return 0, ErrInvalid
	}
	sl, err := n.pre(now, p, slot)
	if err != nil {
		return 0, err
	}
	n.land(now)
	if now >= sl.start {
		return 0, ErrOpen
	}
	if _, ok := sl.active[p]; ok || n.waiting(sl, p) {
		return 0, ErrDuplicate
	}
	if ch == slotpool.Online && n.banned(now, p) {
		return 0, ErrBanned
	}
	uo, us := n.counts(sl)
	avail := false
	if now < sl.start-n.r {
		if ch == slotpool.Online {
			avail = uo < sl.on
		} else {
			avail = us < sl.cap-sl.on
		}
	} else {
		avail = uo+us < sl.cap
	}
	if !avail {
		return 0, ErrNoQuota
	}
	n.seq++
	sl.active[p] = &naiveBooking{patient: p, slot: slot, ch: ch, seq: n.seq}
	return n.seq, nil
}

func (n *naiveSystem) checkin(now int64, p, slot string) error {
	sl, err := n.pre(now, p, slot)
	if err != nil {
		return err
	}
	n.land(now)
	if now < sl.start-n.e {
		return ErrTooEarly
	}
	b, ok := sl.active[p]
	if !ok {
		return ErrNoBooking
	}
	if b.checked {
		return nil
	}
	if now > sl.start+n.g {
		return ErrNoBooking
	}
	b.checked = true
	return nil
}

func (n *naiveSystem) cancel(now int64, p, slot string) error {
	sl, err := n.pre(now, p, slot)
	if err != nil {
		return err
	}
	n.land(now)
	b, ok := sl.active[p]
	if !ok {
		return ErrNoBooking
	}
	if b.checked {
		return ErrAlreadyIn
	}
	delete(sl.active, p)
	n.promote(slot, now)
	if now <= sl.start-n.c {
		return nil
	}
	n.cred[p] = append(n.cred[p], now)
	return ErrLateCancel
}

func (n *naiveSystem) joinWait(now int64, p, slot string) error {
	sl, err := n.pre(now, p, slot)
	if err != nil {
		return err
	}
	n.land(now)
	if _, ok := sl.active[p]; ok || n.waiting(sl, p) {
		return ErrDuplicate
	}
	uo, us := n.counts(sl)
	okWindow := sl.start-n.r <= now && now <= sl.start+n.g && uo+us >= sl.cap
	if !okWindow {
		return ErrNotWaitable
	}
	sl.waitlist = append(sl.waitlist, p)
	return nil
}

type fuzzOp struct {
	kind    int
	now     int64
	p, slot string
	ch      slotpool.Channel
	start   int64
	cap, on int
}

// snapshot 描述某时刻全部可观测状态，用于双实现比对。
type snapshot struct {
	maxNow int64
	seq    int64
	slots  map[string]string // slot -> "uo/us/cap wait:[..]"
	active map[string]string // slot|patient -> "ch:checked:seq"
	cred   map[string][]int64
}

func (s *System) snapshot() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := snapshot{maxNow: s.maxNow, seq: s.seq,
		slots: map[string]string{}, active: map[string]string{},
		cred: map[string][]int64{}}
	for id, st := range s.slots {
		sp := s.pool.Get(id)
		var ws []string
		for _, w := range st.wait {
			ws = append(ws, w.patient)
		}
		sn.slots[id] = fmt.Sprintf("%d/%d/%d wait:%v", sp.UO, sp.US, sp.Cap, ws)
		for p, e := range st.active {
			sn.active[id+"|"+p] = fmt.Sprintf("%d:%v:%d", e.channel, e.checked, e.seq)
		}
	}
	for _, p := range s.cred.Patients() {
		ts := s.cred.Records(p)
		sn.cred[p] = append([]int64(nil), ts...)
	}
	return sn
}

func (n *naiveSystem) snapshot() snapshot {
	sn := snapshot{maxNow: n.maxNow, seq: n.seq,
		slots: map[string]string{}, active: map[string]string{},
		cred: map[string][]int64{}}
	for id, sl := range n.slots {
		uo, us := n.counts(sl)
		sn.slots[id] = fmt.Sprintf("%d/%d/%d wait:%v", uo, us, sl.cap, sl.waitlist)
		for p, b := range sl.active {
			sn.active[id+"|"+p] = fmt.Sprintf("%d:%v:%d", b.ch, b.checked, b.seq)
		}
	}
	for p, ts := range n.cred {
		cp := append([]int64(nil), ts...)
		sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
		sn.cred[p] = cp
	}
	return sn
}

func snapEqual(a, b snapshot) (string, bool) {
	if a.maxNow != b.maxNow {
		return fmt.Sprintf("maxNow %d!=%d", a.maxNow, b.maxNow), false
	}
	if a.seq != b.seq {
		return fmt.Sprintf("seq %d!=%d", a.seq, b.seq), false
	}
	for k, v := range a.slots {
		if b.slots[k] != v {
			return fmt.Sprintf("slot %s: %q != %q", k, v, b.slots[k]), false
		}
	}
	for k := range b.slots {
		if _, ok := a.slots[k]; !ok {
			return fmt.Sprintf("slot %s missing impl", k), false
		}
	}
	for k, v := range a.active {
		if b.active[k] != v {
			return fmt.Sprintf("active %s: %q != %q", k, v, b.active[k]), false
		}
	}
	for k := range b.active {
		if _, ok := a.active[k]; !ok {
			return fmt.Sprintf("active %s missing impl", k), false
		}
	}
	for p, ts := range a.cred {
		ts2 := append([]int64(nil), b.cred[p]...)
		sort.Slice(ts2, func(i, j int) bool { return ts2[i] < ts2[j] })
		ts1 := append([]int64(nil), ts...)
		sort.Slice(ts1, func(i, j int) bool { return ts1[i] < ts1[j] })
		if fmt.Sprint(ts1) != fmt.Sprint(ts2) {
			return fmt.Sprintf("credit %s: %v != %v", p, ts1, ts2), false
		}
	}
	return "", true
}

// TestFuzzAgainstNaive 对 1500 组随机操作序列做逐步对照；
// 日志含每组输入、输出、错误判定依据与状态比对结果。
func TestFuzzAgainstNaive(t *testing.T) {
	t.Log("使用 go test -v 查看每组输入/输出/判定依据")
	rng := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 1500; iter++ {
		r, e, g := rng.Intn(120), rng.Intn(120), rng.Intn(30)
		c, k := rng.Intn(200), 1+rng.Intn(3)
		w := 1 + rng.Intn(2000)
		sys := New(r, e, g, c, k, w)
		nav := newNaive(int64(r), int64(e), int64(g), int64(c), int64(k), int64(w))

		nSlots := 1 + rng.Intn(3)
		nPat := 2 + rng.Intn(6)
		starts := make([]int64, nSlots)
		for i := range starts {
			starts[i] = 200 + int64(rng.Intn(800))
		}
		var ops []fuzzOp
		now := int64(0)
		for i := 0; i < 40+rng.Intn(80); i++ {
			if rng.Intn(5) == 0 {
				now += int64(rng.Intn(30))
			} else {
				now += int64(rng.Intn(3))
			}
			si := rng.Intn(nSlots)
			op := fuzzOp{
				kind: rng.Intn(5),
				now:  now,
				p:    fmt.Sprintf("p%d", rng.Intn(nPat)),
				slot: fmt.Sprintf("s%d", si),
			}
			if rng.Intn(20) == 0 {
				op.slot = ""
			}
			if rng.Intn(20) == 0 {
				op.p = ""
			}
			if op.kind == 1 {
				if rng.Intn(2) == 0 {
					op.ch = slotpool.Online
				} else {
					op.ch = slotpool.OnSite
				}
			}
			if op.kind == 0 {
				op.start = starts[si] + int64(rng.Intn(200))
				op.cap = 1 + rng.Intn(4)
				op.on = rng.Intn(op.cap + 1)
				if rng.Intn(6) == 0 {
					op.start = now - 1
				}
			}
			ops = append(ops, op)
		}

		var trace strings.Builder
		fmt.Fprintf(&trace, "iter=%d R=%d E=%d G=%d C=%d K=%d W=%d\n",
			iter, r, e, g, c, k, w)
		bad := false
		for _, op := range ops {
			var gotErr, wantErr error
			var gotSeq, wantSeq int64
			label := ""
			switch op.kind {
			case 0:
				gotErr = sys.AddSlot(op.now, op.slot, op.start, op.cap, op.on)
				wantErr = nav.addSlot(op.now, op.slot, op.start, op.cap, op.on)
				label = fmt.Sprintf("AddSlot(now=%d slot=%q start=%d cap=%d on=%d)",
					op.now, op.slot, op.start, op.cap, op.on)
			case 1:
				gotSeq, gotErr = sys.Book(op.now, []byte(op.p), op.slot, op.ch)
				wantSeq, wantErr = nav.book(op.now, op.p, op.slot, op.ch)
				label = fmt.Sprintf("Book(now=%d p=%q slot=%q ch=%v)",
					op.now, op.p, op.slot, op.ch)
			case 2:
				gotErr = sys.CheckIn(op.now, []byte(op.p), op.slot)
				wantErr = nav.checkin(op.now, op.p, op.slot)
				label = fmt.Sprintf("CheckIn(now=%d p=%q slot=%q)", op.now, op.p, op.slot)
			case 3:
				gotErr = sys.Cancel(op.now, []byte(op.p), op.slot)
				wantErr = nav.cancel(op.now, op.p, op.slot)
				label = fmt.Sprintf("Cancel(now=%d p=%q slot=%q)", op.now, op.p, op.slot)
			case 4:
				gotErr = sys.JoinWait(op.now, []byte(op.p), op.slot)
				wantErr = nav.joinWait(op.now, op.p, op.slot)
				label = fmt.Sprintf("JoinWait(now=%d p=%q slot=%q)", op.now, op.p, op.slot)
			}
			mark := ""
			if gotErr != wantErr || gotSeq != wantSeq {
				mark = fmt.Sprintf("  <-- 分歧: impl=(%d,%s) naive=(%d,%s)",
					gotSeq, errName(gotErr), wantSeq, errName(wantErr))
				bad = true
			}
			fmt.Fprintf(&trace, "  %-58s => impl(%d,%-11s) naive(%d,%-11s)%s\n",
				label, gotSeq, errName(gotErr), wantSeq, errName(wantErr), mark)
			if reason, eq := snapEqual(sys.snapshot(), nav.snapshot()); !eq {
				fmt.Fprintf(&trace, "    状态分歧: %s\n", reason)
				bad = true
			}
		}
		t.Log(trace.String())
		if bad {
			t.Fatalf("iter %d 与朴素模拟不一致，见上方 trace", iter)
		}
	}
}
