package deploy

import (
	"container/heap"
	"errors"
	"sort"
	"sync"

	"ontology/approval"
	"ontology/envlock"
)

const (
	Deploy Permission = 1 << iota
	Approve
	Rollback
	Admin
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRewind     = errors.New("clock rewind")
	ErrPermission      = errors.New("permission denied")
	ErrNotFound        = errors.New("not found")
	ErrNotOwner        = errors.New("not owner")
	ErrStatus          = errors.New("invalid status")
	ErrStale           = errors.New("stale version")
	ErrUnknownVersion  = errors.New("unknown version")
	ErrSelfApproval    = errors.New("self approval")
	ErrDuplicateVote   = errors.New("duplicate approval")
)

type Permission uint64

func (p Permission) Has(required Permission) bool {
	return p&required == required
}

type Caller struct {
	User        string
	Permissions Permission
}

type EnvConfig struct {
	Name string
	K    int
	TTL  int64
	T    int64
}

type Status int

const (
	Pending Status = iota
	Queued
	Running
	Succeeded
	Failed
	Stale
	Expired
	TimedOut
	Canceled
)

type Info struct {
	ID        int64
	Env       string
	Version   int64
	Rollback  bool
	User      string
	Status    Status
	Start     int64
	Need      int
	Approvals []approval.Ticket
}

type Coordinator struct {
	mu       sync.Mutex
	envs     map[string]*envState
	entries  map[int64]*entry
	nextID   int64
	now      int64
	timers   timeoutHeap
	examined int
}

type envState struct {
	config EnvConfig
	cur    int64
	known  map[int64]struct{}
	lock   *envlock.Locker
}

type entry struct {
	id       int64
	env      *envState
	ver      int64
	rollback bool
	user     string
	status   Status
	start    int64
	need     int
	votes    *approval.Ledger
	heap     int
}

type timeout struct {
	deadline int64
	id       int64
	target   *entry
	index    int
}

type timeoutHeap []*timeout

func (h timeoutHeap) Len() int { return len(h) }

func (h timeoutHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].id < h[j].id
}

func (h timeoutHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
	if h[i].target != nil {
		h[i].target.heap = i
	}
	if h[j].target != nil {
		h[j].target.heap = j
	}
}

func (h *timeoutHeap) Push(value any) {
	timer := value.(*timeout)
	timer.index = len(*h)
	*h = append(*h, timer)
}

func (h *timeoutHeap) Pop() any {
	old := *h
	timer := old[len(old)-1]
	*h = old[:len(old)-1]
	return timer
}

func New(envs []EnvConfig) (*Coordinator, error) {
	if len(envs) == 0 {
		return nil, ErrInvalidArgument
	}
	coordinator := &Coordinator{
		envs:    make(map[string]*envState, len(envs)),
		entries: make(map[int64]*entry),
	}
	for _, config := range envs {
		if config.Name == "" || config.K < 0 || config.K > 5 ||
			config.TTL < 1 || config.TTL > 1_000_000_000 ||
			config.T < 1 || config.T > 1_000_000_000 {
			return nil, ErrInvalidArgument
		}
		if _, exists := coordinator.envs[config.Name]; exists {
			return nil, ErrInvalidArgument
		}
		coordinator.envs[config.Name] = &envState{
			config: config,
			known:  make(map[int64]struct{}),
			lock:   envlock.NewLocker(),
		}
	}
	heap.Init(&coordinator.timers)
	return coordinator, nil
}

func (c *Coordinator) Request(now int64, env string, ver int64, rollback bool, caller Caller) (int64, error) {
	if !validCaller(caller) || env == "" || ver < 1 || ver > 1_000_000_000 {
		return 0, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advance(now); err != nil {
		return 0, err
	}
	required := Deploy
	if rollback {
		required = Rollback
	}
	if !caller.Permissions.Has(required) {
		return 0, ErrPermission
	}
	target, ok := c.envs[env]
	if !ok {
		return 0, ErrNotFound
	}
	if rollback {
		if ver >= target.cur {
			return 0, ErrStale
		}
		if _, exists := target.known[ver]; !exists {
			return 0, ErrUnknownVersion
		}
	} else if ver <= target.cur {
		return 0, ErrStale
	}

	need := target.config.K
	if rollback {
		need++
	}
	c.nextID++
	targetEntry := &entry{
		id:       c.nextID,
		env:      target,
		ver:      ver,
		rollback: rollback,
		user:     caller.User,
		need:     need,
		votes:    approval.NewLedger(),
		heap:     -1,
	}
	c.entries[targetEntry.id] = targetEntry
	if need == 0 {
		targetEntry.status = Queued
		target.lock.Enqueue(targetEntry.id)
		c.grant(target, now)
	} else {
		targetEntry.status = Pending
	}
	return targetEntry.id, nil
}

func (c *Coordinator) Approve(now int64, id int64, caller Caller) error {
	if !validCaller(caller) || id < 1 || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advance(now); err != nil {
		return err
	}
	if !caller.Permissions.Has(Approve) {
		return ErrPermission
	}
	target, exists := c.entries[id]
	if !exists {
		return ErrNotFound
	}
	if target.status != Pending {
		return ErrStatus
	}
	if target.user == caller.User {
		return ErrSelfApproval
	}
	if target.votes.Cast(caller.User, now, target.env.config.TTL) {
		return ErrDuplicateVote
	}
	if target.votes.ValidCount(now) >= target.need {
		target.status = Queued
		target.env.lock.Enqueue(target.id)
		c.grant(target.env, now)
	}
	return nil
}

func (c *Coordinator) Finish(now int64, id int64, ok bool, caller Caller) error {
	if !validCaller(caller) || id < 1 || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advance(now); err != nil {
		return err
	}
	if !caller.Permissions.Has(Deploy) {
		return ErrPermission
	}
	target, exists := c.entries[id]
	if !exists {
		return ErrNotFound
	}
	if target.status != Running {
		return ErrStatus
	}
	c.stopTimer(target)
	target.env.lock.Release()
	if ok {
		target.status = Succeeded
		target.env.cur = target.ver
		target.env.known[target.ver] = struct{}{}
	} else {
		target.status = Failed
	}
	c.grant(target.env, now)
	return nil
}

func (c *Coordinator) Cancel(now int64, id int64, caller Caller) error {
	if !validCaller(caller) || id < 1 || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.advance(now); err != nil {
		return err
	}
	if !caller.Permissions.Has(Deploy) && !caller.Permissions.Has(Admin) {
		return ErrPermission
	}
	target, ok := c.entries[id]
	if !ok {
		return ErrNotFound
	}
	if !caller.Permissions.Has(Admin) && target.user != caller.User {
		return ErrNotOwner
	}
	switch target.status {
	case Pending:
		target.status = Canceled
	case Queued:
		target.env.lock.Remove(target.id)
		target.status = Canceled
	case Running:
		c.stopTimer(target)
		target.env.lock.Release()
		target.status = Canceled
		c.grant(target.env, now)
	default:
		return ErrStatus
	}
	return nil
}

func (c *Coordinator) Tick(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.advance(now)
}

func (c *Coordinator) Get(id int64) (Info, bool) {
	if id < 1 {
		return Info{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	target, ok := c.entries[id]
	if !ok {
		return Info{}, false
	}
	tickets := target.votes.Tickets()
	sort.Slice(tickets, func(i, j int) bool {
		if tickets[i].At != tickets[j].At {
			return tickets[i].At < tickets[j].At
		}
		return tickets[i].User < tickets[j].User
	})
	return Info{
		ID:        target.id,
		Env:       target.env.config.Name,
		Version:   target.ver,
		Rollback:  target.rollback,
		User:      target.user,
		Status:    target.status,
		Start:     target.start,
		Need:      target.need,
		Approvals: tickets,
	}, true
}

func validCaller(caller Caller) bool {
	if caller.User == "" {
		return false
	}
	const validPermissions = Deploy | Approve | Rollback | Admin
	return caller.Permissions&^validPermissions == 0
}

func (c *Coordinator) advance(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if now < c.now {
		return ErrClockRewind
	}
	c.now = now
	c.settle(now)
	return nil
}

func (c *Coordinator) settle(now int64) {
	c.examined = 0
	for len(c.timers) > 0 {
		c.examined++
		timer := c.timers[0]
		if timer.deadline > now {
			break
		}
		heap.Pop(&c.timers)
		target := timer.target
		target.heap = -1
		if target.status != Running {
			continue
		}
		target.env.lock.Release()
		target.status = TimedOut
		c.grant(target.env, timer.deadline)
	}
}

func (c *Coordinator) grant(target *envState, at int64) {
	target.lock.Grant(func(id int64) envlock.GrantDecision {
		queued := c.entries[id]
		if (!queued.rollback && queued.ver <= target.cur) ||
			(queued.rollback && queued.ver >= target.cur) {
			queued.status = Stale
			return envlock.GrantReject
		}
		if queued.need > 0 && queued.votes.ValidCount(at) < queued.need {
			queued.status = Expired
			return envlock.GrantReject
		}
		queued.status = Running
		queued.start = at
		timer := &timeout{
			deadline: at + target.config.T,
			id:       queued.id,
			target:   queued,
		}
		heap.Push(&c.timers, timer)
		queued.heap = timer.index
		return envlock.GrantAcquire
	})
}

func (c *Coordinator) stopTimer(target *entry) {
	if target.heap >= 0 {
		heap.Remove(&c.timers, target.heap)
		target.heap = -1
	}
}
