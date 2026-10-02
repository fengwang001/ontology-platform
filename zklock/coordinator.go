// Package zklock 实现 ZooKeeper 式顺序临时节点读写锁配方的协调模型。
package zklock

// Kind 为请求种类：R 为读，W 为写。
type Kind byte

const (
	// Read 是读请求。
	Read Kind = 'R'
	// Write 是写请求。
	Write Kind = 'W'
)

// Node 描述一把锁上的一个现存顺序子节点。
type Node struct {
	// N 是顺序序号，创建时取该锁的 cs 值，永不复用。
	N int
	// Kind 为创建时的请求种类。
	Kind Kind
	// Session 是拥有该临时节点的会话编号。
	Session int
	// CreateZxid 是创建该节点时领取的 zxid。
	CreateZxid int
}

// Grant 是一次授予事件。
type Grant struct {
	// Lock 为锁名。
	Lock string
	// N 为被授予的子节点序号。
	N int
	// Kind 为被授予子节点的种类。
	Kind Kind
	// Session 为被授予会话。
	Session int
	// TriggerZxid 是触发本次重新评估并授予的删除 zxid。
	TriggerZxid int
}

// Counters 是用于精确复现与开销证明的非导出观测计数器的快照。
type Counters struct {
	// Zxid 为当前全局 zxid 值。
	Zxid int
	// WatchSeq 为当前全局观察登记序号 ws 值。
	WatchSeq int
	// Reevaluations 为删除后重新评估等待者的总次数。
	Reevaluations int
	// EvalTouches 为重新评估过程中访问（比较）子节点的总次数。
	EvalTouches int
}

// Coordinator 是读写锁配方的协调器，可被并发调用。
type Coordinator struct {
	inner *coordinatorImpl
}

// New 创建协调器，C 为每把锁现存子节点上限（1 到 10^6）。
func New(C int) *Coordinator {
	if C < 1 || C > 1_000_000 {
		panic("zklock: C must be in [1, 10^6]")
	}
	return &Coordinator{inner: &coordinatorImpl{
		C:        C,
		sessions: make(map[int]bool),
		locks:    make(map[string]*lockState),
		owns:     make(map[int]map[*child]bool),
	}}
}

// Open 创建一个存活会话，返回从 1 起编号的会话编号。
func (c *Coordinator) Open() int {
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.nextSid++
	ci.sessions[ci.nextSid] = true
	return ci.nextSid
}

// Create 在锁上创建顺序临时子节点并按规则持有或登记观察。
// 返回节点序号；授予不产生事件（创建即持有者）。
func (c *Coordinator) Create(sid int, lock string, kind Kind) (n int, err error) {
	if lock == "" || !isValidKind(kind) {
		return -1, ErrInvalid
	}
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	alive, ok := ci.sessions[sid]
	if !ok {
		return -1, ErrNoSession
	}
	if !alive {
		return -1, ErrExpired
	}
	lk := ci.locks[lock]
	if lk == nil {
		lk = newLock(lock)
		ci.locks[lock] = lk
	}
	if lk.size >= ci.C {
		return -1, ErrFull
	}
	seq := lk.cs
	lk.cs++
	ci.zxid++
	ch := &child{
		lock: lk,
		node: Node{
			N:          seq,
			Kind:       kind,
			Session:    sid,
			CreateZxid: ci.zxid,
		},
		held:     false,
		watchSeq: -1,
	}
	lk.all = avlInsert(lk.all, seq, ch)
	lk.size++
	if kind == Write {
		lk.writers = avlInsert(lk.writers, seq, ch)
	}
	set := ci.owns[sid]
	if set == nil {
		set = make(map[*child]bool)
		ci.owns[sid] = set
	}
	set[ch] = true

	ci.evaluate(lk, ch, func() int {
		ci.ws++
		return ci.ws
	})
	return seq, nil
}

// Release 删除会话在锁上拥有的指定子节点并处理观察通知与授予。
func (c *Coordinator) Release(sid int, lock string, kind Kind, seq int) (events []Grant, err error) {
	if lock == "" || !isValidKind(kind) || seq < 0 {
		return nil, ErrInvalid
	}
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	alive, ok := ci.sessions[sid]
	if !ok {
		return nil, ErrNoSession
	}
	if !alive {
		return nil, ErrExpired
	}
	lk := ci.locks[lock]
	if lk == nil {
		return nil, ErrNoNode
	}
	tn := avlGet(lk.all, seq)
	if tn == nil || tn.val.node.Kind != kind {
		return nil, ErrNoNode
	}
	ch := tn.val
	if ch.node.Session != sid {
		return nil, ErrNotOwner
	}

	ci.zxid++
	delZxid := ci.zxid
	lk.removeWatch(ch)
	ci.deleteChild(lk, ch)
	return ci.notifyDeletion(lk, seq, delZxid, []Grant{}), nil
}

// Expire 令会话过期：先撤销其全部观察，再按创建 zxid 升序删除其节点。
func (c *Coordinator) Expire(sid int) (events []Grant, err error) {
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	alive, ok := ci.sessions[sid]
	if !ok {
		return nil, ErrNoSession
	}
	if !alive {
		return nil, ErrExpired
	}
	ci.sessions[sid] = false

	// 先撤销该会话全部子节点上登记的观察，使其等待者之后不再被评估。
	children := make([]*child, 0, len(ci.owns[sid]))
	for ch := range ci.owns[sid] {
		children = append(children, ch)
		ch.lock.removeWatch(ch)
	}
	sortChildrenByCreateZxid(children)

	events = []Grant{}
	for _, ch := range children {
		ci.zxid++
		delZxid := ci.zxid
		lk := ch.lock
		seq := ch.node.N
		ci.deleteChild(lk, ch)
		events = ci.notifyDeletion(lk, seq, delZxid, events)
	}
	delete(ci.owns, sid)
	return events, nil
}

// Holders 返回锁上当前持有者，按序号升序。
func (c *Coordinator) Holders(lock string) []Node {
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	lk := ci.locks[lock]
	if lk == nil {
		return []Node{}
	}
	var out []Node
	for _, ch := range avlInorder(lk.all, nil) {
		if ch.held {
			out = append(out, ch.node)
		}
	}
	if out == nil {
		return []Node{}
	}
	return out
}

// Children 返回锁上现存子节点，按序号升序。
func (c *Coordinator) Children(lock string) []Node {
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	lk := ci.locks[lock]
	if lk == nil {
		return []Node{}
	}
	chs := avlInorder(lk.all, make([]*child, 0, lk.size))
	out := make([]Node, len(chs))
	for i, ch := range chs {
		out[i] = ch.node
	}
	return out
}

// Counters 返回观测计数器快照。
func (c *Coordinator) Counters() Counters {
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return Counters{
		Zxid:          ci.zxid,
		WatchSeq:      ci.ws,
		Reevaluations: ci.reevaluations,
		EvalTouches:   ci.evalTouches,
	}
}

// ResetCounters 重置删除/评估相关的非导出计数器（zxid、ws 不变）。
func (c *Coordinator) ResetCounters() {
	ci := c.inner
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.reevaluations = 0
	ci.evalTouches = 0
}

func sortChildrenByCreateZxid(chs []*child) {
	// 插入排序之外的常见实现；用标准库排序。
	for i := 1; i < len(chs); i++ {
		for j := i; j > 0 && chs[j-1].node.CreateZxid > chs[j].node.CreateZxid; j-- {
			chs[j-1], chs[j] = chs[j], chs[j-1]
		}
	}
}

// 哨兵错误。
var (
	errInvalid   = invalidError{}
	errNoSession = noSessionError{}
	errExpired   = expiredError{}
	errFull      = fullError{}
	errNoNode    = noNodeError{}
	errNotOwner  = notOwnerError{}
)

type invalidError struct{}

func (invalidError) Error() string { return "zklock: invalid argument" }

type noSessionError struct{}

func (noSessionError) Error() string { return "zklock: session does not exist" }

type expiredError struct{}

func (expiredError) Error() string { return "zklock: session expired" }

type fullError struct{}

func (fullError) Error() string { return "zklock: lock full" }

type noNodeError struct{}

func (noNodeError) Error() string { return "zklock: node does not exist" }

type notOwnerError struct{}

func (notOwnerError) Error() string { return "zklock: not the owner of the node" }

// ErrInvalid 等为可 errors.Is 判定的导出错误。
var (
	ErrInvalid   = errInvalid
	ErrNoSession = errNoSession
	ErrExpired   = errExpired
	ErrFull      = errFull
	ErrNoNode    = errNoNode
	ErrNotOwner  = errNotOwner
)
