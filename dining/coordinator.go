// Package dining 实现基于脏净叉（Chandy–Misra 风格）的分布式资源冲突协调器。
//
// 冲突图上每条边恰有一把叉与一枚请求令牌。初始时叉在标识较小的一端且为脏，
// 令牌在另一端。进程按 思考 -> 饥饿 -> 进餐 循环转移：
//   - 饥饿进程对每把缺少且持有令牌的叉，把令牌发给对方作为请求；
//   - 收到请求时，若手中该叉为脏且自己未在进餐，就洗净发出；
//     若自己仍饥饿，则立即用这枚令牌再请求；
//   - 叉为净或正在进餐则暂存请求，进餐结束后所有叉变脏并立即满足暂存的请求；
//   - 收到的叉为净，持有全部叉时才能进餐。
//
// 消息经注入的 Network 投递：同一有向信道先进先出，其余任意交错。
// 所有导出方法均可并发调用。
package dining

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// State 进程状态。
type State int

const (
	Thinking State = iota // 思考
	Hungry                // 饥饿
	Eating                // 进餐
)

func (s State) String() string {
	switch s {
	case Thinking:
		return "thinking"
	case Hungry:
		return "hungry"
	case Eating:
		return "eating"
	}
	return "unknown"
}

// Edge 冲突边的规范形式，Lo < Hi。
type Edge struct {
	Lo, Hi int
}

func (e Edge) other(p int) int {
	if p == e.Lo {
		return e.Hi
	}
	return e.Lo
}

// MsgKind 消息类型。
type MsgKind int

const (
	Request MsgKind = iota // 请求令牌
	ForkMsg                // 叉
)

func (k MsgKind) String() string {
	if k == ForkMsg {
		return "fork"
	}
	return "request-token"
}

// Message 网络中投递的消息。
type Message struct {
	ID   uint64
	Kind MsgKind
	Edge Edge
	From int
	To   int
}

func (m Message) String() string {
	return fmt.Sprintf("#%d %s %d->%d edge{%d,%d}", m.ID, m.Kind, m.From, m.To, m.Edge.Lo, m.Edge.Hi)
}

// Network 注入的消息网络。实现必须保证同一有向信道（同一 From->To）先进先出，
// 不同信道之间可任意交错。Send 在 Coordinator 锁外调用，但实现不得回调 Coordinator。
type Network interface {
	Send(m Message)
}

// 各类可区分的拒绝原因。
var (
	ErrUnknownProcess   = errors.New("dining: unknown process")
	ErrSelfLoop         = errors.New("dining: self-loop edge")
	ErrDuplicateEdge    = errors.New("dining: duplicate edge")
	ErrNotThinking      = errors.New("dining: process is not thinking")
	ErrNotHungry        = errors.New("dining: process is not hungry")
	ErrMissingForks     = errors.New("dining: process does not hold all forks")
	ErrHungryToThinking = errors.New("dining: hungry process cannot go back to thinking")
	ErrNotEating        = errors.New("dining: process is not eating")
	ErrMessageNotFound  = errors.New("dining: message not in flight")
)

// edgeState 一条边上的叉与令牌状态。叉与令牌各自恰好一份：被某端持有或在途。
type edgeState struct {
	forkHolder    int  // 叉的持有者（在途时为最近持有者）
	forkDirty     bool // 叉是否为脏（在途叉视为净）
	forkInFlight  bool // 叉是否在途
	tokenHolder   int  // 令牌的持有者（在途时为最近持有者）
	tokenInFlight bool // 令牌是否在途
	deferred      bool // 叉持有侧是否有暂存的请求
}

// Coordinator 分布式资源冲突协调器。所有方法可并发调用。
type Coordinator struct {
	mu       sync.Mutex
	n        int
	states   []State
	adj      [][]Edge
	edges    map[Edge]*edgeState
	inFlight map[uint64]Message
	nextID   uint64
	net      Network
}

// NewCoordinator 构建协调器。端点不存在、自环、重复边（含反向重复）整体拒绝。
func NewCoordinator(n int, edges [][2]int, net Network) (*Coordinator, error) {
	c := &Coordinator{
		n:        n,
		states:   make([]State, n),
		adj:      make([][]Edge, n),
		edges:    make(map[Edge]*edgeState),
		inFlight: make(map[uint64]Message),
		net:      net,
	}
	for _, pair := range edges {
		a, b := pair[0], pair[1]
		if a < 0 || a >= n || b < 0 || b >= n {
			return nil, fmt.Errorf("%w: endpoint of {%d,%d}", ErrUnknownProcess, a, b)
		}
		if a == b {
			return nil, fmt.Errorf("%w: {%d,%d}", ErrSelfLoop, a, b)
		}
		e := Edge{Lo: min(a, b), Hi: max(a, b)}
		if _, dup := c.edges[e]; dup {
			return nil, fmt.Errorf("%w: {%d,%d}", ErrDuplicateEdge, a, b)
		}
		// 初始：叉在标识较小的一端且为脏，令牌在另一端。
		c.edges[e] = &edgeState{forkHolder: e.Lo, forkDirty: true, tokenHolder: e.Hi}
		c.adj[e.Lo] = append(c.adj[e.Lo], e)
		c.adj[e.Hi] = append(c.adj[e.Hi], e)
	}
	return c, nil
}

// Hungry 思考 -> 饥饿：对每把缺少且持有令牌的叉发出请求。
func (c *Coordinator) Hungry(p int) error {
	c.mu.Lock()
	if err := c.checkProcessLocked(p); err != nil {
		c.mu.Unlock()
		return err
	}
	if c.states[p] != Thinking {
		s := c.states[p]
		c.mu.Unlock()
		return fmt.Errorf("%w: process %d is %s", ErrNotThinking, p, s)
	}
	c.states[p] = Hungry
	var out []Message
	for _, e := range c.adj[p] {
		es := c.edges[e]
		if !holdsFork(es, p) && es.tokenHolder == p && !es.tokenInFlight {
			es.tokenInFlight = true
			out = append(out, c.sendLocked(Request, e, p, e.other(p)))
		}
	}
	c.mu.Unlock()
	c.emit(out)
	return nil
}

// Eat 饥饿 -> 进餐：要求持有全部叉。
func (c *Coordinator) Eat(p int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkProcessLocked(p); err != nil {
		return err
	}
	if c.states[p] != Hungry {
		return fmt.Errorf("%w: process %d is %s", ErrNotHungry, p, c.states[p])
	}
	var missing []Edge
	for _, e := range c.adj[p] {
		if !holdsFork(c.edges[e], p) {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: process %d missing %v", ErrMissingForks, p, missing)
	}
	c.states[p] = Eating
	return nil
}

// Finish 进餐 -> 思考：所有叉变脏，并立即满足暂存的请求。
func (c *Coordinator) Finish(p int) error {
	c.mu.Lock()
	if err := c.checkProcessLocked(p); err != nil {
		c.mu.Unlock()
		return err
	}
	switch c.states[p] {
	case Hungry:
		c.mu.Unlock()
		return fmt.Errorf("%w: process %d", ErrHungryToThinking, p)
	case Thinking:
		c.mu.Unlock()
		return fmt.Errorf("%w: process %d", ErrNotEating, p)
	}
	c.states[p] = Thinking
	for _, e := range c.adj[p] {
		es := c.edges[e]
		if holdsFork(es, p) {
			es.forkDirty = true
		}
	}
	var out []Message
	for _, e := range c.adj[p] {
		es := c.edges[e]
		if es.deferred {
			es.deferred = false
			es.forkDirty = false // 洗净后再发出
			es.forkInFlight = true
			out = append(out, c.sendLocked(ForkMsg, e, p, e.other(p)))
		}
	}
	c.mu.Unlock()
	c.emit(out)
	return nil
}

// Deliver 投递一条在途消息。
func (c *Coordinator) Deliver(id uint64) error {
	c.mu.Lock()
	m, ok := c.inFlight[id]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("%w: id %d", ErrMessageNotFound, id)
	}
	delete(c.inFlight, id)
	e := m.Edge
	es := c.edges[e]
	p := m.To
	var out []Message
	if m.Kind == ForkMsg {
		es.forkInFlight = false
		es.forkHolder = p
		es.forkDirty = false // 收到的叉为净
	} else {
		es.tokenInFlight = false
		es.tokenHolder = p
		out = c.onRequestLocked(p, e, es)
	}
	c.mu.Unlock()
	c.emit(out)
	return nil
}

// onRequestLocked 处理进程 p 收到边 e 上的请求令牌。
func (c *Coordinator) onRequestLocked(p int, e Edge, es *edgeState) []Message {
	q := e.other(p)
	if holdsFork(es, p) {
		if es.forkDirty && c.states[p] != Eating {
			// 脏叉且未在进餐：洗净发出。
			es.forkDirty = false
			es.forkInFlight = true
			out := []Message{c.sendLocked(ForkMsg, e, p, q)}
			if c.states[p] == Hungry {
				// 自己仍饥饿：立即用这枚令牌再请求。
				es.tokenInFlight = true
				out = append(out, c.sendLocked(Request, e, p, q))
			}
			return out
		}
		// 净叉或正在进餐：暂存请求。
		es.deferred = true
		return nil
	}
	// 未持有该叉：若饥饿则立即把令牌转发给对方作为请求。
	if c.states[p] == Hungry {
		es.tokenInFlight = true
		return []Message{c.sendLocked(Request, e, p, q)}
	}
	return nil
}

func holdsFork(es *edgeState, p int) bool {
	return es.forkHolder == p && !es.forkInFlight
}

func (c *Coordinator) checkProcessLocked(p int) error {
	if p < 0 || p >= c.n {
		return fmt.Errorf("%w: %d", ErrUnknownProcess, p)
	}
	return nil
}

// sendLocked 登记一条在途消息，返回待注入网络的消息。
func (c *Coordinator) sendLocked(kind MsgKind, e Edge, from, to int) Message {
	c.nextID++
	m := Message{ID: c.nextID, Kind: kind, Edge: e, From: from, To: to}
	c.inFlight[m.ID] = m
	return m
}

// emit 在锁外把消息注入网络。
func (c *Coordinator) emit(msgs []Message) {
	if c.net == nil {
		return
	}
	for _, m := range msgs {
		c.net.Send(m)
	}
}

// ForkInfo 叉的快照信息。
type ForkInfo struct {
	Holder   int
	Dirty    bool
	InFlight bool
	To       int // InFlight 时有效：目的进程
}

// TokenInfo 请求令牌的快照信息。
type TokenInfo struct {
	Holder   int
	InFlight bool
	To       int // InFlight 时有效：目的进程
}

// Snapshot 协调器某一时刻的一致性快照。
type Snapshot struct {
	States   []State
	Forks    map[Edge]ForkInfo
	Tokens   map[Edge]TokenInfo
	Deferred map[Edge]bool
	InFlight []Message // 按 ID 升序
}

// Snapshot 返回当前状态快照。
func (c *Coordinator) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Coordinator) snapshotLocked() Snapshot {
	s := Snapshot{
		States:   append([]State(nil), c.states...),
		Forks:    make(map[Edge]ForkInfo, len(c.edges)),
		Tokens:   make(map[Edge]TokenInfo, len(c.edges)),
		Deferred: make(map[Edge]bool),
	}
	for e, es := range c.edges {
		s.Forks[e] = ForkInfo{Holder: es.forkHolder, Dirty: es.forkDirty, InFlight: es.forkInFlight}
		s.Tokens[e] = TokenInfo{Holder: es.tokenHolder, InFlight: es.tokenInFlight}
		if es.deferred {
			s.Deferred[e] = true
		}
	}
	for _, m := range c.inFlight {
		if m.Kind == ForkMsg {
			f := s.Forks[m.Edge]
			f.To = m.To
			s.Forks[m.Edge] = f
		} else {
			tk := s.Tokens[m.Edge]
			tk.To = m.To
			s.Tokens[m.Edge] = tk
		}
		s.InFlight = append(s.InFlight, m)
	}
	sort.Slice(s.InFlight, func(i, j int) bool { return s.InFlight[i].ID < s.InFlight[j].ID })
	return s
}

// Orientation 由叉的位置与脏净导出的优先关系（有向边 from->to）：
// 叉脏时由持有者指向对方，叉净时由对方指向持有者；在途叉视为已到达接收方且为净。
func (s Snapshot) Orientation() map[Edge][2]int {
	out := make(map[Edge][2]int, len(s.Forks))
	for e, f := range s.Forks {
		holder, dirty := f.Holder, f.Dirty
		if f.InFlight {
			holder, dirty = f.To, false
		}
		if dirty {
			out[e] = [2]int{holder, e.other(holder)}
		} else {
			out[e] = [2]int{e.other(holder), holder}
		}
	}
	return out
}

// Acyclic 判定优先关系是否无环（Kahn 拓扑排序）。
func (s Snapshot) Acyclic() bool {
	indeg := make(map[int]int, len(s.States))
	adj := make(map[int][]int, len(s.States))
	for _, d := range s.Orientation() {
		indeg[d[1]]++
		adj[d[0]] = append(adj[d[0]], d[1])
	}
	var queue []int
	for p := range s.States {
		if indeg[p] == 0 {
			queue = append(queue, p)
		}
	}
	seen := 0
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		seen++
		for _, y := range adj[x] {
			indeg[y]--
			if indeg[y] == 0 {
				queue = append(queue, y)
			}
		}
	}
	return seen == len(s.States)
}

// AdjacentEatingPairs 返回相邻且同时在进餐的进程对。
func (s Snapshot) AdjacentEatingPairs() [][2]int {
	var out [][2]int
	for e := range s.Forks {
		if s.States[e.Lo] == Eating && s.States[e.Hi] == Eating {
			out = append(out, [2]int{e.Lo, e.Hi})
		}
	}
	return out
}

// CheckInvariants 校验核心不变量：
// 每条边的叉与令牌各恰好一份（持有或在途）、相邻进程不同时进餐、
// 由叉的位置与脏净导出的优先关系无环。
func (c *Coordinator) CheckInvariants() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	forkMsgs := make(map[Edge]uint64)
	tokenMsgs := make(map[Edge]uint64)
	for id, m := range c.inFlight {
		if _, ok := c.edges[m.Edge]; !ok {
			return fmt.Errorf("message %d references unknown edge %v", id, m.Edge)
		}
		fromOK := m.From == m.Edge.Lo || m.From == m.Edge.Hi
		toOK := m.To == m.Edge.Lo || m.To == m.Edge.Hi
		if !fromOK || !toOK || m.From == m.To {
			return fmt.Errorf("message %d endpoints inconsistent with edge %v", id, m.Edge)
		}
		if m.Kind == ForkMsg {
			if _, dup := forkMsgs[m.Edge]; dup {
				return fmt.Errorf("edge %v: multiple forks in flight", m.Edge)
			}
			forkMsgs[m.Edge] = id
		} else {
			if _, dup := tokenMsgs[m.Edge]; dup {
				return fmt.Errorf("edge %v: multiple tokens in flight", m.Edge)
			}
			tokenMsgs[m.Edge] = id
		}
	}
	for e, es := range c.edges {
		if _, flying := forkMsgs[e]; flying != es.forkInFlight {
			return fmt.Errorf("edge %v: fork in-flight flag mismatch", e)
		}
		if _, flying := tokenMsgs[e]; flying != es.tokenInFlight {
			return fmt.Errorf("edge %v: token in-flight flag mismatch", e)
		}
		if !es.forkInFlight && es.forkHolder != e.Lo && es.forkHolder != e.Hi {
			return fmt.Errorf("edge %v: fork held by non-endpoint %d", e, es.forkHolder)
		}
		if !es.tokenInFlight && es.tokenHolder != e.Lo && es.tokenHolder != e.Hi {
			return fmt.Errorf("edge %v: token held by non-endpoint %d", e, es.tokenHolder)
		}
		if c.states[e.Lo] == Eating && c.states[e.Hi] == Eating {
			return fmt.Errorf("adjacent processes %d and %d both eating", e.Lo, e.Hi)
		}
	}
	if !c.snapshotLocked().Acyclic() {
		return errors.New("dining: priority relation has a cycle")
	}
	return nil
}
