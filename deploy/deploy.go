// Package deploy coordinates deployment requests across environments
// with approval gates and per-environment mutual-exclusion locks.
//
// All methods are safe for concurrent use; the result equals some
// serial order. The clock is monotonic: every operation that passes
// parameter and clock checks advances the clock and lands all due
// timeouts before doing its own work.
package deploy

import (
	"container/heap"
	"errors"
	"fmt"
	"sync"

	"ontology/approval"
	"ontology/envlock"
)

const (
	maxNow = int64(1_000_000_000_000)
	maxVer = int64(1_000_000_000)
	maxDur = int64(1_000_000_000)
	maxK   = 5
)

// Perm is a caller permission bit.
type Perm uint8

const (
	PermDeploy Perm = 1 << iota
	PermApprove
	PermRollback
	PermAdmin
)

const permMask = PermDeploy | PermApprove | PermRollback | PermAdmin

// Caller identifies who performs an operation and with which permissions.
type Caller struct {
	User  string
	Perms Perm
}

// Status is the lifecycle state of a deployment request.
type Status int

const (
	Pending Status = iota
	Queued
	Running
	Succeeded
	Failed
	TimedOut
	Stale
	Expired
	Cancelled
)

var statusNames = [...]string{
	Pending:   "Pending",
	Queued:    "Queued",
	Running:   "Running",
	Succeeded: "Succeeded",
	Failed:    "Failed",
	TimedOut:  "TimedOut",
	Stale:     "Stale",
	Expired:   "Expired",
	Cancelled: "Cancelled",
}

func (s Status) String() string {
	if s < 0 || int(s) >= len(statusNames) {
		return "Unknown"
	}
	return statusNames[s]
}

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidParam      = errors.New("deploy: invalid parameter")
	ErrClockRegression   = errors.New("deploy: clock regression")
	ErrPermission        = errors.New("deploy: permission denied")
	ErrNotFound          = errors.New("deploy: no such environment or request")
	ErrNotOwner          = errors.New("deploy: not the request owner")
	ErrBadState          = errors.New("deploy: request in wrong state")
	ErrStaleVersion      = errors.New("deploy: stale version")
	ErrUnknownVersion    = errors.New("deploy: unknown version")
	ErrSelfApproval      = errors.New("deploy: self approval")
	ErrDuplicateApproval = approval.ErrDuplicateApproval
)

// EnvSpec configures one environment.
type EnvSpec struct {
	Name string
	K    int   // approvals required (0..5); rollbacks require K+1
	TTL  int64 // approval validity window in seconds (1..1e9)
	T    int64 // run time limit in seconds (1..1e9)
}

// Info is a read-only snapshot of a request.
type Info struct {
	ID       int64
	Env      string
	Ver      int64
	Rollback bool
	Owner    string
	Need     int
	Status   Status
	Start    int64
}

type envState struct {
	spec EnvSpec
	cur  int64
	succ map[int64]bool
	lock envlock.Lock
}

type request struct {
	id       int64
	env      string
	ver      int64
	rollback bool
	owner    string
	need     int
	status   Status
	start    int64
	ticket   *approval.Ticket
}

// timeoutEntry is a heap item for a Running request's deadline.
// Entries are removed lazily: an entry whose request is no longer
// Running is skipped without counting as examined.
type timeoutEntry struct {
	deadline int64
	id       int64
}

type timeoutHeap []timeoutEntry

func (h timeoutHeap) Len() int { return len(h) }

func (h timeoutHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}

func (h timeoutHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *timeoutHeap) Push(x any) { *h = append(*h, x.(timeoutEntry)) }

func (h *timeoutHeap) Pop() any {
	old := *h
	n := len(old)
	entry := old[n-1]
	*h = old[:n-1]
	return entry
}

// Coordinator is the deployment coordinator.
type Coordinator struct {
	mu       sync.Mutex
	envs     map[string]*envState
	reqs     map[int64]*request
	nextID   int64
	maxNow   int64
	due      timeoutHeap
	examined int // Running records examined by the last landing pass
	landed   int // timeouts landed by the last landing pass
}

// New validates the environment specs and builds a coordinator.
func New(specs []EnvSpec) (*Coordinator, error) {
	c := &Coordinator{
		envs:   make(map[string]*envState, len(specs)),
		reqs:   make(map[int64]*request),
		nextID: 1,
	}
	for _, spec := range specs {
		if spec.Name == "" || spec.K < 0 || spec.K > maxK ||
			spec.TTL < 1 || spec.TTL > maxDur || spec.T < 1 || spec.T > maxDur {
			return nil, fmt.Errorf("%w: env %+v", ErrInvalidParam, spec)
		}
		if _, dup := c.envs[spec.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate env %q", ErrInvalidParam, spec.Name)
		}
		c.envs[spec.Name] = &envState{spec: spec, succ: make(map[int64]bool)}
	}
	return c, nil
}

func checkNow(now int64) error {
	if now < 0 || now > maxNow {
		return fmt.Errorf("%w: now=%d", ErrInvalidParam, now)
	}
	return nil
}

func checkCaller(caller Caller) error {
	if caller.User == "" || caller.Perms&^permMask != 0 {
		return fmt.Errorf("%w: caller %+v", ErrInvalidParam, caller)
	}
	return nil
}

// lock advances the clock to now and lands all due timeouts. The caller
// must hold c.mu and now must not be a regression.
func (c *Coordinator) advance(now int64) {
	c.maxNow = now
	c.examined, c.landed = 0, 0
	for {
		for len(c.due) > 0 && !c.alive(c.due[0]) {
			heap.Pop(&c.due)
		}
		if len(c.due) == 0 {
			return
		}
		top := c.due[0]
		c.examined++
		if top.deadline > now {
			return
		}
		heap.Pop(&c.due)
		r := c.reqs[top.id]
		e := c.envs[r.env]
		r.status = TimedOut
		e.lock.Release()
		c.landed++
		// Re-grant at the expiry moment, not the discovery moment;
		// the new Running request may itself already be due, cascading.
		c.grant(e, r.start+e.spec.T)
	}
}

func (c *Coordinator) alive(entry timeoutEntry) bool {
	r, ok := c.reqs[entry.id]
	return ok && r.status == Running
}

// grant re-checks version and approvals at the grant moment g and moves
// the queue head to Running while the environment is free. Stale is
// checked before Expired.
func (c *Coordinator) grant(e *envState, g int64) {
	for e.lock.Running() == 0 {
		id, ok := e.lock.Peek()
		if !ok {
			return
		}
		r := c.reqs[id]
		switch {
		case !r.rollback && r.ver <= e.cur, r.rollback && r.ver >= e.cur:
			r.status = Stale
			e.lock.Pop()
		case r.need > 0 && r.ticket.ValidCount(g) < r.need:
			r.status = Expired
			e.lock.Pop()
		default:
			r.status = Running
			r.start = g
			e.lock.Acquire(id)
			heap.Push(&c.due, timeoutEntry{deadline: g + e.spec.T, id: id})
		}
	}
}

// gate performs the shared prologue of every mutating operation: lock,
// reject clock regression, advance the clock and land due timeouts.
// It returns false (with the lock released) on clock regression.
func (c *Coordinator) gate(now int64) bool {
	c.mu.Lock()
	if now < c.maxNow {
		c.mu.Unlock()
		return false
	}
	c.advance(now)
	return true
}

// Request submits a deployment (rollback=false) or rollback request and
// returns its global ID. Rejected requests consume no ID.
func (c *Coordinator) Request(now int64, env string, ver int64, rollback bool, caller Caller) (int64, error) {
	if err := checkNow(now); err != nil {
		return 0, err
	}
	if env == "" || ver < 1 || ver > maxVer {
		return 0, fmt.Errorf("%w: env=%q ver=%d", ErrInvalidParam, env, ver)
	}
	if err := checkCaller(caller); err != nil {
		return 0, err
	}
	if !c.gate(now) {
		return 0, fmt.Errorf("%w: now=%d", ErrClockRegression, now)
	}
	defer c.mu.Unlock()
	needPerm := PermDeploy
	if rollback {
		needPerm = PermRollback
	}
	if caller.Perms&needPerm == 0 {
		return 0, fmt.Errorf("%w: %s cannot request (rollback=%v)", ErrPermission, caller.User, rollback)
	}
	e, ok := c.envs[env]
	if !ok {
		return 0, fmt.Errorf("%w: env %q", ErrNotFound, env)
	}
	need := e.spec.K
	if rollback {
		need++
		if ver >= e.cur {
			return 0, fmt.Errorf("%w: rollback ver=%d cur=%d", ErrStaleVersion, ver, e.cur)
		}
		if !e.succ[ver] {
			return 0, fmt.Errorf("%w: rollback ver=%d", ErrUnknownVersion, ver)
		}
	} else if ver <= e.cur {
		return 0, fmt.Errorf("%w: ver=%d cur=%d", ErrStaleVersion, ver, e.cur)
	}
	id := c.nextID
	c.nextID++
	r := &request{
		id: id, env: env, ver: ver, rollback: rollback, owner: caller.User,
		need: need, status: Pending, ticket: approval.NewTicket(e.spec.TTL),
	}
	c.reqs[id] = r
	if need == 0 {
		r.status = Queued
		e.lock.Enqueue(id)
		c.grant(e, now)
	}
	return id, nil
}

// Approve records caller's approval of a Pending request at time now.
func (c *Coordinator) Approve(now, id int64, caller Caller) error {
	if err := checkNow(now); err != nil {
		return err
	}
	if err := checkCaller(caller); err != nil {
		return err
	}
	if !c.gate(now) {
		return fmt.Errorf("%w: now=%d", ErrClockRegression, now)
	}
	defer c.mu.Unlock()
	if caller.Perms&PermApprove == 0 {
		return fmt.Errorf("%w: %s cannot approve", ErrPermission, caller.User)
	}
	r, ok := c.reqs[id]
	if !ok {
		return fmt.Errorf("%w: request %d", ErrNotFound, id)
	}
	if r.status != Pending {
		return fmt.Errorf("%w: request %d is %s", ErrBadState, id, r.status)
	}
	if r.owner == caller.User {
		return fmt.Errorf("%w: %s owns request %d", ErrSelfApproval, caller.User, id)
	}
	if err := r.ticket.Add(caller.User, now); err != nil {
		return fmt.Errorf("%w: %s on request %d", err, caller.User, id)
	}
	if r.ticket.ValidCount(now) >= r.need {
		r.status = Queued
		e := c.envs[r.env]
		e.lock.Enqueue(id)
		c.grant(e, now)
	}
	return nil
}

// Finish completes a Running request. On ok the environment's current
// version becomes the request's version and joins the success set.
func (c *Coordinator) Finish(now, id int64, ok bool, caller Caller) error {
	if err := checkNow(now); err != nil {
		return err
	}
	if err := checkCaller(caller); err != nil {
		return err
	}
	if !c.gate(now) {
		return fmt.Errorf("%w: now=%d", ErrClockRegression, now)
	}
	defer c.mu.Unlock()
	if caller.Perms&PermDeploy == 0 {
		return fmt.Errorf("%w: %s cannot finish", ErrPermission, caller.User)
	}
	r, found := c.reqs[id]
	if !found {
		return fmt.Errorf("%w: request %d", ErrNotFound, id)
	}
	if r.status != Running {
		return fmt.Errorf("%w: request %d is %s", ErrBadState, id, r.status)
	}
	e := c.envs[r.env]
	if ok {
		r.status = Succeeded
		e.cur = r.ver
		e.succ[r.ver] = true
	} else {
		r.status = Failed
	}
	e.lock.Release()
	c.grant(e, now)
	return nil
}

// Cancel cancels a Pending, Queued or Running request. Without Admin
// permission only the owner may cancel.
func (c *Coordinator) Cancel(now, id int64, caller Caller) error {
	if err := checkNow(now); err != nil {
		return err
	}
	if err := checkCaller(caller); err != nil {
		return err
	}
	if !c.gate(now) {
		return fmt.Errorf("%w: now=%d", ErrClockRegression, now)
	}
	defer c.mu.Unlock()
	if caller.Perms&(PermDeploy|PermAdmin) == 0 {
		return fmt.Errorf("%w: %s cannot cancel", ErrPermission, caller.User)
	}
	r, ok := c.reqs[id]
	if !ok {
		return fmt.Errorf("%w: request %d", ErrNotFound, id)
	}
	if caller.Perms&PermAdmin == 0 && r.owner != caller.User {
		return fmt.Errorf("%w: %s does not own request %d", ErrNotOwner, caller.User, id)
	}
	e := c.envs[r.env]
	switch r.status {
	case Pending:
		r.status = Cancelled
	case Queued:
		r.status = Cancelled
		e.lock.Remove(id)
	case Running:
		r.status = Cancelled
		e.lock.Release()
		c.grant(e, now)
	default:
		return fmt.Errorf("%w: request %d is %s", ErrBadState, id, r.status)
	}
	return nil
}

// Tick advances the clock and lands due timeouts; it does nothing else.
func (c *Coordinator) Tick(now int64) error {
	if err := checkNow(now); err != nil {
		return err
	}
	if !c.gate(now) {
		return fmt.Errorf("%w: now=%d", ErrClockRegression, now)
	}
	c.mu.Unlock()
	return nil
}

// Get returns the state of a request as of the last landing; it does
// not advance the clock or virtually land timeouts.
func (c *Coordinator) Get(id int64) (Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.reqs[id]
	if !ok {
		return Info{}, fmt.Errorf("%w: request %d", ErrNotFound, id)
	}
	return Info{
		ID: r.id, Env: r.env, Ver: r.ver, Rollback: r.rollback,
		Owner: r.owner, Need: r.need, Status: r.status, Start: r.start,
	}, nil
}
