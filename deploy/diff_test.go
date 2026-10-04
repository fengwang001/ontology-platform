package deploy

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// 朴素逐步模拟器：完全按题面规则用独立数据结构重放，不引用实现内部逻辑。

type simStatus int

const (
	simPending simStatus = iota
	simQueued
	simRunning
	simSucceeded
	simFailed
	simTimedOut
	simStale
	simExpired
	simCanceled
)

type simRequest struct {
	id       int64
	env      string
	ver      int64
	rollback bool
	owner    string
	status   simStatus
	start    int64
	need     int
	votes    map[string]int64 // approver -> 批准时刻
}

type simEnv struct {
	k, ttl, t int64
	cur       int64
	succeeded map[int64]bool
	queue     []int64
	holder    int64
	holderVer int64
	holderRB  bool
}

type simModel struct {
	envs map[string]*simEnv
	reqs map[int64]*simRequest
	seq  int64
	now  int64
}

func newSimModel(cfgs []EnvConfig) *simModel {
	m := &simModel{envs: map[string]*simEnv{}, reqs: map[int64]*simRequest{}}
	for _, c := range cfgs {
		m.envs[c.Name] = &simEnv{k: int64(c.K), ttl: c.TTL, t: c.T, succeeded: map[int64]bool{}}
	}
	return m
}

func (m *simModel) validVotes(r *simRequest, g int64) int {
	n := 0
	for _, at := range r.votes {
		if g < at+m.envs[r.env].ttl {
			n++
		}
	}
	return n
}

// grantAt 模拟在逻辑时刻 g 给环境 e 授锁（e 当前必须空闲）。
// 返回新入锁的请求 id（0 表示无）。
func (m *simModel) grantAt(e *simEnv, g int64) int64 {
	for len(e.queue) > 0 {
		rid := e.queue[0]
		e.queue = e.queue[1:]
		r := m.reqs[rid]
		if (!r.rollback && r.ver <= e.cur) || (r.rollback && r.ver >= e.cur) {
			r.status = simStale
			continue
		}
		if r.need > 0 && m.validVotes(r, g) < r.need {
			r.status = simExpired
			continue
		}
		e.holder = rid
		e.holderVer = r.ver
		e.holderRB = r.rollback
		r.status = simRunning
		r.start = g
		return rid
	}
	return 0
}

// land 模拟超时落地的完整级联：每次只有最早到期的 Running 能超时，
// 以到期时刻 g=start+T 授锁，新 Running 若 g+T<=now 继续超时。
func (m *simModel) land(now int64) {
	for {
		// 找全局最早到期者。
		var earliest *simRequest
		var due int64
		for _, r := range m.reqs {
			if r.status != simRunning {
				continue
			}
			d := r.start + m.envs[r.env].t
			if earliest == nil || d < due {
				earliest, due = r, d
			}
		}
		if earliest == nil || due > now {
			return
		}
		e := m.envs[earliest.env]
		e.holder, e.holderVer, e.holderRB = 0, 0, false
		earliest.status = simTimedOut
		m.grantAt(e, due)
	}
}

type simResult struct {
	id  int64
	err error
}

func permHas(p, bit Perm) bool { return p&bit != 0 }

// step 在模拟器上执行一条操作，返回 (id, error)；op 为与真实实现相同的参数。
type op struct {
	kind   string
	now    int64
	env    string
	ver    int64
	rb, ok bool
	id     int64
	caller Caller
}

func (m *simModel) step(o op) simResult {
	// 参数合法性检查（各操作各自需要的字段）。
	badArg := o.now < 0 || o.now > 1e12 || o.caller.User == "" ||
		o.caller.Perms&^permMask != 0
	switch o.kind {
	case "request":
		badArg = badArg || o.env == "" || o.ver < 1 || o.ver > 1e9
	case "tick":
		badArg = o.now < 0 || o.now > 1e12
	default:
		badArg = badArg || o.id <= 0
	}
	if badArg {
		return simResult{err: ErrInvalidArgument}
	}
	if o.now < m.now {
		return simResult{err: ErrClockBackwards}
	}
	m.now = o.now
	m.land(o.now)

	switch o.kind {
	case "tick":
		return simResult{}
	case "request":
		needPerm := PermDeploy
		if o.rb {
			needPerm = PermRollback
		}
		if !permHas(o.caller.Perms, needPerm) {
			return simResult{err: ErrPermission}
		}
		e, ok := m.envs[o.env]
		if !ok {
			return simResult{err: ErrNotFound}
		}
		if (!o.rb && o.ver <= e.cur) || (o.rb && o.ver >= e.cur) {
			return simResult{err: ErrVersionStale}
		}
		if o.rb && !e.succeeded[o.ver] {
			return simResult{err: ErrUnknownVersion}
		}
		m.seq++
		need := int(e.k)
		if o.rb {
			need++
		}
		r := &simRequest{
			id: m.seq, env: o.env, ver: o.ver, rollback: o.rb,
			owner: o.caller.User, need: need, votes: map[string]int64{},
		}
		if need == 0 {
			r.status = simQueued
			e.queue = append(e.queue, r.id)
		} else {
			r.status = simPending
		}
		m.reqs[r.id] = r
		if need == 0 && e.holder == 0 {
			m.grantAt(e, o.now)
		}
		return simResult{id: r.id}
	case "approve":
		if !permHas(o.caller.Perms, PermApprove) {
			return simResult{err: ErrPermission}
		}
		r, ok := m.reqs[o.id]
		if !ok {
			return simResult{err: ErrNotFound}
		}
		if r.status != simPending {
			return simResult{err: ErrInvalidState}
		}
		if o.caller.User == r.owner {
			return simResult{err: ErrSelfApproval}
		}
		ttl := m.envs[r.env].ttl
		if at, dup := r.votes[o.caller.User]; dup && o.now < at+ttl {
			return simResult{err: ErrDuplicateApproval}
		}
		r.votes[o.caller.User] = o.now
		if m.validVotes(r, o.now) >= r.need {
			r.status = simQueued
			e := m.envs[r.env]
			e.queue = append(e.queue, r.id)
			if e.holder == 0 {
				m.grantAt(e, o.now)
			}
		}
		return simResult{}
	case "finish":
		if !permHas(o.caller.Perms, PermDeploy) {
			return simResult{err: ErrPermission}
		}
		r, ok := m.reqs[o.id]
		if !ok {
			return simResult{err: ErrNotFound}
		}
		if r.status != simRunning {
			return simResult{err: ErrInvalidState}
		}
		e := m.envs[r.env]
		e.holder, e.holderVer, e.holderRB = 0, 0, false
		if o.ok {
			r.status = simSucceeded
			e.cur = r.ver
			e.succeeded[r.ver] = true
		} else {
			r.status = simFailed
		}
		m.grantAt(e, o.now)
		return simResult{}
	case "cancel":
		if !permHas(o.caller.Perms, PermDeploy) && !permHas(o.caller.Perms, PermAdmin) {
			return simResult{err: ErrPermission}
		}
		r, ok := m.reqs[o.id]
		if !ok {
			return simResult{err: ErrNotFound}
		}
		if !permHas(o.caller.Perms, PermAdmin) && o.caller.User != r.owner {
			return simResult{err: ErrNotOwner}
		}
		switch r.status {
		case simPending:
			r.status = simCanceled
		case simQueued:
			e := m.envs[r.env]
			for i, q := range e.queue {
				if q == r.id {
					e.queue = append(e.queue[:i], e.queue[i+1:]...)
					break
				}
			}
			r.status = simCanceled
		case simRunning:
			e := m.envs[r.env]
			e.holder, e.holderVer, e.holderRB = 0, 0, false
			r.status = simCanceled
			m.grantAt(e, o.now)
		default:
			return simResult{err: ErrInvalidState}
		}
		return simResult{}
	}
	return simResult{err: ErrInvalidArgument}
}

var simStatusNames = map[simStatus]string{
	simPending: "Pending", simQueued: "Queued", simRunning: "Running",
	simSucceeded: "Succeeded", simFailed: "Failed", simTimedOut: "TimedOut",
	simStale: "Stale", simExpired: "Expired", simCanceled: "Canceled",
}

func statusMatch(a Status, b simStatus) bool {
	return a.String() == simStatusNames[b]
}

// TestRandomDifferential 以 1500 组随机操作序列对照实现与朴素模拟。
// -v 时逐操作打印输入、双方输出与判定依据（错误身份、编号、最终全量状态）。
func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	rng2 := rand.New(rand.NewSource(20261005))
	for g := 0; g < groups; g++ {
		runOneGroup(t, rng2, g)
	}
}

func runOneGroup(t *testing.T, rng *rand.Rand, group int) {
	t.Helper()
	envNames := []string{"prod", "stage", "dev"}
	cfgs := make([]EnvConfig, len(envNames))
	for i, n := range envNames {
		cfgs[i] = EnvConfig{
			Name: n,
			K:    rng.Intn(4), // 0..3，回滚最多 4 票
			TTL:  int64(1 + rng.Intn(20)),
			T:    int64(1 + rng.Intn(15)),
		}
	}
	c, err := New(cfgs)
	if err != nil {
		t.Fatalf("group %d New: %v", group, err)
	}
	sim := newSimModel(cfgs)

	users := []string{"alice", "bob", "carol", "dave"}
	userPerm := map[string]Perm{
		"alice": PermDeploy | PermApprove | PermRollback,
		"bob":   PermDeploy | PermApprove,
		"carol": PermApprove | PermAdmin,
		"dave":  PermDeploy,
	}
	var log []string
	now := int64(0)

	errName := func(e error) string {
		if e == nil {
			return "nil"
		}
		return e.Error()
	}

	nOps := 20 + rng.Intn(40)
	for i := 0; i < nOps; i++ {
		o := op{}
		// 时钟：大多数前进，偶尔不动，少量构造回退（用于覆盖拒绝次序）。
		switch rng.Intn(10) {
		case 0, 1, 2:
			now += int64(rng.Intn(5))
		case 9:
			now--
		}
		if now < 0 {
			now = 0
		}
		o.now = now
		user := users[rng.Intn(len(users))]
		o.caller = Caller{User: user, Perms: userPerm[user]}

		switch rng.Intn(6) {
		case 0:
			o.kind = "request"
			o.env = envNames[rng.Intn(len(envNames))]
			o.ver = int64(1 + rng.Intn(10))
			o.rb = rng.Intn(3) == 0
		case 1:
			o.kind = "approve"
			o.id = int64(1 + rng.Intn(int(sim.seq)+3))
		case 2:
			o.kind = "finish"
			o.id = int64(1 + rng.Intn(int(sim.seq)+3))
			o.ok = rng.Intn(2) == 0
		case 3:
			o.kind = "cancel"
			o.id = int64(1 + rng.Intn(int(sim.seq)+3))
		case 4:
			o.kind = "tick"
		default:
			o.kind = "request"
			o.env = envNames[rng.Intn(len(envNames))]
			o.ver = int64(1 + rng.Intn(10))
		}

		var gotID int64
		var gotErr error
		switch o.kind {
		case "request":
			gotID, gotErr = c.Request(o.now, o.env, o.ver, o.rb, o.caller)
		case "approve":
			gotErr = c.Approve(o.now, o.id, o.caller)
		case "finish":
			gotErr = c.Finish(o.now, o.id, o.ok, o.caller)
		case "cancel":
			gotErr = c.Cancel(o.now, o.id, o.caller)
		case "tick":
			gotErr = c.Tick(o.now)
		}
		want := sim.step(o)

		basis := fmt.Sprintf("real=(id=%d err=%s) sim=(id=%d err=%s)",
			gotID, errName(gotErr), want.id, errName(want.err))
		log = append(log, fmt.Sprintf("op%02d %-8s now=%-3d env=%-5q ver=%-2d rb=%-5v ok=%-5v id=%-3d by=%-5q => %s",
			i, o.kind, o.now, o.env, o.ver, o.rb, o.ok, o.id, o.caller.User, basis))

		if !errors.Is(gotErr, want.err) || (want.err == nil && gotID != want.id) {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("group %d op %d mismatch: got id=%d err=%v; want id=%d err=%v",
				group, i, gotID, gotErr, want.id, want.err)
		}
	}

	// 最终把时钟推到足够远，落地全部超时，再全量比对请求状态与 cur/成功集合。
	final := now + 1_000_000
	if err := c.Tick(final); err != nil {
		t.Fatalf("final tick: %v", err)
	}
	sim.step(op{kind: "tick", now: final})

	for id, sr := range sim.reqs {
		s, gerr := c.Get(id)
		if gerr != nil {
			t.Fatalf("group %d Get(%d): %v", group, id, gerr)
		}
		if !statusMatch(s.Status, sr.status) || s.Start != sr.start {
			for _, l := range log {
				t.Log(l)
			}
			t.Fatalf("group %d id=%d status real=%s(start=%d) sim=%s(start=%d)",
				group, id, s.Status, s.Start, simStatusNames[sr.status], sr.start)
		}
	}
	for name, se := range sim.envs {
		ge := c.envs[name]
		if ge.lock.Cur() != se.cur {
			t.Fatalf("group %d env=%s cur real=%d sim=%d", group, name, ge.lock.Cur(), se.cur)
		}
		if ge.lock.Holder() != se.holder {
			t.Fatalf("group %d env=%s holder real=%d sim=%d", group, name, ge.lock.Holder(), se.holder)
		}
		for v := range se.succeeded {
			if !ge.lock.Succeeded(v) {
				t.Fatalf("group %d env=%s ver=%d missing in real succeeded set", group, name, v)
			}
		}
		if ge.lock.QueueLen() != len(se.queue) {
			t.Fatalf("group %d env=%s queue len real=%d sim=%d", group, name, ge.lock.QueueLen(), len(se.queue))
		}
	}

	// 逐步日志（默认也输出，方便复放；失败时上面已打印）。
	if testing.Verbose() {
		for _, l := range log {
			t.Logf("group %04d %s", group, l)
		}
	}
}
