package runner_test

// naive 是题面规则的逐步朴素模拟：每步用位运算逐位维护，
// 与生产 Runner 跑同一条随机操作序列并逐字段比对。

type naiveState int

const (
	nActive  naiveState = iota // 无在跑、可能 Suspended
	nRunning                   // 一步 Running
	nCompleted
	nFailed
)

type naiveEvent struct {
	kind     string
	step     int
	at       int64
	miss     uint64
	approver string
}

type naiveInstance struct {
	trigger  string
	ceil     uint64
	reqs     []uint64
	snapshot uint64
	steps    []string // Pending/Running/Done
	next     int
	state    naiveState
	suspend  bool
	deadline int64
	miss     uint64
	outcome  string // "" / Completed / Rejected / Expired
	termAt   int64
	audit    []naiveEvent
}

type naiveModel struct {
	masks   map[string]uint64
	defs    map[string][]uint64 // def -> reqs
	ceils   map[string]uint64
	insts   map[string]*naiveInstance
	clock   int64
	timeout int64
}

func newNaive(T int64) *naiveModel {
	return &naiveModel{
		masks:   map[string]uint64{},
		defs:    map[string][]uint64{},
		ceils:   map[string]uint64{},
		insts:   map[string]*naiveInstance{},
		timeout: T,
	}
}

type naiveStatus struct {
	state    naiveState
	steps    []string
	next     int
	suspend  bool
	miss     uint64
	deadline int64
	outcome  string
	termAt   int64
}

const approverBit uint64 = uint64(1) << 63

// apply 执行一条操作。op 形如：
// grant p m / revoke p m / define def ceil r.. / launch i def p now /
// start i now / finish i now / approve i a now / reject i a now / status i now
// 返回 (错误类名, 状态或nil)。错误类名为 "" 表示成功。
func (m *naiveModel) apply(op opSpec) (string, *naiveStatus) {
	if op.kind == "grant" || op.kind == "revoke" {
		m.masks[op.p] = m.masks[op.p] | op.m
		if op.kind == "revoke" {
			m.masks[op.p] = m.masks[op.p] &^ op.m
		}
		return "", nil
	}
	if op.kind == "define" {
		return "", nil
	}
	// 以下均带 now。
	now := op.now
	if now < 0 || now > 1_000_000_000_000_000 || now < m.clock {
		return "ErrClock", nil
	}
	in := m.insts[op.inst]
	commitExpire := op.kind != "status"
	due := func() bool {
		return in != nil && in.state == nActive && in.suspend && in.runningIdx() < 0 &&
			now >= in.deadline
	}
	writeExpire := func() {
		in.state = nFailed
		in.outcome = "Expired"
		in.termAt = in.deadline
		in.suspend = false
		in.miss = 0
		in.audit = append(in.audit, naiveEvent{
			kind: "Expire", step: in.next, at: in.deadline,
		})
		m.clock = now
	}
	switch op.kind {
	case "launch":
		if _, ok := m.insts[op.inst]; ok {
			return "ErrExists", nil
		}
		reqs, ok := m.defs[op.def]
		if !ok {
			return "ErrNotFound", nil
		}
		snap := m.masks[op.p] & m.ceils[op.def]
		steps := make([]string, len(reqs))
		for i := range steps {
			steps[i] = "Pending"
		}
		m.insts[op.inst] = &naiveInstance{
			trigger: op.p, ceil: m.ceils[op.def], reqs: append([]uint64(nil), reqs...),
			snapshot: snap, steps: steps, next: 0, state: nActive,
		}
		m.clock = now
		return "", nil
	case "status":
		if in == nil {
			return "ErrNotFound", nil
		}
		st := m.snapshot(in)
		if due() {
			st.state = nFailed
			st.suspend = false
			st.miss = 0
			st.outcome = "Expired"
			st.termAt = in.deadline
		}
		return "", &st
	case "start":
		if in == nil {
			return "ErrNotFound", nil
		}
		if in.state != nActive || in.runningIdx() >= 0 || in.next >= len(in.reqs) {
			return "ErrState", nil
		}
		if due() {
			if commitExpire {
				writeExpire()
			}
			return "ErrState", nil
		}
		i := in.next
		eff := m.masks[in.trigger] & in.snapshot
		req := in.reqs[i]
		if req&^eff == 0 {
			in.steps[i] = "Running"
			in.next = i + 1
			in.state = nRunning
			in.suspend = false
			in.miss = 0
			in.deadline = 0
			in.audit = append(in.audit, naiveEvent{kind: "Allow", step: i, at: now})
		} else {
			in.suspend = true
			in.miss = req &^ eff
			in.deadline = now + m.timeout
			in.audit = append(in.audit, naiveEvent{kind: "Deny", step: i, miss: in.miss, at: now})
		}
		m.clock = now
		return "", nil
	case "finish":
		if in == nil {
			return "ErrNotFound", nil
		}
		if due() {
			if commitExpire {
				writeExpire()
			}
			return "ErrState", nil
		}
		i := in.runningIdx()
		if in.state != nRunning || i < 0 {
			return "ErrState", nil
		}
		in.steps[i] = "Done"
		in.state = nActive
		if in.next >= len(in.reqs) {
			in.state = nCompleted
			in.outcome = "Completed"
			in.termAt = now
		}
		m.clock = now
		return "", nil
	case "approve", "reject":
		if in == nil {
			return "ErrNotFound", nil
		}
		if due() {
			if commitExpire {
				writeExpire()
			}
			return "ErrState", nil
		}
		if in.state != nActive || !in.suspend {
			return "ErrState", nil
		}
		if op.a == in.trigger {
			return "ErrSelf", nil
		}
		if m.masks[op.a]&approverBit == 0 {
			return "ErrNoAuthority", nil
		}
		i := in.next
		if op.kind == "approve" {
			in.steps[i] = "Running"
			in.next = i + 1
			in.state = nRunning
			in.suspend = false
			in.miss = 0
			in.deadline = 0
			in.audit = append(in.audit, naiveEvent{
				kind: "Override", step: i, at: now, approver: op.a,
			})
		} else {
			in.state = nFailed
			in.suspend = false
			in.miss = 0
			in.deadline = 0
			in.outcome = "Rejected"
			in.termAt = now
			in.audit = append(in.audit, naiveEvent{
				kind: "Reject", step: i, at: now, approver: op.a,
			})
		}
		m.clock = now
		return "", nil
	}
	return "UNKNOWN", nil
}

func (in *naiveInstance) runningIdx() int {
	for i, s := range in.steps {
		if s == "Running" {
			return i
		}
	}
	return -1
}

func (m *naiveModel) snapshot(in *naiveInstance) naiveStatus {
	steps := append([]string(nil), in.steps...)
	return naiveStatus{
		state: in.state, steps: steps, next: in.next, suspend: in.suspend,
		miss: in.miss, deadline: in.deadline, outcome: in.outcome, termAt: in.termAt,
	}
}
