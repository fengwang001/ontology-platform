package cedu

// naive_model.go（仅测试使用）：独立编写的参考实现。
// 它刻意“朴素”：不做任何增量缓存，每次操作都从事件日志全量重建
// 持证人的记录集合与各周期结论，复杂度随记录数线性增长。其唯一用途
// 是与带增量缓存的在线实现做差分对照。

type naiveRec struct {
	id       string
	cat      Category
	credits  int
	earnedOn int
	org      string
	regAt    int
	cycle    int
	zone     bool
}

type naiveHolder struct {
	id      string
	issue   int
	lastNow int
	expired bool
	front   int
	records map[string]*naiveRec // by id
	dup     map[string]string    // dupKey -> id
	seq     int
}

type naiveService struct {
	cfg     Config
	lastNow int
	h       map[string]*naiveHolder
}

func newNaive(cfg Config) *naiveService {
	return &naiveService{cfg: cfg, lastNow: -1, h: map[string]*naiveHolder{}}
}

// liveRecords 全量重建当前生命周期的有效记录（按 id 稳定序）。
func (n *naiveService) liveRecords(h *naiveHolder) []*naiveRec {
	out := make([]*naiveRec, 0, len(h.records))
	for i := 1; i <= h.seq; i++ {
		id := recordID(h.id, i)
		if r := h.records[id]; r != nil {
			out = append(out, r)
		}
	}
	return out
}

type naiveEval struct {
	front   int
	expired bool
	phase   int // front 周期：0 进行中 / 1 宽限中 / 2 已终结
	out     map[int]outcome
	carry   map[int]int
}

// evaluate 截至时刻 τ 全量扫描所有记录，顺序判定每个周期。
func (n *naiveService) evaluate(h *naiveHolder, tau int) naiveEval {
	recs := n.liveRecords(h)
	sum := func(k int, zone bool) [3]int {
		var v [3]int
		for _, r := range recs {
			if r.cycle == k && r.zone == zone {
				v[r.cat] += r.credits
			}
		}
		return v
	}
	ev := naiveEval{out: map[int]outcome{}, carry: map[int]int{}}
	k := 0
	for {
		_, end := cycleBounds(h.issue, n.cfg.CycleLength, k)
		ge := end + n.cfg.GraceDays
		w := sum(k, false)
		r := rawCredit{requiredRaw: w[Required], electiveRaw: w[Elective], onlineRaw: w[Online]}
		if k > 0 && ev.out[k-1] == ocPass {
			zp := sum(k-1, true)
			r.electiveRaw += zp[Elective] + ev.carry[k-1]
			r.onlineRaw += zp[Online]
		}
		c := countCredit(r, n.cfg)
		if tau < end {
			ev.front, ev.phase = k, 0
			ev.carry[k] = carryOut(r, c, n.cfg)
			return ev
		}
		if c.met(n.cfg) {
			ev.out[k] = ocPass
			ev.carry[k] = carryOut(r, c, n.cfg)
			k++
			continue
		}
		if tau < ge {
			ev.front, ev.phase = k, 1
			ev.carry[k] = 0
			return ev
		}
		z := sum(k, true)
		r2 := r
		r2.requiredRaw += z[Required]
		r2.electiveRaw += z[Elective]
		r2.onlineRaw += z[Online]
		if countCredit(r2, n.cfg).met(n.cfg) {
			ev.out[k] = ocGracePass
			ev.carry[k] = 0
			k++
			continue
		}
		ev.out[k] = ocExpired
		ev.front, ev.phase, ev.expired = k, 2, true
		return ev
	}
}

func (n *naiveService) register(in RegisterInput) error {
	if in.HolderID == "" || in.IssueDate < 0 || in.Now < in.IssueDate {
		return newError(ErrInvalidParam, "bad")
	}
	if in.Now < n.lastNow {
		return newError(ErrClockRollback, "bad")
	}
	if old := n.h[in.HolderID]; old != nil && !old.expired {
		return newError(ErrStateNotAllowed, "bad")
	}
	n.h[in.HolderID] = &naiveHolder{
		id: in.HolderID, issue: in.IssueDate, lastNow: in.Now,
		records: map[string]*naiveRec{}, dup: map[string]string{},
	}
	n.lastNow = in.Now
	return nil
}

func (n *naiveService) credit(in CreditInput) (string, error) {
	if in.HolderID == "" || in.Org == "" || !in.Category.valid() ||
		in.Credits <= 0 || in.EarnedOn < 0 || in.EarnedOn > in.Now {
		return "", newError(ErrInvalidParam, "bad")
	}
	if in.Now < n.lastNow {
		return "", newError(ErrClockRollback, "bad")
	}
	h := n.h[in.HolderID]
	if h == nil {
		return "", newError(ErrNotFound, "bad")
	}
	ev := n.evaluate(h, in.Now)
	if ev.expired {
		return "", newError(ErrCertificateExpired, "bad")
	}
	if in.EarnedOn < h.issue {
		return "", newError(ErrInvalidParam, "bad")
	}
	f := ev.front
	start, end := cycleBounds(h.issue, n.cfg.CycleLength, f)
	ge := end + n.cfg.GraceDays
	cycle, zone := f, false
	switch {
	case in.EarnedOn < start:
		return "", newError(ErrStateNotAllowed, "bad")
	case in.EarnedOn < end:
		cycle, zone = f, false
	case in.EarnedOn < ge:
		cycle, zone = f, true
	default:
		cycle, zone = (in.EarnedOn-h.issue)/n.cfg.CycleLength, false
	}
	if _, ok := h.dup[dupKey(in.Org, in.EarnedOn, in.Category)]; ok {
		return "", newError(ErrDuplicate, "bad")
	}
	h.seq++
	id := recordID(h.id, h.seq)
	h.records[id] = &naiveRec{
		id: id, cat: in.Category, credits: in.Credits,
		earnedOn: in.EarnedOn, org: in.Org, regAt: in.Now,
		cycle: cycle, zone: zone,
	}
	h.dup[dupKey(in.Org, in.EarnedOn, in.Category)] = id
	h.lastNow, n.lastNow = in.Now, in.Now
	return id, nil
}

func (n *naiveService) correctOrRevoke(holderID, rid, org string, credits, now int, revoke bool) error {
	if holderID == "" || rid == "" || org == "" || now < 0 || (!revoke && credits <= 0) {
		return newError(ErrInvalidParam, "bad")
	}
	if now < n.lastNow {
		return newError(ErrClockRollback, "bad")
	}
	h := n.h[holderID]
	if h == nil {
		return newError(ErrNotFound, "bad")
	}
	ev := n.evaluate(h, now)
	if ev.expired {
		return newError(ErrCertificateExpired, "bad")
	}
	r := h.records[rid]
	if r == nil || r.org != org {
		return newError(ErrNotFound, "bad")
	}
	if now > r.regAt+n.cfg.CorrectDays {
		return newError(ErrCorrectionExpired, "bad")
	}
	if revoke {
		delete(h.records, rid)
		delete(h.dup, dupKey(r.org, r.earnedOn, r.cat))
	} else {
		r.credits = credits
	}
	h.lastNow, n.lastNow = now, now
	return nil
}

// snapshot 全量计算 now 时刻当前周期快照。
func (n *naiveService) snapshot(h *naiveHolder, now int) CycleStatus {
	ev := n.evaluate(h, now)
	k := ev.front
	start, end := cycleBounds(h.issue, n.cfg.CycleLength, k)
	recs := n.liveRecords(h)
	var w, z [3]int
	for _, r := range recs {
		if r.cycle == k && !r.zone {
			w[r.cat] += r.credits
		}
		if r.cycle == k && r.zone {
			z[r.cat] += r.credits
		}
	}
	raw := rawCredit{requiredRaw: w[Required], electiveRaw: w[Elective], onlineRaw: w[Online]}
	carryIn := 0
	if k > 0 && ev.out[k-1] == ocPass {
		carryIn = ev.carry[k-1]
		raw.electiveRaw += carryIn
		for _, r := range recs {
			if r.cycle == k-1 && r.zone {
				switch r.cat {
				case Elective:
					raw.electiveRaw += r.credits
				case Online:
					raw.onlineRaw += r.credits
				}
			}
		}
	}
	phase := ev.phase
	if ev.out[k] == ocPass || ev.out[k] == ocGracePass || ev.out[k] == ocExpired {
		phase = 2
	}
	if phase == 1 {
		raw.requiredRaw += z[Required]
		raw.electiveRaw += z[Elective]
		raw.onlineRaw += z[Online]
	}
	c := countCredit(raw, n.cfg)
	cs := CycleStatus{
		Index: k, Start: start, End: end, GraceEnd: end + n.cfg.GraceDays,
		RequiredIn: c.requiredIn, ElectiveIn: c.electiveIn, OnlineIn: c.onlineIn,
		TotalIn: c.totalIn, CarryIn: carryIn,
	}
	switch ev.out[k] {
	case ocPass:
		cs.Met, cs.CarryOut = true, ev.carry[k]
	case ocGracePass:
		cs.Met = true
	case ocExpired:
		cs.Met = false
	default:
		cs.Met = c.met(n.cfg)
		cs.InGrace = phase == 1
	}
	return cs
}
