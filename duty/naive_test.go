package duty

// naiveModel 是按本题规则独立写成的朴素对照模型：全部检查均为线性扫描，
// 滚动窗逐整数起点枚举。它与生产实现共享 Config/Duty 类型，但不复用任何
// 判定代码路径，因此可作为差分测试的可信基准。

type naivePerson struct {
	quals  map[int]int
	duties []*Duty
}

type naiveSystem struct {
	cfg    Config
	clock  int
	nextID int
	people map[int]*naivePerson
}

func newNaive(cfg Config) *naiveSystem {
	return &naiveSystem{cfg: cfg, nextID: 1, people: map[int]*naivePerson{}}
}

func (n *naiveSystem) addPerson(now, id int) Result {
	if now < 0 || id < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if now < n.clock {
		return Result{Reject: RejectClockRollback}
	}
	if _, e := n.people[id]; e {
		return Result{Reject: RejectInvalidParams}
	}
	n.people[id] = &naivePerson{quals: map[int]int{}}
	n.clock = now
	return Result{}
}

func (n *naiveSystem) updateQual(now, pid, ac, ex int) Result {
	if now < 0 || pid < 0 || ac < 0 || ex < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if now < n.clock {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := n.people[pid]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	p.quals[ac] = ex
	n.clock = now
	return Result{}
}

func (n *naiveSystem) revokeQual(now, pid, ac int) Result {
	if now < 0 || pid < 0 || ac < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if now < n.clock {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := n.people[pid]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	delete(p.quals, ac)
	n.clock = now
	return Result{}
}

// naiveRegister 朴素登记：返回结论与新 ID（接受时）。
func (n *naiveSystem) register(now, pid, st, en, legs, ac int) (Result, int) {
	if now < 0 || pid < 0 || st < 0 || en <= st || legs < 0 || legs > 8 || ac < 0 {
		return Result{Reject: RejectInvalidParams}, 0
	}
	if now < n.clock {
		return Result{Reject: RejectClockRollback}, 0
	}
	p, ok := n.people[pid]
	if !ok {
		return Result{Reject: RejectNoPerson}, 0
	}
	if r := n.check(p, &Duty{Start: st, End: en, Legs: legs, Aircraft: ac}, false, 0); !r.OK() {
		return r, 0
	}
	d := &Duty{ID: n.nextID, PersonID: pid, Start: st, End: en, Legs: legs, Aircraft: ac, OrigEnd: en}
	n.nextID++
	// 保持 Start 升序插入。
	pos := 0
	for pos < len(p.duties) && p.duties[pos].Start < st {
		pos++
	}
	p.duties = append(p.duties, nil)
	copy(p.duties[pos+1:], p.duties[pos:])
	p.duties[pos] = d
	n.clock = now
	return Result{}, d.ID
}

func (n *naiveSystem) extend(now, pid, did, newEnd int) Result {
	if now < 0 || pid < 0 || did < 0 || newEnd < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if now < n.clock {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := n.people[pid]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	var d *Duty
	for _, e := range p.duties {
		if e.ID == did {
			d = e
		}
	}
	if d == nil {
		return Result{Reject: RejectNoDuty}
	}
	if d.End <= now {
		return Result{Reject: RejectImmutable}
	}
	if newEnd <= d.End {
		return Result{Reject: RejectInvalidParams}
	}
	cand := *d
	cand.End = newEnd
	if r := n.check(p, &cand, true, d.ID); !r.OK() {
		return r
	}
	d.End = newEnd
	d.Extended = true
	n.clock = now
	return Result{}
}

func (n *naiveSystem) cancel(now, pid, did int) Result {
	if now < 0 || pid < 0 || did < 0 {
		return Result{Reject: RejectInvalidParams}
	}
	if now < n.clock {
		return Result{Reject: RejectClockRollback}
	}
	p, ok := n.people[pid]
	if !ok {
		return Result{Reject: RejectNoPerson}
	}
	pos := -1
	for i, e := range p.duties {
		if e.ID == did {
			pos = i
		}
	}
	if pos < 0 {
		return Result{Reject: RejectNoDuty}
	}
	if p.duties[pos].Start <= now {
		return Result{Reject: RejectImmutable}
	}
	p.duties = append(p.duties[:pos], p.duties[pos+1:]...)
	n.clock = now
	return Result{}
}

// check 执行 6..12 类检查。extend=true 且 skipID 为被延长值勤期 ID。
func (n *naiveSystem) check(p *naivePerson, cand *Duty, extend bool, skipID int) Result {
	c := n.cfg
	if ex, h := p.quals[cand.Aircraft]; !h || ex <= cand.End {
		return Result{Reject: RejectQualification}
	}
	// 构造含候选的有序序列，再逐对检查相邻值勤期：
	// 严格相交或端点相接 => 重叠；否则检查休息是否足够。
	sorted := append([]*Duty{}, p.duties...)
	if extend {
		tmp := make([]*Duty, 0, len(sorted))
		for _, d := range sorted {
			if d.ID != skipID {
				tmp = append(tmp, d)
			}
		}
		sorted = tmp
	}
	pos := 0
	for pos < len(sorted) && sorted[pos].Start < cand.Start {
		pos++
	}
	// 同起点
	if pos < len(sorted) && sorted[pos].Start == cand.Start {
		return Result{Reject: RejectOverlap}
	}
	if pos > 0 {
		prev := sorted[pos-1]
		if prev.End >= cand.Start {
			return Result{Reject: RejectOverlap}
		}
		if cand.Start-prev.End < c.RestRequired(prev.End-prev.Start) {
			return Result{Reject: RejectRest}
		}
	}
	if pos < len(sorted) {
		nx := sorted[pos]
		if cand.End >= nx.Start {
			return Result{Reject: RejectOverlap}
		}
		if nx.Start-cand.End < c.RestRequired(cand.End-cand.Start) {
			return Result{Reject: RejectRest}
		}
	}
	// 单次上限。
	limit := c.SingleLimit(cand.Start, cand.Legs)
	if extend {
		limit += c.MaxExtension
	}
	if cand.End-cand.Start > limit {
		return Result{Reject: RejectSingleLimit}
	}
	// 延长规则。
	if extend {
		var orig *Duty
		for _, d := range p.duties {
			if d.ID == skipID {
				orig = d
			}
		}
		if orig.Extended || cand.End-orig.OrigEnd > c.MaxExtension {
			return Result{Reject: RejectExtension}
		}
		for _, e := range p.duties {
			if e.ID == skipID || !e.Extended {
				continue
			}
			ls := cand.Start
			if e.Start > ls {
				ls = e.Start
			}
			ee := cand.End
			if e.End < ee {
				ee = e.End
			}
			if ee-ls < c.Window7 {
				return Result{Reject: RejectExtension}
			}
		}
	}
	// 滚动：逐整数窗起点枚举，从 0 到足够远；返回最小超限起点。
	all := make([]*Duty, 0, len(p.duties)+1)
	for _, d := range p.duties {
		if extend && d.ID == skipID {
			continue
		}
		all = append(all, d)
	}
	all = append(all, cand)
	maxEnd := cand.End
	minStart := cand.Start
	roll := func(win, capLimit int) int {
		for w := minStart - win; w <= maxEnd; w++ {
			if w < 0 {
				continue
			}
			total := 0
			for _, d := range all {
				total += overlap(d.Start, d.End, w, w+win)
			}
			if total > capLimit {
				return w
			}
		}
		return -1
	}
	if w := roll(c.Window7, c.Limit7); w >= 0 {
		return Result{Reject: RejectRolling7, WindowStart: w}
	}
	if w := roll(c.Window28, c.Limit28); w >= 0 {
		return Result{Reject: RejectRolling28, WindowStart: w}
	}
	return Result{}
}
