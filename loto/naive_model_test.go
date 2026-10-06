package loto_test

// naive 是独立编写的"朴素模型"：
//   - 不使用任何活动票/在位锁索引；
//   - 冲突判定与送电判定都线性扫描系统中全部历史票；
//   - 逻辑直接按规格文字逐条翻译，刻意不与实现共享代码。
// 它与真实 System 接受同样的随机操作序列，结果必须逐项一致。

type nPerson struct {
	id    string
	roles map[string]bool
}

type nLock struct {
	worker, point, permit string
	trialRemoved          bool
	removed               bool
	forced                bool
}

type nPermit struct {
	id, applicant string
	devices       map[string]bool
	points        map[string]bool
	workers       map[string]bool
	workType      string
	start, end    int64
	phase         string
	approvers     map[string]bool
	overdue       bool
	inside        map[string]bool
	locks         map[string]*nLock
	completedAt   int64
}

type nError struct{ code string }

func (e *nError) Error() string { return e.code }

type naive struct {
	persons map[string]*nPerson
	devices map[string]map[string]bool
	permits map[string]*nPermit
	clock   int64
	audit   []string
}

func newNaive() *naive {
	return &naive{
		persons: map[string]*nPerson{},
		devices: map[string]map[string]bool{},
		permits: map[string]*nPermit{},
	}
}

func nfail(code string) error { return &nError{code: code} }

func nset(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func (n *naive) registerPerson(id string, roles ...string) error {
	if id == "" {
		return nfail("InvalidParam")
	}
	valid := map[string]bool{"applicant": true, "approver": true, "worker": true, "supervisor": true}
	rs := map[string]bool{}
	for _, r := range roles {
		if !valid[r] {
			return nfail("InvalidParam")
		}
		rs[r] = true
	}
	p, ok := n.persons[id]
	if !ok {
		p = &nPerson{id: id, roles: map[string]bool{}}
		n.persons[id] = p
	}
	for r := range rs {
		p.roles[r] = true
	}
	return nil
}

func (n *naive) registerDevice(id string, points []string) error {
	if id == "" {
		return nfail("InvalidParam")
	}
	ps := map[string]bool{}
	for _, pt := range points {
		if pt == "" {
			return nfail("InvalidParam")
		}
		ps[pt] = true
	}
	if _, ok := n.devices[id]; !ok {
		n.devices[id] = map[string]bool{}
	}
	for pt := range ps {
		n.devices[id][pt] = true
	}
	return nil
}

func (n *naive) checkClock(at int64) error {
	if at < 0 {
		return nfail("InvalidParam")
	}
	if at < n.clock {
		return nfail("ClockRollback")
	}
	return nil
}

func (n *naive) advance(at int64) {
	for _, p := range n.permits {
		if !p.overdue && p.phase != "pending" && p.phase != "completed" && at >= p.end {
			p.overdue = true
			n.audit = append(n.audit, "auto_overdue:"+p.id)
		}
	}
	n.clock = at
}

func (n *naive) log(s string) { n.audit = append(n.audit, s) }

func overlapN(a0, a1, b0, b1 int64) bool { return a0 < b1 && b0 < a1 }

func occupiedN(ph string) bool {
	return ph == "effective" || ph == "verified" || ph == "working" || ph == "trial"
}

func (p *nPermit) allLocks() bool {
	for w := range p.workers {
		for pt := range p.points {
			lk := p.locks[w+"|"+pt]
			if lk == nil || lk.removed || lk.trialRemoved {
				return false
			}
		}
	}
	return true
}

func (p *nPermit) workerLocks(w string) bool {
	for pt := range p.points {
		lk := p.locks[w+"|"+pt]
		if lk == nil || lk.removed || lk.trialRemoved {
			return false
		}
	}
	return true
}

func (n *naive) apply(id, applicant string, devices []string, wt string, start, end int64, workers []string) error {
	if id == "" || applicant == "" || start < 0 || end <= start ||
		(wt != "normal" && wt != "high_risk" && wt != "observation") {
		return nfail("InvalidParam")
	}
	dm, wm := nset(devices), nset(workers)
	if len(dm) == 0 || len(wm) == 0 {
		return nfail("InvalidParam")
	}
	for _, d := range devices {
		if d == "" {
			return nfail("InvalidParam")
		}
	}
	for _, w := range workers {
		if w == "" {
			return nfail("InvalidParam")
		}
	}
	if _, dup := n.permits[id]; dup {
		return nfail("InvalidParam")
	}
	app, ok := n.persons[applicant]
	if !ok {
		return nfail("NotFound")
	}
	for d := range dm {
		if _, ok := n.devices[d]; !ok {
			return nfail("NotFound")
		}
	}
	for w := range wm {
		if _, ok := n.persons[w]; !ok {
			return nfail("NotFound")
		}
	}
	if !app.roles["applicant"] {
		return nfail("PermissionDenied")
	}
	for w := range wm {
		if !n.persons[w].roles["worker"] {
			return nfail("PermissionDenied")
		}
	}
	points := map[string]bool{}
	for d := range dm {
		for pt := range n.devices[d] {
			points[pt] = true
		}
	}
	n.permits[id] = &nPermit{
		id: id, applicant: applicant, devices: dm, points: points, workers: wm,
		workType: wt, start: start, end: end, phase: "pending",
		approvers: map[string]bool{}, inside: map[string]bool{}, locks: map[string]*nLock{},
	}
	n.log("apply:" + id)
	return nil
}

func (n *naive) approve(id, who string, at int64) error {
	if id == "" || who == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	a, ok := n.persons[who]
	if !ok {
		return nfail("NotFound")
	}
	if !a.roles["approver"] {
		return nfail("PermissionDenied")
	}
	if p.phase != "pending" {
		return nfail("StateNotAllowed")
	}
	effective := p.workType != "high_risk" || len(p.approvers)+1 >= 2
	if effective {
		for _, q := range n.permits {
			if q.id == p.id || !occupiedN(q.phase) {
				continue
			}
			if p.workType == "observation" && q.workType == "observation" {
				continue
			}
			for d := range p.devices {
				if q.devices[d] && overlapN(p.start, p.end, q.start, q.end) {
					return nfail("Conflict")
				}
			}
		}
	}
	if who == p.applicant {
		return nfail("ConditionNotMet")
	}
	if p.approvers[who] {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	p.approvers[who] = true
	if effective {
		p.phase = "effective"
		n.log("approve_effective:" + id)
	} else {
		n.log("approve_partial:" + id)
	}
	return nil
}

func (n *naive) placeLock(id, w, pt string, at int64) error {
	if id == "" || w == "" || pt == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if !p.points[pt] {
		return nfail("InvalidParam")
	}
	if _, ok := n.persons[w]; !ok {
		return nfail("NotFound")
	}
	if !p.workers[w] {
		return nfail("PermissionDenied")
	}
	var lk *nLock
	switch p.phase {
	case "effective", "verified":
	case "working":
		lk = p.locks[w+"|"+pt]
		if lk == nil || !lk.removed || !lk.forced {
			return nfail("StateNotAllowed")
		}
	case "trial":
		lk = p.locks[w+"|"+pt]
		if lk == nil || !lk.trialRemoved || lk.worker != w {
			return nfail("StateNotAllowed")
		}
	default:
		return nfail("StateNotAllowed")
	}
	lk = p.locks[w+"|"+pt]
	if lk != nil && !lk.removed && !lk.trialRemoved {
		return nfail("StateNotAllowed")
	}
	if lk == nil {
		lk = &nLock{worker: w, point: pt, permit: id}
		p.locks[w+"|"+pt] = lk
	}
	n.advance(at)
	lk.removed, lk.trialRemoved, lk.forced = false, false, false
	n.log("place_lock:" + id + ":" + w + "@" + pt)
	return nil
}

func (n *naive) verify(id, v string, at int64) error {
	if id == "" || v == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[v]; !ok {
		return nfail("NotFound")
	}
	if p.phase != "effective" {
		return nfail("StateNotAllowed")
	}
	if p.workers[v] {
		return nfail("ConditionNotMet")
	}
	if !p.allLocks() {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	p.phase = "verified"
	n.log("verify:" + id)
	return nil
}

func (n *naive) startWork(id, actor string, at int64) error {
	if id == "" || actor == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[actor]; !ok {
		return nfail("NotFound")
	}
	if actor != p.applicant {
		return nfail("PermissionDenied")
	}
	if p.phase != "verified" {
		return nfail("StateNotAllowed")
	}
	if p.overdue || at >= p.end {
		return nfail("StateNotAllowed")
	}
	if at < p.start || at >= p.end {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	p.phase = "working"
	n.log("start_work:" + id)
	return nil
}

func (n *naive) enter(id, w string, at int64) error {
	if id == "" || w == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[w]; !ok {
		return nfail("NotFound")
	}
	if !p.workers[w] {
		return nfail("PermissionDenied")
	}
	if p.phase != "working" {
		return nfail("StateNotAllowed")
	}
	if p.inside[w] {
		return nfail("ConditionNotMet")
	}
	if !p.workerLocks(w) {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	p.inside[w] = true
	n.log("enter:" + id + ":" + w)
	return nil
}

func (n *naive) leave(id, w string, at int64) error {
	if id == "" || w == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[w]; !ok {
		return nfail("NotFound")
	}
	if !p.workers[w] {
		return nfail("PermissionDenied")
	}
	if p.phase != "working" {
		return nfail("StateNotAllowed")
	}
	if !p.inside[w] {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	delete(p.inside, w)
	n.log("leave:" + id + ":" + w)
	return nil
}

func (n *naive) complete(id, actor string, at int64) error {
	if id == "" || actor == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[actor]; !ok {
		return nfail("NotFound")
	}
	if actor != p.applicant {
		return nfail("PermissionDenied")
	}
	if p.phase != "working" {
		return nfail("StateNotAllowed")
	}
	if p.overdue || at >= p.end {
		return nfail("StateNotAllowed")
	}
	if len(p.inside) != 0 {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	p.phase = "completed"
	p.completedAt = at
	n.log("complete:" + id)
	return nil
}

func (n *naive) removeLock(id, w, pt string, at int64) error {
	if id == "" || w == "" || pt == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[w]; !ok {
		return nfail("NotFound")
	}
	lk := p.locks[w+"|"+pt]
	if lk == nil {
		return nfail("NotFound")
	}
	if !p.workers[w] {
		return nfail("PermissionDenied")
	}
	if p.overdue && p.phase != "completed" {
		return nfail("PermissionDenied")
	}
	if p.phase != "completed" {
		return nfail("StateNotAllowed")
	}
	if lk.removed {
		return nfail("StateNotAllowed")
	}
	n.advance(at)
	lk.removed = true
	n.log("remove_lock:" + id + ":" + w + "@" + pt)
	return nil
}

func (n *naive) beginTrial(id, actor string, at int64) error {
	if id == "" || actor == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[actor]; !ok {
		return nfail("NotFound")
	}
	if actor != p.applicant {
		return nfail("PermissionDenied")
	}
	if p.phase != "working" {
		return nfail("StateNotAllowed")
	}
	if p.overdue {
		return nfail("StateNotAllowed")
	}
	if len(p.inside) != 0 {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	for _, lk := range p.locks {
		if !lk.removed && !lk.trialRemoved {
			lk.trialRemoved = true
		}
	}
	p.phase = "trial"
	n.log("begin_trial:" + id)
	return nil
}

func (n *naive) endTrial(id, actor string, at int64) error {
	if id == "" || actor == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[actor]; !ok {
		return nfail("NotFound")
	}
	if actor != p.applicant {
		return nfail("PermissionDenied")
	}
	if p.phase != "trial" {
		return nfail("StateNotAllowed")
	}
	for _, lk := range p.locks {
		if lk.trialRemoved && !lk.removed {
			return nfail("ConditionNotMet")
		}
	}
	n.advance(at)
	p.phase = "effective"
	n.log("end_trial:" + id)
	return nil
}

func (n *naive) forceRemoveLock(id, pt, w, sup, con, reason string, at int64) error {
	if id == "" || pt == "" || w == "" || sup == "" || con == "" {
		return nfail("InvalidParam")
	}
	if reason == "" {
		return nfail("InvalidParam")
	}
	if err := n.checkClock(at); err != nil {
		return err
	}
	p, ok := n.permits[id]
	if !ok {
		return nfail("NotFound")
	}
	s, ok := n.persons[sup]
	if !ok {
		return nfail("NotFound")
	}
	c, ok := n.persons[con]
	if !ok {
		return nfail("NotFound")
	}
	if _, ok := n.persons[w]; !ok {
		return nfail("NotFound")
	}
	lk := p.locks[w+"|"+pt]
	if lk == nil {
		return nfail("NotFound")
	}
	if !s.roles["supervisor"] {
		return nfail("PermissionDenied")
	}
	if !c.roles["supervisor"] {
		return nfail("PermissionDenied")
	}
	if !p.workers[w] {
		return nfail("PermissionDenied")
	}
	if !p.overdue || p.phase == "completed" {
		return nfail("StateNotAllowed")
	}
	if lk.removed {
		return nfail("StateNotAllowed")
	}
	if sup == con {
		return nfail("ConditionNotMet")
	}
	n.advance(at)
	lk.removed = true
	lk.trialRemoved = false
	lk.forced = true
	n.log("force_remove_lock:" + id + ":" + w + "@" + pt)
	return nil
}

// canEnergize 朴素版：线性扫描全部历史票来回答两个条件。
func (n *naive) canEnergize(device string, at int64) (bool, error) {
	if device == "" || at < 0 {
		return false, nfail("InvalidParam")
	}
	if at < n.clock {
		return false, nfail("ClockRollback")
	}
	points, ok := n.devices[device]
	if !ok {
		return false, nfail("NotFound")
	}
	// 条件 1：任一历史票在该设备依赖点上存在物理在位锁即阻断。
	for _, p := range n.permits {
		for _, lk := range p.locks {
			if lk.removed || lk.trialRemoved {
				continue
			}
			if points[lk.point] {
				return false, nil
			}
		}
	}
	// 条件 2：涉及该设备、且处于 working/trial 之外占用态的票。
	for _, p := range n.permits {
		if !p.devices[device] || !occupiedN(p.phase) {
			continue
		}
		if p.overdue {
			return false, nil
		}
		if p.phase != "working" && p.phase != "trial" {
			return false, nil
		}
	}
	return true, nil
}

type nState struct {
	phase   string
	overdue bool
	locks   []string // worker@point 物理在位
	inside  []string
	approve int
}
