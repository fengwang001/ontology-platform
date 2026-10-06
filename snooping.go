// Package snooping 实现组播侦听交换机的成员关系与查询器选举控制器。
//
// 控制器由三个子包组成：querier（查询器选举与查询发送计划）、member
// （按端口的组成员关系与离组处理）、forward（组播转发端口集合）。本包
// 负责参数与时钟校验、拒绝次序、惰性落地，并用单互斥锁把并发操作串行化。
package snooping

import (
	"cmp"
	"errors"
	"slices"
	"sync"

	"ontology/forward"
	"ontology/member"
	"ontology/querier"
)

// maxTime 是 now 的上界（含）。
const maxTime = int64(1_000_000_000_000)

var (
	// ErrParam 表示参数非法。
	ErrParam = errors.New("snooping: invalid parameter")
	// ErrClock 表示 now 小于已接受的最大 now（时钟回退）。
	ErrClock = errors.New("snooping: clock regression")
	// ErrLinkLocal 表示对本地链路组执行了 Report 或 Leave。
	ErrLinkLocal = errors.New("snooping: link-local group")
	// ErrNotMember 表示对非成员（组，端口）执行了 Leave。
	ErrNotMember = member.ErrNotMember
	// ErrPortLimit 表示该端口未到期的成员关系数已达 Lp。
	ErrPortLimit = member.ErrPortLimit
	// ErrGroupLimit 表示有成员的组数已达 Gmax。
	ErrGroupLimit = member.ErrGroupLimit
)

// Kind 区分本机发出的查询种类。
type Kind int

const (
	// General 是通用查询，组与端口记 0。
	General Kind = iota
	// Specific 是特定组查询。
	Specific
)

// QueryEvent 是本机发出的一条查询。
type QueryEvent struct {
	Time  int64
	Kind  Kind
	Group uint32
	Port  int
}

// Switch 是组播侦听控制器。全部方法可并发调用，效果等价于某个串行顺序。
type Switch struct {
	mu        sync.Mutex
	ports     int
	ownIP     uint32
	fastLeave []bool

	maxNow    int64
	lastDrain int64

	q *querier.Querier
	m *member.Members
	f *forward.Forwarder

	touched int // 最近一次 Forward 触达的记录数（含查表的一次）
}

// New 构造控制器。P 为端口数（1..256），ownIP 为非零本机地址，
// QI/QRI/LMQI 为查询间隔、响应时限（须小于 QI）、末成员查询间隔
// （1..10^6 秒），Rb 为健壮系数（1..7），fastLeave 为逐端口布尔
// （nil 表示全部关闭），Gmax 为组数上限，Lp 为每端口成员关系上限。
func New(P int, ownIP uint32, QI, QRI int64, Rb int, LMQI int64, fastLeave []bool, floodUnknown bool, Gmax, Lp int) (*Switch, error) {
	if P < 1 || P > 256 || ownIP == 0 ||
		QI < 1 || QRI < 0 || QRI >= QI ||
		LMQI < 1 || LMQI > 1_000_000 ||
		Rb < 1 || Rb > 7 ||
		(fastLeave != nil && len(fastLeave) != P) ||
		Gmax < 1 || Lp < 1 {
		return nil, ErrParam
	}
	fl := make([]bool, P)
	copy(fl, fastLeave)
	gmi := int64(Rb)*QI + QRI
	oqpi := int64(Rb)*QI + QRI/2
	return &Switch{
		ports:     P,
		ownIP:     ownIP,
		fastLeave: fl,
		lastDrain: -1,
		q:         querier.New(ownIP, QI, oqpi),
		m:         member.New(gmi, int64(Rb), LMQI, Lp, Gmax),
		f:         forward.New(P, floodUnknown),
	}, nil
}

// checkClock 校验 now 的取值范围与时钟单调性。
func (s *Switch) checkClock(now int64) error {
	if now < 0 || now > maxTime {
		return ErrParam
	}
	if now < s.maxNow {
		return ErrClock
	}
	return nil
}

// land 在操作被接受后落地不晚于 now 的时间迁移：成员到期与查询器恢复。
func (s *Switch) land(now int64) {
	s.m.Expire(now)
	s.q.Advance(now)
}

// Report 使（组，端口）成为成员（已是成员则刷新），返回应把此报告转给
// 的端口：当前路由器端口去掉 port，升序。
func (s *Switch) Report(port int, group uint32, now int64) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port < 1 || port > s.ports || !forward.IsMulticast(group) {
		return nil, ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if forward.IsLinkLocal(group) {
		return nil, ErrLinkLocal
	}
	if err := s.m.Report(group, port, now); err != nil {
		return nil, err
	}
	s.land(now)
	routers, _ := s.q.RouterPorts(now)
	s.maxNow = now
	return excludePort(routers, port), nil
}

// Leave 处理离组：fastLeave 端口立即移除；本机为查询器时降低到期时刻
// 并安排末成员查询；否则接受但无效果。
func (s *Switch) Leave(port int, group uint32, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port < 1 || port > s.ports || !forward.IsMulticast(group) {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if forward.IsLinkLocal(group) {
		return ErrLinkLocal
	}
	if err := s.m.Leave(group, port, now, s.fastLeave[port-1], s.q.IsQuerierAt(now)); err != nil {
		return err
	}
	s.land(now)
	s.maxNow = now
	return nil
}

// Query 处理从端口收到的他机通用查询：该端口成为路由器端口；srcIP 小于
// ownIP 时本机让位并取消尚未到时刻的特定组查询。
func (s *Switch) Query(port int, srcIP uint32, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port < 1 || port > s.ports || srcIP == 0 || srcIP == s.ownIP {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.land(now)
	if s.q.Query(port, srcIP, now) {
		s.m.CancelAllAfter(now)
	}
	s.maxNow = now
	return nil
}

// Drain 返回自上次 Drain 以来、时刻不大于 now 的全部本机查询，按
// （时刻，通用先于特定，组，端口）升序。
func (s *Switch) Drain(now int64) ([]QueryEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.land(now)
	general := s.q.DrainGeneral(s.lastDrain, now)
	specific := s.m.DrainSpecific(now)
	events := make([]QueryEvent, 0, len(general)+len(specific))
	for _, t := range general {
		events = append(events, QueryEvent{Time: t, Kind: General})
	}
	for _, e := range specific {
		events = append(events, QueryEvent{Time: e.Time, Kind: Specific, Group: e.Group, Port: e.Port})
	}
	slices.SortFunc(events, func(a, b QueryEvent) int {
		if c := cmp.Compare(a.Time, b.Time); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Group, b.Group); c != 0 {
			return c
		}
		return cmp.Compare(a.Port, b.Port)
	})
	s.lastDrain = now
	s.maxNow = now
	return events, nil
}

// Forward 返回组的出端口升序集合（去掉 inPort）。
func (s *Switch) Forward(group uint32, inPort int, now int64) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inPort < 1 || inPort > s.ports || !forward.IsMulticast(group) {
		return nil, ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.land(now)
	members, mt := s.m.Ports(group, now)
	routers, rt := s.q.RouterPorts(now)
	s.touched = 1 + mt + rt
	out := s.f.Ports(group, inPort, members, routers)
	s.maxNow = now
	return out, nil
}

func excludePort(ports []int, port int) []int {
	out := make([]int, 0, len(ports))
	for _, p := range ports {
		if p != port {
			out = append(out, p)
		}
	}
	return out
}
