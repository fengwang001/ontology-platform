// Package deploy 实现带审批闸门与环境互斥锁的部署协调器。
package deploy

import (
	"container/heap"
	"sync"

	"ontology/approval"
	"ontology/envlock"
)

// Perm 是调用者的权限位集合。
type Perm uint32

const (
	PermDeploy   Perm = 1 << iota // 发起普通部署、Finish
	PermApprove                   // 批准
	PermRollback                  // 发起回滚
	PermAdmin                     // 取消他人请求
)

const permMask = PermDeploy | PermApprove | PermRollback | PermAdmin

// Caller 描述一次调用的发起者。
type Caller struct {
	User  string
	Perms Perm
}

// Status 是部署请求的生命周期状态。
type Status int

const (
	StatusPending Status = iota
	StatusQueued
	StatusRunning
	StatusSucceeded
	StatusFailed
	StatusTimedOut
	StatusStale
	StatusExpired
	StatusCanceled
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "Pending"
	case StatusQueued:
		return "Queued"
	case StatusRunning:
		return "Running"
	case StatusSucceeded:
		return "Succeeded"
	case StatusFailed:
		return "Failed"
	case StatusTimedOut:
		return "TimedOut"
	case StatusStale:
		return "Stale"
	case StatusExpired:
		return "Expired"
	default:
		return "Canceled"
	}
}

// EnvConfig 是 New 接受的环境配置。
type EnvConfig struct {
	Name string
	K    int   // 普通部署所需批准数，0..5
	TTL  int64 // 批准有效期秒数，1..1e9
	T    int64 // 运行时限秒数，1..1e9
}

// Snapshot 是 Get 返回的只读状态（上次落地后的状态，不做虚拟推进）。
type Snapshot struct {
	ID       int64
	Env      string
	Ver      int64
	Rollback bool
	Owner    string
	Status   Status
	Start    int64 // 进入 Running 的逻辑时刻，未运行过为 0
	Need     int   // 所需批准数
}

type request struct {
	id       int64
	env      *envState
	ver      int64
	rollback bool
	owner    string
	status   Status
	start    int64
	need     int
	ticket   *approval.Ticket
}

type envState struct {
	lock *envlock.Env
	k    int
	ttl  int64
	t    int64
}

// deadline 是到期堆中的一项：due = start + T。
type deadline struct {
	due int64
	id  int64
}

type deadlineHeap []*deadline

func (h deadlineHeap) Len() int           { return len(h) }
func (h deadlineHeap) Less(i, j int) bool { return h[i].due < h[j].due }
func (h deadlineHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *deadlineHeap) Push(x any)        { *h = append(*h, x.(*deadline)) }
func (h *deadlineHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// 以下三个包装在维护堆的同时同步 id->下标的映射。
func (c *Coordinator) pushDL(d *deadline) {
	heap.Push(&c.dl, d)
	c.syncDL()
}

func (c *Coordinator) popDL() *deadline {
	d := heap.Pop(&c.dl).(*deadline)
	delete(c.dlAt, d.id)
	c.syncDL()
	return d
}
func (c *Coordinator) removeDL(id int64) {
	idx, ok := c.dlAt[id]
	if !ok {
		return
	}
	heap.Remove(&c.dl, idx)
	delete(c.dlAt, id)
	c.syncDL()
}

// syncDL 以堆实际内容重建下标映射，保证与堆位置恒一致。
func (c *Coordinator) syncDL() {
	for i, d := range c.dl {
		c.dlAt[d.id] = i
	}
}

// Coordinator 是部署协调器。所有方法可并发调用。
type Coordinator struct {
	mu   sync.Mutex
	envs map[string]*envState
	reqs map[int64]*request
	seq  int64
	now  int64
	dl   deadlineHeap
	dlAt map[int64]int // 请求 id -> 到期堆下标；堆交换时由 swapDL 同步

	// 最近一次落地的检视数与落地数，用于证明 examined <= landed+1。
	examinedLast int
	landedLast   int
}

// New 按 envs 创建协调器。配置非法或环境名重复时返回 ErrInvalidArgument。
func New(envs []EnvConfig) (*Coordinator, error) {
	c := &Coordinator{
		envs: make(map[string]*envState),
		reqs: make(map[int64]*request),
		dlAt: make(map[int64]int),
	}
	for _, cfg := range envs {
		if cfg.Name == "" || cfg.K < 0 || cfg.K > 5 ||
			cfg.TTL < 1 || cfg.TTL > 1e9 || cfg.T < 1 || cfg.T > 1e9 {
			return nil, ErrInvalidArgument
		}
		if _, dup := c.envs[cfg.Name]; dup {
			return nil, ErrInvalidArgument
		}
		c.envs[cfg.Name] = &envState{
			lock: envlock.New(cfg.Name),
			k:    cfg.K,
			ttl:  cfg.TTL,
			t:    cfg.T,
		}
	}
	c.dl = make(deadlineHeap, 0)
	heap.Init(&c.dl)
	return c, nil
}

func validCaller(cl Caller) bool {
	return cl.User != "" && cl.Perms&^permMask == 0
}

func validNow(now int64) bool { return now >= 0 && now <= 1e12 }

func validVer(ver int64) bool { return ver >= 1 && ver <= 1e9 }

// enter 执行参数检查之后的时钟检查与推进，并落地所有到期者。
// 调用方必须已完成参数合法性检查并持有 c.mu。
func (c *Coordinator) enter(now int64) error {
	if now < c.now {
		return ErrClockBackwards
	}
	c.now = now
	c.land(now)
	return nil
}

// grantAt 在逻辑时刻 g 为空闲环境 es 授锁：反复复核队首并可能级联入锁。
func (c *Coordinator) grantAt(es *envState, g int64) {
	var stale, expired []int64
	es.lock.TryTake(
		func(id int64) int { return c.reqs[id].ticket.ValidAt(g) },
		func(id int64) int { return c.reqs[id].need },
		&stale, &expired,
	)
	for _, id := range stale {
		c.reqs[id].status = StatusStale
	}
	for _, id := range expired {
		c.reqs[id].status = StatusExpired
	}
	if id := es.lock.Holder(); id != 0 {
		r := c.reqs[id]
		r.status = StatusRunning
		r.start = g
		c.pushDL(&deadline{due: g + es.t, id: r.id})
	}
}

// land 推进到期落地，包括以到期时刻授锁引发的级联。
// 每次 while 迭代恰好检视一个堆顶 Running 记录，
// 故 examined <= landed + 1（最后多检视一个未到期堆顶或堆空）。
func (c *Coordinator) land(now int64) {
	examined, landed := 0, 0
	for c.dl.Len() > 0 {
		top := c.dl[0]
		examined++
		if top.due > now {
			break
		}
		c.popDL()
		r := c.reqs[top.id]
		es := r.env
		es.lock.Release()
		r.status = StatusTimedOut
		landed++
		c.grantAt(es, top.due) // 以到期时刻授锁，新 Running 由循环继续判定
	}
	c.examinedLast = examined
	c.landedLast = landed
}

// Request 提交部署请求，返回全局部署编号（被拒不占号）。
func (c *Coordinator) Request(now int64, envName string, ver int64, rollback bool, caller Caller) (int64, error) {
	if !validCaller(caller) || !validNow(now) || !validVer(ver) || envName == "" {
		return 0, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.enter(now); err != nil {
		return 0, err
	}
	required := PermDeploy
	if rollback {
		required = PermRollback
	}
	if caller.Perms&required == 0 {
		return 0, ErrPermission
	}
	es, ok := c.envs[envName]
	if !ok {
		return 0, ErrNotFound
	}
	cur := es.lock.Cur()
	if (!rollback && ver <= cur) || (rollback && ver >= cur) {
		return 0, ErrVersionStale
	}
	if rollback && !es.lock.Succeeded(ver) {
		return 0, ErrUnknownVersion
	}
	c.seq++
	need := es.k
	if rollback {
		need = es.k + 1
	}
	r := &request{
		id:       c.seq,
		env:      es,
		ver:      ver,
		rollback: rollback,
		owner:    caller.User,
		need:     need,
		ticket:   approval.New(es.ttl),
	}
	if need == 0 {
		r.status = StatusQueued
		es.lock.Enqueue(envlock.Queued{ID: r.id, Ver: ver, Rollback: rollback})
	} else {
		r.status = StatusPending
	}
	c.reqs[r.id] = r
	if need == 0 && es.lock.Holder() == 0 {
		c.grantAt(es, now)
	}
	return r.id, nil
}

// Approve 由 caller 在 now 时刻批准编号 id 的请求。
func (c *Coordinator) Approve(now int64, id int64, caller Caller) error {
	if !validCaller(caller) || !validNow(now) || id <= 0 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.enter(now); err != nil {
		return err
	}
	if caller.Perms&PermApprove == 0 {
		return ErrPermission
	}
	r, ok := c.reqs[id]
	if !ok {
		return ErrNotFound
	}
	if r.status != StatusPending {
		return ErrInvalidState
	}
	if caller.User == r.owner {
		return ErrSelfApproval
	}
	if err := r.ticket.Add(caller.User, now); err != nil {
		if err == approval.ErrDuplicateApproval {
			return ErrDuplicateApproval
		}
		return err
	}
	if r.ticket.ValidAt(now) >= r.need {
		r.status = StatusQueued
		r.env.lock.Enqueue(envlock.Queued{ID: r.id, Ver: r.ver, Rollback: r.rollback})
		if r.env.lock.Holder() == 0 {
			c.grantAt(r.env, now)
		}
	}
	return nil
}

// Finish 结束一个 Running 请求；ok 为真成功落地版本，否则失败且 cur 不变。
func (c *Coordinator) Finish(now int64, id int64, ok bool, caller Caller) error {
	if !validCaller(caller) || !validNow(now) || id <= 0 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.enter(now); err != nil {
		return err
	}
	if caller.Perms&PermDeploy == 0 {
		return ErrPermission
	}
	r, exists := c.reqs[id]
	if !exists {
		return ErrNotFound
	}
	if r.status != StatusRunning {
		return ErrInvalidState
	}
	es := r.env
	c.removeDL(r.id)
	es.lock.Release()
	if ok {
		r.status = StatusSucceeded
		es.lock.MarkSucceeded(r.ver)
	} else {
		r.status = StatusFailed
	}
	c.grantAt(es, now)
	return nil
}

// Cancel 取消 Pending、Queued 或 Running 的请求。
func (c *Coordinator) Cancel(now int64, id int64, caller Caller) error {
	if !validCaller(caller) || !validNow(now) || id <= 0 {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.enter(now); err != nil {
		return err
	}
	if caller.Perms&(PermDeploy|PermAdmin) == 0 {
		return ErrPermission
	}
	r, ok := c.reqs[id]
	if !ok {
		return ErrNotFound
	}
	if caller.Perms&PermAdmin == 0 && caller.User != r.owner {
		return ErrNotOwner
	}
	switch r.status {
	case StatusPending:
		r.status = StatusCanceled
	case StatusQueued:
		r.env.lock.Remove(r.id)
		r.status = StatusCanceled
	case StatusRunning:
		es := r.env
		c.removeDL(r.id)
		es.lock.Release()
		r.status = StatusCanceled
		c.grantAt(es, now)
	default:
		return ErrInvalidState
	}
	return nil
}

// Tick 只把时钟推进到 now 并落地所有到期者。
func (c *Coordinator) Tick(now int64) error {
	if !validNow(now) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enter(now)
}

// Get 只读返回编号 id 的当前状态（上次落地后的状态，不做虚拟推进）。
func (c *Coordinator) Get(id int64) (Snapshot, error) {
	if id <= 0 {
		return Snapshot{}, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.reqs[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return Snapshot{
		ID:       r.id,
		Env:      r.env.lock.Name(),
		Ver:      r.ver,
		Rollback: r.rollback,
		Owner:    r.owner,
		Status:   r.status,
		Start:    r.start,
		Need:     r.need,
	}, nil
}
