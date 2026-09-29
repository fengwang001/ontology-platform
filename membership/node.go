package membership

import (
	"errors"
	"io"
	"log"
	"math/bits"
	"sort"
	"strconv"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrIncarnationNegative = errors.New("membership: incarnation must not be negative")
	ErrUnknownMember       = errors.New("membership: member not in member list")
	ErrInvalidParameter    = errors.New("membership: lambda and B must be positive")
	ErrNodeExited          = errors.New("membership: operation rejected, node has exited")
	ErrInvalidStatus       = errors.New("membership: message status is invalid")
	ErrClockRewind         = errors.New("membership: clock cannot move backwards")
)

// Config 描述节点的静态配置。
type Config struct {
	SelfID         string
	Members        []string
	Lambda         int
	B              int
	SuspectTimeout int64
	LogOutput      io.Writer
}

// Node 是单个成员节点的合并/传播器。
type Node struct {
	cfg      Config
	mu       sync.Mutex
	view     map[string]*entry
	pending  []*pending
	clock    int64
	exited   bool
	logger   *log.Logger
	members  []string
	carryMax int
}

// New 创建节点；初始所有成员均为存活(0)。
func New(cfg Config) (*Node, error) {
	if cfg.Lambda <= 0 || cfg.B <= 0 {
		return nil, ErrInvalidParameter
	}
	known := make(map[string]struct{}, len(cfg.Members))
	members := make([]string, 0, len(cfg.Members))
	for _, m := range cfg.Members {
		if _, ok := known[m]; ok {
			continue
		}
		known[m] = struct{}{}
		members = append(members, m)
	}
	sort.Strings(members)
	if _, ok := known[cfg.SelfID]; !ok {
		return nil, ErrUnknownMember
	}
	out := cfg.LogOutput
	if out == nil {
		out = io.Discard
	}
	n := &Node{
		cfg:      cfg,
		view:     make(map[string]*entry, len(members)),
		pending:  make([]*pending, 0),
		logger:   log.New(out, "[membership] ", 0),
		members:  members,
		carryMax: carryLimit(cfg.Lambda, len(members)),
	}
	for _, m := range members {
		n.view[m] = &entry{status: Alive, incarnation: 0}
	}
	n.logger.Printf("input=New self=%s members=%v lambda=%d B=%d timeout=%d -> output=node-ready carryLimit=%d",
		cfg.SelfID, members, cfg.Lambda, cfg.B, cfg.SuspectTimeout, n.carryMax)
	return n, nil
}

// Receive 处理一条入站消息。
func (n *Node) Receive(m Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.validate(m); err != nil {
		n.logger.Printf("input=%s -> output=rejected reason=%q", m, err)
		return err
	}
	n.processTimeoutsLocked()
	n.mergeLocked(m)
	return nil
}

// ReceiveBatch 按任意顺序处理一批消息。
func (n *Node) ReceiveBatch(ms []Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.exited {
		n.logger.Printf("input=batch(size=%d) -> output=rejected reason=%q", len(ms), ErrNodeExited)
		return ErrNodeExited
	}
	for _, m := range ms {
		if err := n.validate(m); err != nil {
			n.logger.Printf("input=batch(size=%d) offending=%s -> output=rejected reason=%q (no state changed)",
				len(ms), m, err)
			return err
		}
	}
	n.processTimeoutsLocked()
	for _, m := range ms {
		n.mergeLocked(m)
	}
	return nil
}

// Tick 推进注入时钟并执行超时升级。
func (n *Node) Tick(t int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.exited {
		n.logger.Printf("input=Tick(%d) -> output=rejected reason=%q", t, ErrNodeExited)
		return ErrNodeExited
	}
	if t < n.clock {
		n.logger.Printf("input=Tick(%d) current=%d -> output=rejected reason=%q", t, n.clock, ErrClockRewind)
		return ErrClockRewind
	}
	n.logger.Printf("input=Tick(%d) current=%d", t, n.clock)
	n.clock = t
	n.processTimeoutsLocked()
	return nil
}

// Generate 生成至多 B 条外发消息。
func (n *Node) Generate() ([]Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.exited {
		n.logger.Printf("input=Generate -> output=rejected reason=%q", ErrNodeExited)
		return nil, ErrNodeExited
	}
	n.processTimeoutsLocked()

	eligible := make([]*pending, 0, len(n.pending))
	for _, p := range n.pending {
		if p.sentCount < n.carryMax {
			eligible = append(eligible, p)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].sentCount != eligible[j].sentCount {
			return eligible[i].sentCount < eligible[j].sentCount
		}
		return eligible[i].msg.Member < eligible[j].msg.Member
	})

	limit := n.cfg.B
	if len(eligible) < limit {
		limit = len(eligible)
	}
	out := make([]Message, 0, limit)
	sent := make(map[*pending]bool, limit)
	for _, p := range eligible[:limit] {
		p.sentCount++
		sent[p] = true
		out = append(out, p.msg)
	}

	alive := n.pending[:0]
	for _, p := range n.pending {
		if p.sentCount < n.carryMax {
			alive = append(alive, p)
		}
	}
	n.pending = alive

	n.logger.Printf("input=Generate clock=%d queued=%d carryLimit=%d -> output=%v basis=sent-count-asc,then-member-asc",
		n.clock, len(n.pending), n.carryMax, out)
	return out, nil
}

// View 返回当前视图快照。
func (n *Node) View() []ViewItem {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]ViewItem, 0, len(n.members))
	for _, m := range n.members {
		e := n.view[m]
		out = append(out, ViewItem{Member: m, Status: e.status, Incarnation: e.incarnation})
	}
	return out
}

// Exited 返回节点是否已退出。
func (n *Node) Exited() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.exited
}

// carryLimit 返回 lambda * ceil(log2(n+1))。
func carryLimit(lambda, n int) int {
	if n < 1 {
		return 0
	}
	return lambda * bits.Len(uint(n))
}

// validate 检查消息是否可被接受；不改变任何状态。
func (n *Node) validate(m Message) error {
	if n.exited {
		return ErrNodeExited
	}
	if m.Incarnation < 0 {
		return ErrIncarnationNegative
	}
	if m.Status < Alive || m.Status > Dead {
		return ErrInvalidStatus
	}
	if _, ok := n.view[m.Member]; !ok {
		return ErrUnknownMember
	}
	return nil
}

// mergeLocked 应用单条已校验消息的合并规则。
func (n *Node) mergeLocked(m Message) {
	cur := n.view[m.Member]

	if m.Member == n.cfg.SelfID && m.Status == Dead {
		cur.status = Dead
		cur.incarnation = m.Incarnation
		n.exited = true
		n.removePendingLocked(m.Member)
		n.logger.Printf("input=%s self=true -> output=DEAD+exit basis=dead-to-self-is-terminal clock=%d", m, n.clock)
		return
	}

	// 关于自己的可疑：若化身号不小于自身，自证恢复。
	if m.Member == n.cfg.SelfID && m.Status == Suspect && m.Incarnation >= cur.incarnation {
		newInc := m.Incarnation + 1
		cur.status = Alive
		cur.incarnation = newInc
		alive := Message{Member: n.cfg.SelfID, Status: Alive, Incarnation: newInc}
		n.upsertPendingLocked(alive)
		n.logger.Printf("input=%s self=true current=%s(%d) -> output=%s basis=self-refute-inc=%d+1 clock=%d",
			m, cur.status, m.Incarnation, alive, m.Incarnation, n.clock)
		return
	}

	accepted, why := dominates(m, cur.status, cur.incarnation)
	if !accepted {
		n.logger.Printf("input=%s current=%s(%d) -> output=discard basis=%s clock=%d",
			m, cur.status, cur.incarnation, why, n.clock)
		return
	}

	prevStatus, prevInc := cur.status, cur.incarnation
	cur.status = m.Status
	cur.incarnation = m.Incarnation
	if m.Status == Suspect {
		cur.deadline = n.clock + n.cfg.SuspectTimeout
	} else {
		cur.deadline = 0
	}
	n.upsertPendingLocked(m)
	n.logger.Printf("input=%s current=%s(%d) -> output=accepted view=%s(%d) basis=%s deadline=%d clock=%d",
		m, prevStatus, prevInc, m.Status, m.Incarnation, why, cur.deadline, n.clock)
}

// dominates 判定传入更新是否覆盖当前视图。
// Dead 压过一切且不可逆；Alive(i) 仅在 i>j 时覆盖 Alive(j)/Suspect(j)；
// Suspect(i) 在 i>=j 时覆盖 Alive(j)，在 i>j 时覆盖 Suspect(j)。
func dominates(m Message, curStatus Status, curInc int64) (bool, reason) {
	switch curStatus {
	case Dead:
		return false, "dead-is-terminal"
	case Alive:
		switch m.Status {
		case Dead:
			return true, "dead-overrides-all"
		case Suspect:
			if m.Incarnation >= curInc {
				return true, "suspect(i)>=alive(j)"
			}
			return false, "suspect(i)<alive(j)"
		case Alive:
			if m.Incarnation > curInc {
				return true, "alive(i)>alive(j)"
			}
			return false, "alive(i)<=alive(j)"
		}
	case Suspect:
		switch m.Status {
		case Dead:
			return true, "dead-overrides-all"
		case Alive:
			if m.Incarnation > curInc {
				return true, "alive(i)>suspect(j)-refuted"
			}
			return false, "alive(i)<=suspect(j)"
		case Suspect:
			if m.Incarnation > curInc {
				return true, "suspect(i)>suspect(j)"
			}
			return false, "suspect(i)<=suspect(j)"
		}
	}
	return false, "unknown-status"
}

// processTimeoutsLocked 将已到点（含恰好相等）的可疑升级为确认失效。
func (n *Node) processTimeoutsLocked() {
	for _, member := range n.members {
		e := n.view[member]
		if e.status != Suspect {
			continue
		}
		if n.clock >= e.deadline {
			upgraded := Message{Member: member, Status: Dead, Incarnation: e.incarnation}
			e.status = Dead
			e.deadline = 0
			n.upsertPendingLocked(upgraded)
			selfNote := ""
			if member == n.cfg.SelfID {
				n.exited = true
				n.removePendingLocked(member)
				selfNote = " self=true -> exit"
			}
			n.logger.Printf("input=timeout member=%s inc=%d deadline=%d clock=%d -> output=%s basis=suspect-timeout-exact-inclusive%s",
				member, e.incarnation, e.deadline, n.clock, upgraded, selfNote)
		}
	}
}

// upsertPendingLocked 加入/替换待传播更新；同一成员的新更新重新计次。
func (n *Node) upsertPendingLocked(m Message) {
	for i, p := range n.pending {
		if p.msg.Member == m.Member {
			n.pending[i] = &pending{msg: m, sentCount: 0}
			return
		}
	}
	n.pending = append(n.pending, &pending{msg: m, sentCount: 0})
}

// removePendingLocked 移除某成员的全部待传播更新。
func (n *Node) removePendingLocked(member string) {
	kept := n.pending[:0]
	for _, p := range n.pending {
		if p.msg.Member != member {
			kept = append(kept, p)
		}
	}
	n.pending = kept
}

// String 便于日志记录。
func (m Message) String() string {
	return "msg(" + m.Member + "," + m.Status.String() + "," + strconv.FormatInt(m.Incarnation, 10) + ")"
}
