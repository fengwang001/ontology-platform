// Package ontology implements a multicast snooping switch controller:
// querier election and query scheduling, per-port group membership with
// leave processing, and multicast forwarding port sets. All state is a
// pure function of the accepted operation sequence; every operation is
// safe for concurrent use and behaves as some serial order.
package ontology

import (
	"errors"
	"sync"

	"ontology/forward"
	"ontology/member"
	"ontology/querier"
)

var (
	ErrParam      = errors.New("igmp: invalid parameter")
	ErrClock      = errors.New("igmp: clock regression")
	ErrLocalLink  = errors.New("igmp: link-local group")
	ErrNonMember  = member.ErrNonMember
	ErrPortLimit  = member.ErrPortLimit
	ErrGroupLimit = member.ErrGroupLimit
)

const maxNow = int64(1e12)

// Kind distinguishes general and group-specific queries.
type Kind int

const (
	General  Kind = iota // general query; Group and Port are 0
	Specific             // group-specific query
)

// Query is one locally originated query returned by Drain.
type Query struct {
	Time  int64
	Kind  Kind
	Group uint32
	Port  int
}

// Switch is the snooping controller. The zero value is not usable; use New.
type Switch struct {
	mu           sync.Mutex
	ports        int
	ownIP        uint32
	qi           int64
	floodUnknown bool
	maxNow       int64
	mem          *member.Table
	qr           *querier.Querier
	touched      int // member records + router entries touched by last Forward
}

// New builds a controller for ports 1..P. gmi = Rb*QI+QRI is the group
// membership interval; oqpi = Rb*QI+QRI/2 is the other-querier present
// interval.
func New(P int, ownIP uint32, QI, QRI, Rb, LMQI int64, fastLeave []bool, floodUnknown bool, Gmax, Lp int) (*Switch, error) {
	if P < 1 || P > 256 || ownIP == 0 ||
		QI < 1 || QI > 1e6 || QRI < 1 || QRI > 1e6 || QRI >= QI ||
		LMQI < 1 || LMQI > 1e6 || Rb < 1 || Rb > 7 ||
		len(fastLeave) != P || Gmax < 1 || Lp < 1 {
		return nil, ErrParam
	}
	gmi := Rb*QI + QRI
	oqpi := Rb*QI + QRI/2
	return &Switch{
		ports:        P,
		ownIP:        ownIP,
		qi:           QI,
		floodUnknown: floodUnknown,
		mem:          member.NewTable(P, int(Rb), LMQI, gmi, fastLeave, Gmax, Lp),
		qr:           querier.New(ownIP, QI, oqpi),
	}, nil
}

// checkClock enforces the rejection order: invalid parameters first, then
// clock regression. Rejected calls leave state and clock untouched.
func (s *Switch) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrParam
	}
	if now < s.maxNow {
		return ErrClock
	}
	return nil
}

// Report records a membership report for (port, group) at now and returns
// the router ports (excluding port, ascending) the report should be
// forwarded to.
func (s *Switch) Report(port int, group uint32, now int64) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port < 1 || port > s.ports || !forward.Valid(group) {
		return nil, ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	if forward.LocalLink(group) {
		return nil, ErrLocalLink
	}
	s.qr.Advance(now)
	if err := s.mem.Report(port, group, now); err != nil {
		return nil, err
	}
	s.maxNow = now
	routers, _ := s.qr.RouterPorts(now)
	return excludePort(routers, port), nil
}

// Leave processes a leave for (port, group) at now.
func (s *Switch) Leave(port int, group uint32, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port < 1 || port > s.ports || !forward.Valid(group) {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if forward.LocalLink(group) {
		return ErrLocalLink
	}
	s.qr.Advance(now)
	spec, err := s.mem.Leave(port, group, now, s.qr.IsQuerier(), s.qr.YieldCuts())
	if err != nil {
		return err
	}
	if len(spec.Times) > 0 {
		s.qr.Schedule(group, port, spec.Times, spec.Epoch, spec.MCut)
	}
	s.maxNow = now
	return nil
}

// Query records a foreign general query received on port with source
// address srcIP at now.
func (s *Switch) Query(port int, srcIP uint32, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port < 1 || port > s.ports || srcIP == 0 || srcIP == s.ownIP {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.qr.Advance(now)
	s.qr.Query(port, srcIP, now)
	s.maxNow = now
	return nil
}

// Drain returns every local query scheduled since the previous Drain with
// time <= now, ordered by (time, general before specific, group, port).
func (s *Switch) Drain(now int64) ([]Query, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.qr.Advance(now)
	events := s.qr.Drain(now, s.mem.QueryValid)
	s.mem.DropTombs(now)
	s.maxNow = now
	if len(events) == 0 {
		return nil, nil
	}
	out := make([]Query, len(events))
	for i, e := range events {
		out[i] = Query{Time: e.Time, Group: e.Group, Port: e.Port}
		if !e.General {
			out[i].Kind = Specific
		}
	}
	return out, nil
}

// Forward returns the sorted egress port set for a frame of group arriving
// on inPort at now.
func (s *Switch) Forward(group uint32, inPort int, now int64) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inPort < 1 || inPort > s.ports || !forward.Valid(group) {
		return nil, ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.maxNow = now
	s.touched = 0
	if forward.LocalLink(group) {
		return forward.Compute(s.ports, inPort, true, nil, nil), nil
	}
	members, examined := s.mem.LiveMembers(group, now)
	routers, rexamined := s.qr.RouterPorts(now)
	s.touched = examined + rexamined + 1
	switch {
	case len(members) > 0:
		return forward.Compute(s.ports, inPort, false, members, routers), nil
	case s.floodUnknown:
		return forward.Compute(s.ports, inPort, true, nil, nil), nil
	default:
		return forward.Compute(s.ports, inPort, false, nil, routers), nil
	}
}

func excludePort(ports []int, port int) []int {
	var out []int
	for _, p := range ports {
		if p != port {
			out = append(out, p)
		}
	}
	return out
}
