package delegation

import (
	"sort"
	"sync"
	"time"
)

// DelegationInput 是 Declare 的入参：一条委托声明。
type DelegationInput struct {
	Delegator       string
	Delegatee       string
	Subset          []Permission // 委托的权限子集
	AllowRedelegate bool         // 是否允许受托方再委托
	ValidFrom       time.Time    // 有效期起（含）
	ValidTo         time.Time    // 有效期止（不含）
}

// CheckResult 是一次访问判定的结果。
type CheckResult struct {
	Allowed      bool     // 是否允许访问
	Witness      []uint64 // 允许时支持判定的委托链（委托 ID，自目标主体向上游）
	NodesVisited int      // 本次判定遍历的委托记录数
}

// grantInterval 记录一条直接权限的持有区间 [fromSeq, toSeq)，toSeq==0 表示仍持有。
type grantInterval struct {
	fromSeq uint64
	toSeq   uint64
}

// delegationRecord 是委托记录的内部表示，创建后内容不可变（除撤销标记）。
type delegationRecord struct {
	id              uint64
	delegator       string
	delegatee       string
	subset          PermissionSet
	allowRedelegate bool
	validFrom       time.Time
	validTo         time.Time
	declaredSeq     uint64
	revokedSeq      uint64 // 0 表示未撤销
}

// eventMark 记录每次调用的 (seq, time)，用于把历史时刻映射到状态截止序号。
type eventMark struct {
	seq  uint64
	time time.Time
}

// Service 是委托链模块的核心服务。所有公开方法都可并发调用，
// 内部通过单一互斥锁串行化，保证结果等价于某个串行执行顺序。
type Service struct {
	mu     sync.Mutex
	clock  Clock
	rec    Recorder
	seq    uint64
	lastT  time.Time
	events []eventMark

	nextID      uint64
	delegations map[uint64]*delegationRecord
	incoming    map[string][]*delegationRecord // 按受托方索引（邻接表）
	outgoing    map[string][]*delegationRecord // 按委托方索引（用于环检测）
	direct      map[string]map[Permission][]grantInterval
}

// NewService 创建服务。rec 为 nil 时丢弃日志。
func NewService(clock Clock, rec Recorder) *Service {
	if clock == nil {
		clock = RealClock{}
	}
	if rec == nil {
		rec = discardRecorder{}
	}
	return &Service{
		clock:       clock,
		rec:         rec,
		nextID:      1,
		delegations: make(map[uint64]*delegationRecord),
		incoming:    make(map[string][]*delegationRecord),
		outgoing:    make(map[string][]*delegationRecord),
		direct:      make(map[string]map[Permission][]grantInterval),
	}
}

// next 推进全局序号并返回当前 (seq, time)，对时钟回退做钳制。
func (s *Service) next() (uint64, time.Time) {
	s.seq++
	t := s.clock.Now()
	if t.Before(s.lastT) {
		t = s.lastT
	}
	s.lastT = t
	s.events = append(s.events, eventMark{seq: s.seq, time: t})
	return s.seq, t
}

// GrantDirect 授予主体一批直接（原始）权限。重复授予同一权限是幂等的。
func (s *Service) GrantDirect(subject string, perms ...Permission) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, t := s.next()
	if s.direct[subject] == nil {
		s.direct[subject] = make(map[Permission][]grantInterval)
	}
	for _, p := range perms {
		intervals := s.direct[subject][p]
		if n := len(intervals); n > 0 && intervals[n-1].toSeq == 0 {
			continue // 已持有，幂等
		}
		s.direct[subject][p] = append(intervals, grantInterval{fromSeq: seq})
	}
	s.rec.Record(LogEntry{
		Seq: seq, Time: t, Op: OpGrantDirect,
		Subject: subject, Permissions: NewPermissionSet(perms...).Sorted(),
	})
}

// RevokeDirect 收缩主体的直接（原始）权限；不存在的权限忽略。
// 收缩会在后续判定中级联影响所有以该权限为依据的下游委托。
func (s *Service) RevokeDirect(subject string, perms ...Permission) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, t := s.next()
	for _, p := range perms {
		intervals := s.direct[subject][p]
		if n := len(intervals); n > 0 && intervals[n-1].toSeq == 0 {
			intervals[n-1].toSeq = seq
			s.direct[subject][p] = intervals
		}
	}
	s.rec.Record(LogEntry{
		Seq: seq, Time: t, Op: OpRevokeDirect,
		Subject: subject, Permissions: NewPermissionSet(perms...).Sorted(),
	})
}

// Declare 声明一条委托。被拒绝的声明不会改变任何已有状态。
// 错误按固定优先级汇报：ErrScopeExceeded > ErrRedelegationDenied >
// ErrCycle > ErrExpired。
func (s *Service) Declare(in DelegationInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, t := s.next()
	entry := LogEntry{
		Seq: seq, Time: t, Op: OpDeclare,
		Delegator: in.Delegator, Delegatee: in.Delegatee,
		Permissions: NewPermissionSet(in.Subset...).Sorted(),
		AllowReleg:  in.AllowRedelegate,
		ValidFrom:   in.ValidFrom, ValidTo: in.ValidTo,
	}
	id, err := s.declare(seq, t, in)
	entry.DelegationID = id
	entry.Err = err
	s.rec.Record(entry)
	return id, err
}

// declare 是 Declare 的核心逻辑（调用方已持锁）。
// 按固定优先级检查：超范围 > 禁止再委托 > 成环 > 已过期。
// 任何拒绝都不会改变已有状态。
func (s *Service) declare(seq uint64, now time.Time, in DelegationInput) (uint64, error) {
	subset := NewPermissionSet(in.Subset...)
	if in.Delegator == "" || in.Delegatee == "" || len(subset) == 0 {
		return 0, ErrInvalidArgument
	}
	if !in.ValidFrom.Before(in.ValidTo) {
		return 0, ErrInvalidArgument
	}
	ev := newEvaluator(s, seq, now)
	// 1. 声明子集必须 ⊆ 委托方当前实际拥有的权限。
	if !subset.SubsetOf(ev.effective(in.Delegator)) {
		return 0, ErrScopeExceeded
	}
	// 2. 子集还必须 ⊆ 委托方的可再委托权限（上游委托需允许再委托）。
	if !subset.SubsetOf(ev.delegatable(in.Delegator)) {
		return 0, ErrRedelegationDenied
	}
	// 3. 新增边 delegator→delegatee 不得成环（对全部未撤销委托保守判定，
	//    保证任意时刻的生效子图都是 DAG）。
	if s.createsCycle(in.Delegator, in.Delegatee) {
		return 0, ErrCycle
	}
	// 4. 有效期必须尚未结束。
	if !now.Before(in.ValidTo) {
		return 0, ErrExpired
	}
	d := &delegationRecord{
		id:              s.nextID,
		delegator:       in.Delegator,
		delegatee:       in.Delegatee,
		subset:          subset,
		allowRedelegate: in.AllowRedelegate,
		validFrom:       in.ValidFrom,
		validTo:         in.ValidTo,
		declaredSeq:     seq,
	}
	s.nextID++
	s.delegations[d.id] = d
	s.incoming[d.delegatee] = append(s.incoming[d.delegatee], d)
	s.outgoing[d.delegator] = append(s.outgoing[d.delegator], d)
	return d.id, nil
}

// createsCycle 判断新增边 delegator→delegatee 是否会成环：
// 即在未撤销委托图中，delegatee 是否已能到达 delegator（含二者相等）。
func (s *Service) createsCycle(delegator, delegatee string) bool {
	if delegator == delegatee {
		return true
	}
	stack := []string{delegatee}
	seen := map[string]bool{delegatee: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range s.outgoing[cur] {
			if d.revokedSeq != 0 {
				continue
			}
			if d.delegatee == delegator {
				return true
			}
			if !seen[d.delegatee] {
				seen[d.delegatee] = true
				stack = append(stack, d.delegatee)
			}
		}
	}
	return false
}

// Revoke 撤销一条委托。撤销未知 ID 返回 ErrDelegationNotFound；
// 重复撤销是幂等的（返回 nil）。
func (s *Service) Revoke(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, t := s.next()
	d, ok := s.delegations[id]
	var err error
	switch {
	case !ok:
		err = ErrDelegationNotFound
	case d.revokedSeq != 0:
		// 幂等：重复撤销不产生额外效果
	default:
		d.revokedSeq = seq
	}
	s.rec.Record(LogEntry{Seq: seq, Time: t, Op: OpRevoke, DelegationID: id, Err: err})
	return err
}

// Check 在当前时刻判定 subject 是否拥有权限 p。
// 判定总是基于当前状态重新计算，不缓存历史结论。
func (s *Service) Check(subject string, p Permission) CheckResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, t := s.next()
	res := s.check(seq, t, subject, p)
	s.rec.Record(LogEntry{
		Seq: seq, Time: t, Op: OpCheck,
		Subject: subject, Permissions: []Permission{p},
		Allowed: res.Allowed, Witness: res.Witness, NodesVisited: res.NodesVisited,
	})
	return res
}

// CheckAt 返回“若在时刻 asOf 执行判定”应得出的结论：
// 使用 asOf 时刻可见的全部状态与该时刻的有效期重新计算。
// 该结论不随后续的收缩、撤销或过期而改变。
func (s *Service) CheckAt(subject string, p Permission, asOf time.Time) CheckResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, t := s.next()
	cutoff := s.cutoffSeq(asOf)
	res := s.check(cutoff, asOf, subject, p)
	s.rec.Record(LogEntry{
		Seq: seq, Time: t, Op: OpCheckAt, Subject: subject,
		Permissions: []Permission{p}, AsOf: asOf,
		Allowed: res.Allowed, Witness: res.Witness, NodesVisited: res.NodesVisited,
	})
	return res
}

// check 在状态截止序号 cutoff、判定时刻 now 下重新计算一次访问判定。
func (s *Service) check(cutoff uint64, now time.Time, subject string, p Permission) CheckResult {
	ev := newEvaluator(s, cutoff, now)
	res := CheckResult{Allowed: ev.effective(subject).Contains(p), NodesVisited: ev.visited}
	if res.Allowed {
		res.Witness = ev.witness(subject, p, make(map[string]bool))
	}
	return res
}

// cutoffSeq 返回时刻 t 可见的最大事件序号（事件时间单调不减，可二分）。
func (s *Service) cutoffSeq(t time.Time) uint64 {
	i := sort.Search(len(s.events), func(i int) bool {
		return s.events[i].time.After(t)
	})
	if i == 0 {
		return 0
	}
	return s.events[i-1].seq
}
