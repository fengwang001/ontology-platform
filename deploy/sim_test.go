package deploy

import (
	"errors"
	"fmt"
	"sort"

	"ontology/approval"
)

type op struct {
	name     string
	now      int64
	env      string
	id       int64
	ver      int64
	rollback bool
	ok       bool
	caller   Caller
}

type opResult struct {
	id  int64
	err error
}

type snapshotEntry struct {
	id, ver, start int64
	env, user      string
	rollback       bool
	status         Status
	need           int
	tickets        []approval.Ticket
}

type snapshot struct {
	cur     map[string]int64
	entries []snapshotEntry
}

type simEnv struct {
	k, ttl, t, cur int64
	known          map[int64]bool
	running        int64
	queue          []int64
}

type simReq struct {
	id, ver, start, need int64
	env, user            string
	rollback             bool
	status               Status
	votes                map[string]approval.Ticket
}

type simTimer struct{ deadline, id int64 }

type naiveModel struct {
	envs   map[string]*simEnv
	reqs   map[int64]*simReq
	timers []simTimer
	nextID int64
	now    int64
}

var (
	alice  = Caller{User: "alice", Permissions: Deploy | Rollback | Admin}
	bob    = Caller{User: "bob", Permissions: Deploy | Approve}
	carol  = Caller{User: "carol", Permissions: Approve}
	dave   = Caller{User: "dave", Permissions: Deploy}
	noperm = Caller{User: "noperm"}
)

func newNaiveModel(configs []EnvConfig) *naiveModel {
	m := &naiveModel{
		envs: make(map[string]*simEnv, len(configs)),
		reqs: make(map[int64]*simReq),
	}
	for _, cfg := range configs {
		m.envs[cfg.Name] = &simEnv{
			k: int64(cfg.K), ttl: cfg.TTL, t: cfg.T,
			known: map[int64]bool{},
		}
	}
	return m
}

func validTestCaller(c Caller) bool {
	return c.User != "" && c.Permissions&^(Deploy|Approve|Rollback|Admin) == 0
}

func (m *naiveModel) advance(now int64) error {
	if now < 0 || now > 1e12 {
		return ErrInvalidArgument
	}
	if now < m.now {
		return ErrClockRewind
	}
	m.now = now
	for {
		best := -1
		for i, timer := range m.timers {
			if timer.deadline <= now && (best == -1 || timer.deadline < m.timers[best].deadline ||
				timer.deadline == m.timers[best].deadline && timer.id < m.timers[best].id) {
				best = i
			}
		}
		if best < 0 {
			return nil
		}
		timer := m.timers[best]
		m.timers = append(m.timers[:best], m.timers[best+1:]...)
		req := m.reqs[timer.id]
		if req.status != Running {
			continue
		}
		env := m.envs[req.env]
		env.running = 0
		req.status = TimedOut
		m.grant(req.env, timer.deadline)
	}
}

func (m *naiveModel) validVotes(req *simReq, at int64) int {
	count := 0
	for _, ticket := range req.votes {
		if at < ticket.At+ticket.TTL {
			count++
		}
	}
	return count
}

func (m *naiveModel) grant(envName string, at int64) {
	env := m.envs[envName]
	if env.running != 0 {
		return
	}
	for len(env.queue) > 0 {
		id := env.queue[0]
		env.queue = env.queue[1:]
		req := m.reqs[id]
		if (!req.rollback && req.ver <= env.cur) || (req.rollback && req.ver >= env.cur) {
			req.status = Stale
			continue
		}
		if req.need > 0 && int64(m.validVotes(req, at)) < req.need {
			req.status = Expired
			continue
		}
		req.status = Running
		req.start = at
		env.running = id
		m.timers = append(m.timers, simTimer{at + env.t, id})
		return
	}
}

func (m *naiveModel) request(in op) opResult {
	if !validTestCaller(in.caller) || in.env == "" || in.ver < 1 || in.ver > 1e9 {
		return opResult{err: ErrInvalidArgument}
	}
	if err := m.advance(in.now); err != nil {
		return opResult{err: err}
	}
	required := Deploy
	if in.rollback {
		required = Rollback
	}
	if !in.caller.Permissions.Has(required) {
		return opResult{err: ErrPermission}
	}
	env, ok := m.envs[in.env]
	if !ok {
		return opResult{err: ErrNotFound}
	}
	if in.rollback {
		if in.ver >= env.cur {
			return opResult{err: ErrStale}
		}
		if !env.known[in.ver] {
			return opResult{err: ErrUnknownVersion}
		}
	} else if in.ver <= env.cur {
		return opResult{err: ErrStale}
	}
	need := env.k
	if in.rollback {
		need++
	}
	m.nextID++
	req := &simReq{id: m.nextID, env: in.env, ver: in.ver, rollback: in.rollback,
		user: in.caller.User, need: need, votes: map[string]approval.Ticket{}}
	m.reqs[req.id] = req
	if need == 0 {
		req.status = Queued
		env.queue = append(env.queue, req.id)
		m.grant(in.env, in.now)
	} else {
		req.status = Pending
	}
	return opResult{id: req.id}
}

func (m *naiveModel) approve(in op) opResult {
	if !validTestCaller(in.caller) || in.id < 1 {
		return opResult{err: ErrInvalidArgument}
	}
	if err := m.advance(in.now); err != nil {
		return opResult{err: err}
	}
	if !in.caller.Permissions.Has(Approve) {
		return opResult{err: ErrPermission}
	}
	req, ok := m.reqs[in.id]
	if !ok {
		return opResult{err: ErrNotFound}
	}
	if req.status != Pending {
		return opResult{err: ErrStatus}
	}
	if req.user == in.caller.User {
		return opResult{err: ErrSelfApproval}
	}
	if old, voted := req.votes[in.caller.User]; voted && in.now < old.At+old.TTL {
		return opResult{err: ErrDuplicateVote}
	}
	req.votes[in.caller.User] = approval.Ticket{User: in.caller.User, At: in.now, TTL: m.envs[req.env].ttl}
	if int64(m.validVotes(req, in.now)) >= req.need {
		req.status = Queued
		m.envs[req.env].queue = append(m.envs[req.env].queue, req.id)
		m.grant(req.env, in.now)
	}
	return opResult{}
}

func (m *naiveModel) finish(in op) opResult {
	if !validTestCaller(in.caller) || in.id < 1 {
		return opResult{err: ErrInvalidArgument}
	}
	if err := m.advance(in.now); err != nil {
		return opResult{err: err}
	}
	if !in.caller.Permissions.Has(Deploy) {
		return opResult{err: ErrPermission}
	}
	req, ok := m.reqs[in.id]
	if !ok {
		return opResult{err: ErrNotFound}
	}
	if req.status != Running {
		return opResult{err: ErrStatus}
	}
	env := m.envs[req.env]
	env.running = 0
	if in.ok {
		req.status = Succeeded
		env.cur = req.ver
		env.known[req.ver] = true
	} else {
		req.status = Failed
	}
	m.grant(req.env, in.now)
	return opResult{}
}

func (m *naiveModel) cancel(in op) opResult {
	if !validTestCaller(in.caller) || in.id < 1 {
		return opResult{err: ErrInvalidArgument}
	}
	if err := m.advance(in.now); err != nil {
		return opResult{err: err}
	}
	if !in.caller.Permissions.Has(Deploy) && !in.caller.Permissions.Has(Admin) {
		return opResult{err: ErrPermission}
	}
	req, ok := m.reqs[in.id]
	if !ok {
		return opResult{err: ErrNotFound}
	}
	if !in.caller.Permissions.Has(Admin) && req.user != in.caller.User {
		return opResult{err: ErrNotOwner}
	}
	env := m.envs[req.env]
	switch req.status {
	case Pending:
		req.status = Canceled
	case Queued:
		for i, id := range env.queue {
			if id == req.id {
				env.queue = append(env.queue[:i], env.queue[i+1:]...)
				break
			}
		}
		req.status = Canceled
	case Running:
		env.running = 0
		req.status = Canceled
		m.grant(req.env, in.now)
	default:
		return opResult{err: ErrStatus}
	}
	return opResult{}
}

func (m *naiveModel) apply(in op) opResult {
	switch in.name {
	case "request":
		return m.request(in)
	case "approve":
		return m.approve(in)
	case "finish":
		return m.finish(in)
	case "cancel":
		return m.cancel(in)
	case "tick":
		return opResult{err: m.advance(in.now)}
	default:
		return opResult{err: ErrInvalidArgument}
	}
}

func sortedTickets(votes map[string]approval.Ticket) []approval.Ticket {
	out := make([]approval.Ticket, 0, len(votes))
	for _, ticket := range votes {
		out = append(out, ticket)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].At < out[j].At || out[i].At == out[j].At && out[i].User < out[j].User
	})
	return out
}

func (m *naiveModel) snapshot() snapshot {
	out := snapshot{cur: map[string]int64{}, entries: make([]snapshotEntry, 0, len(m.reqs))}
	for name, env := range m.envs {
		out.cur[name] = env.cur
	}
	for _, req := range m.reqs {
		out.entries = append(out.entries, snapshotEntry{
			id: req.id, ver: req.ver, start: req.start, env: req.env, user: req.user,
			rollback: req.rollback, status: req.status, need: int(req.need), tickets: sortedTickets(req.votes),
		})
	}
	sort.Slice(out.entries, func(i, j int) bool { return out.entries[i].id < out.entries[j].id })
	return out
}

func coordinatorSnapshot(c *Coordinator) snapshot {
	out := snapshot{cur: map[string]int64{}, entries: make([]snapshotEntry, 0, len(c.entries))}
	for name, env := range c.envs {
		out.cur[name] = env.cur
	}
	for _, req := range c.entries {
		out.entries = append(out.entries, snapshotEntry{
			id: req.id, ver: req.ver, start: req.start, env: req.env.config.Name, user: req.user,
			rollback: req.rollback, status: req.status, need: req.need, tickets: req.votes.Tickets(),
		})
	}
	sort.Slice(out.entries, func(i, j int) bool { return out.entries[i].id < out.entries[j].id })
	return out
}

func applyProduction(c *Coordinator, in op) opResult {
	switch in.name {
	case "request":
		id, err := c.Request(in.now, in.env, in.ver, in.rollback, in.caller)
		return opResult{id, err}
	case "approve":
		return opResult{err: c.Approve(in.now, in.id, in.caller)}
	case "finish":
		return opResult{err: c.Finish(in.now, in.id, in.ok, in.caller)}
	case "cancel":
		return opResult{err: c.Cancel(in.now, in.id, in.caller)}
	case "tick":
		return opResult{err: c.Tick(in.now)}
	default:
		return opResult{err: ErrInvalidArgument}
	}
}

func describeOp(in op) string {
	return fmt.Sprintf("%s(now=%d env=%q id=%d ver=%d rollback=%t ok=%t user=%s perms=%d)",
		in.name, in.now, in.env, in.id, in.ver, in.rollback, in.ok, in.caller.User, in.caller.Permissions)
}

func sameError(got, want error) bool {
	return want == nil && got == nil || want != nil && errors.Is(got, want)
}

func errorName(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
