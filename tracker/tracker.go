package tracker

import (
	"container/heap"
	"errors"
	"sync"

	"ontology/seqno"
)

var (
	ErrInvalidArg     = errors.New("invalid argument")
	ErrMemberNotFound = errors.New("member not found")
	ErrMemberExists   = errors.New("member already exists")
	ErrStaleTerm      = errors.New("stale term")
	ErrWrongState     = errors.New("wrong state")
	ErrNotCaughtUp    = errors.New("replica not caught up")
	ErrPlanStale      = errors.New("promotion plan is stale")
)

// Role 为成员身份。
type Role int

const (
	RolePrimary Role = iota
	RoleSync
	RoleCatchup
)

// Op 是一个历史条目：真实写入或晋升补洞的空操作。
type Op struct {
	Seq  int
	Term int
	Body string
	Noop bool
}

// MemberView 是晋升决策所需的成员快照。
type MemberView struct {
	Name      string
	Role      Role
	LCP       int
	Processed []int
}

// View 是组在某一时刻的只读快照。
type View struct {
	Primary string
	Term    int
	GCP     int
	MaxSeq  int
	Ops     []Op
	Members []MemberView
}

type member struct {
	name string
	role Role
	set  *seqno.Set
}

// Group 是一个主副本复制组。
type Group struct {
	mu      sync.RWMutex
	primary string
	term    int
	maxSeq  int
	gcp     int
	members map[string]*member
	order   []string
	ops     map[int]Op
	// ackCount[seq] = 当前同步集合中已处理 seq 的成员数。
	ackCount map[int]int
	// 已确认的真实操作序号集合（与 confirmedList 同步维护）。
	confirmedMap  map[int]bool
	confirmedList []int
	// unconfirmed：已分配但尚未确认的真实操作序号。
	// readyHeap：处理数已达到同步成员数、待确认追加的候选（升序弹出）。
	// queued 防止同一序号被重复压入。
	unconfirmed map[int]bool
	readyHeap   intHeap
	queued      map[int]bool
	lost        []int
}

// New 建组：1..16 个互不相同、名字 1..64 字节的成员，初始全部在同步集合，term=1。
func New(primary string, replicas []string) (*Group, error) {
	names := append([]string{primary}, replicas...)
	if len(names) < 1 || len(names) > 16 {
		return nil, ErrInvalidArg
	}
	if !validName(primary) {
		return nil, ErrInvalidArg
	}
	seen := map[string]bool{primary: true}
	for _, name := range replicas {
		if !validName(name) || seen[name] {
			return nil, ErrInvalidArg
		}
		seen[name] = true
	}
	g := &Group{
		primary:      primary,
		term:         1,
		members:      map[string]*member{},
		ops:          map[int]Op{},
		ackCount:     map[int]int{},
		confirmedMap: map[int]bool{},
		unconfirmed:  map[int]bool{},
		queued:       map[int]bool{},
	}
	for i, name := range names {
		role := RoleSync
		if i == 0 {
			role = RolePrimary
		}
		g.members[name] = &member{name: name, role: role, set: seqno.NewSet()}
		g.order = append(g.order, name)
	}
	return g, nil
}

// Write 由主分配连续序号（自 1 起），主立即处理。
func (g *Group) Write(body string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	seq := g.maxSeq + 1
	g.maxSeq = seq
	g.ops[seq] = Op{Seq: seq, Term: g.term, Body: body}
	g.members[g.primary].set.Observe(seq)
	g.ackCount[seq] = 1
	g.unconfirmed[seq] = true
	g.recompute()
	return seq, nil
}

// Ack 记录某副本处理完 (seq, term)。
func (g *Group) Ack(memberName string, seq, term int) error {
	// 拒绝次序：参数非法 > 成员不存在 > 任期过期 > 状态不符。
	if !validName(memberName) || seq < 1 || term < 1 {
		return ErrInvalidArg
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if seq > g.maxSeq || term > g.term {
		return ErrInvalidArg
	}
	m, ok := g.members[memberName]
	if !ok {
		return ErrMemberNotFound
	}
	if term < g.term {
		return ErrStaleTerm
	}
	if m.role == RolePrimary {
		return ErrWrongState
	}
	if m.set.Observe(seq) && m.role == RoleSync {
		g.ackCount[seq]++
		g.recompute()
	}
	return nil
}

// AddReplica 加入一个空的追赶副本。
func (g *Group) AddReplica(name string) error {
	if !validName(name) {
		return ErrInvalidArg
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.members[name]; ok {
		return ErrMemberExists
	}
	g.members[name] = &member{name: name, role: RoleCatchup, set: seqno.NewSet()}
	g.order = append(g.order, name)
	return nil
}

// MarkInSync 把已追上的追赶副本并入同步集合。
func (g *Group) MarkInSync(name string) error {
	if !validName(name) {
		return ErrInvalidArg
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.members[name]
	if !ok {
		return ErrMemberNotFound
	}
	if m.role != RoleCatchup {
		return ErrWrongState
	}
	if m.set.LCP() < g.gcp {
		return ErrNotCaughtUp
	}
	for confirmed := range g.confirmedMap {
		if !m.set.Contains(confirmed) {
			return ErrNotCaughtUp
		}
	}
	m.role = RoleSync
	for _, seq := range m.set.Sorted() {
		g.ackCount[seq]++
	}
	g.recompute()
	return nil
}

// FailReplica 移除一个非主成员并重算 gcp 与确认。
func (g *Group) FailReplica(name string) error {
	if !validName(name) {
		return ErrInvalidArg
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.members[name]
	if !ok {
		return ErrMemberNotFound
	}
	if m.role == RolePrimary {
		return ErrWrongState
	}
	if m.role == RoleSync {
		for _, seq := range m.set.Sorted() {
			g.ackCount[seq]--
			if g.ackCount[seq] == 0 {
				delete(g.ackCount, seq)
			}
		}
	}
	delete(g.members, name)
	for i, n := range g.order {
		if n == name {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	g.recompute()
	return nil
}

// View 返回当前只读快照。
func (g *Group) View() View {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v := View{
		Primary: g.primary,
		Term:    g.term,
		GCP:     g.gcp,
		MaxSeq:  g.maxSeq,
	}
	for seq := 1; seq <= g.maxSeq; seq++ {
		if op, ok := g.ops[seq]; ok {
			v.Ops = append(v.Ops, op)
		}
	}
	for _, name := range g.order {
		m := g.members[name]
		v.Members = append(v.Members, MemberView{
			Name:      name,
			Role:      m.role,
			LCP:       m.set.LCP(),
			Processed: m.set.Sorted(),
		})
	}
	return v
}

// CommitPromotion 在锁内校验并原子提交晋升计划。
func (g *Group) CommitPromotion(p Plan) error {
	if !validName(p.NewPrimary) {
		return ErrInvalidArg
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.members[p.NewPrimary]
	if !ok {
		return ErrMemberNotFound
	}
	if m.role != RoleSync {
		return ErrWrongState
	}
	oldPrimary := g.primary

	// 所有派生量在锁内按提交时刻状态重新计算：
	// 快照与提交之间可能已插入其他操作，不能直接采用 Plan 中的 M/Fill/G。
	gSnapshot := g.gcp
	newMax := m.set.Max()
	fill := make([]int, 0)
	processed := map[int]bool{}
	for _, seq := range m.set.Sorted() {
		processed[seq] = true
	}
	for seq := 1; seq <= newMax; seq++ {
		if !processed[seq] {
			fill = append(fill, seq)
		}
	}

	// Lost：被空操作顶替的洞 + 新主历史之外（seq>newMax）的已分配序号，升序追加。
	lost := append([]int(nil), fill...)
	for seq := newMax + 1; seq <= g.maxSeq; seq++ {
		lost = append(lost, seq)
	}

	// 任期与全局操作表：补 noop，删除 M 之后的旧版本。
	g.term++
	for _, seq := range fill {
		g.ops[seq] = Op{Seq: seq, Term: g.term, Noop: true}
		delete(g.unconfirmed, seq)
	}
	for seq := newMax + 1; seq <= g.maxSeq; seq++ {
		delete(g.ops, seq)
		delete(g.ackCount, seq)
		delete(g.unconfirmed, seq)
	}
	g.maxSeq = newMax

	// 旧主移出组；追赶副本回滚到 g；其余同步副本回滚并重取 g+1..M。
	delete(g.members, oldPrimary)
	for i, n := range g.order {
		if n == oldPrimary {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	for _, name := range g.order {
		other := g.members[name]
		if other.role == RoleCatchup {
			other.set.DiscardAbove(gSnapshot)
			continue
		}
		other.set.DiscardAbove(gSnapshot)
		for seq := gSnapshot + 1; seq <= newMax; seq++ {
			other.set.Observe(seq)
		}
	}

	m.role = RolePrimary
	g.primary = p.NewPrimary
	g.lost = append(g.lost, lost...)

	// 依据提交后的真实状态重建确认计数并重算（不信任计划中的派生数字）。
	g.rebuildAckCount()
	g.recompute()
	return nil
}

// History 返回成员历史 (seq, term, body|noop)。
func (g *Group) History(memberName string) ([]Op, error) {
	if !validName(memberName) {
		return nil, ErrInvalidArg
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	m, ok := g.members[memberName]
	if !ok {
		return nil, ErrMemberNotFound
	}
	history := make([]Op, 0)
	for _, seq := range m.set.Sorted() {
		if op, ok := g.ops[seq]; ok {
			history = append(history, op)
		}
	}
	return history, nil
}

// Confirmed 返回已确认真实操作序号（确认次序）。
func (g *Group) Confirmed() []int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]int(nil), g.confirmedList...)
}

// Lost 返回晋升中被丢弃的序号（升序、逐次晋升追加）。
func (g *Group) Lost() []int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]int(nil), g.lost...)
}

// GCP 返回全局检查点。
func (g *Group) GCP() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.gcp
}

// Term 返回当前任期。
func (g *Group) Term() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.term
}

// LCP 返回成员本地检查点。
func (g *Group) LCP(memberName string) (int, error) {
	if !validName(memberName) {
		return 0, ErrInvalidArg
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	m, ok := g.members[memberName]
	if !ok {
		return 0, ErrMemberNotFound
	}
	return m.set.LCP(), nil
}

// RoleOf 返回成员身份。
func (g *Group) RoleOf(memberName string) (Role, error) {
	if !validName(memberName) {
		return 0, ErrInvalidArg
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	m, ok := g.members[memberName]
	if !ok {
		return 0, ErrMemberNotFound
	}
	return m.role, nil
}

// Plan 由 promote 包计算、由 tracker 原子提交的晋升计划。
type Plan struct {
	NewPrimary string
	Term       int
	G          int
	M          int
	Fill       []int
	Lost       []int
}

func validName(name string) bool {
	n := len(name)
	return n >= 1 && n <= 64
}

// syncMembers 返回当前同步集合（含主）的稳定顺序快照。
func (g *Group) syncMembers() []*member {
	synced := make([]*member, 0, len(g.order))
	for _, name := range g.order {
		m := g.members[name]
		if m.role == RolePrimary || m.role == RoleSync {
			synced = append(synced, m)
		}
	}
	return synced
}

// rebuildAckCount 依据各同步成员当前的处理集合重建计数。
func (g *Group) rebuildAckCount() {
	g.ackCount = map[int]int{}
	for _, m := range g.syncMembers() {
		for _, seq := range m.set.Sorted() {
			g.ackCount[seq]++
		}
	}
}

// recompute 重算 gcp（只增不减），并把新达成全员处理的真实操作按
// seq 升序追加到 Confirmed（同一步内确认多个时升序）。
func (g *Group) recompute() {
	synced := g.syncMembers()
	if len(synced) > 0 {
		minLCP := synced[0].set.LCP()
		for _, m := range synced[1:] {
			if lcp := m.set.LCP(); lcp < minLCP {
				minLCP = lcp
			}
		}
		if minLCP > g.gcp {
			g.gcp = minLCP
		}
	}
	need := len(synced)
	for seq := range g.unconfirmed {
		if !g.queued[seq] && g.ackCount[seq] >= need {
			heap.Push(&g.readyHeap, seq)
			g.queued[seq] = true
		}
	}
	for g.readyHeap.Len() > 0 {
		seq := heap.Pop(&g.readyHeap).(int)
		delete(g.queued, seq)
		if !g.unconfirmed[seq] || g.ackCount[seq] < need {
			continue
		}
		op, exists := g.ops[seq]
		if !exists || op.Noop {
			delete(g.unconfirmed, seq)
			continue
		}
		delete(g.unconfirmed, seq)
		g.confirmedMap[seq] = true
		g.confirmedList = append(g.confirmedList, seq)
	}
}

// intHeap 是最小序号堆。
type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *intHeap) Push(x any) { *h = append(*h, x.(int)) }

func (h *intHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
