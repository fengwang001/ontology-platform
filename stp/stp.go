// Package stp implements a spanning-tree-protocol bridge port role
// calculator.
package stp

import (
	"errors"
	"sort"
	"sync"
)

// Time is a logical time (for example a monotonic millisecond clock).
type Time int64

// ConfigMessage is the configuration BPDU vector advertised on a link.
type ConfigMessage struct {
	RootID   int64
	Cost     int64
	SenderID int64
	PortID   int64
}

// PortSpec describes a local port at construction time.
type PortSpec struct {
	ID   int64
	Cost int64
}

// PortRole is the role assigned to a local port.
type PortRole int

const (
	RoleRoot PortRole = iota
	RoleDesignated
	RoleBlocking
)

func (r PortRole) String() string {
	switch r {
	case RoleRoot:
		return "root"
	case RoleDesignated:
		return "designated"
	case RoleBlocking:
		return "blocking"
	default:
		return "unknown"
	}
}

// PortResult is the computed state of one local port.
type PortResult struct {
	PortID     int64
	Role       PortRole
	Forward    bool
	HasMessage bool
	Message    ConfigMessage
}

// RolesResult is the result of a role query for a bridge.
type RolesResult struct {
	BridgeID int64
	RootID   int64
	RootCost int64
	Ports    []PortResult
}

// Sentinel errors reported for rejected operations.
var (
	ErrClockRolledBack = errors.New("stp: clock rolled back")
	ErrUnknownPort     = errors.New("stp: port does not exist")
	ErrInvalidMessage  = errors.New("stp: invalid bridge id or negative cost")
	ErrSelfOrigin      = errors.New("stp: sending bridge is this bridge")
	ErrInvalidBridge   = errors.New("stp: invalid bridge specification")
)

type storedMessage struct {
	msg ConfigMessage
	at  Time
}

type candidate struct {
	root      int64
	cost      int64
	sender    int64
	senderPt  int64
	localPort int64
}

// Bridge is a single spanning-tree bridge with its local ports.
type Bridge struct {
	mu       sync.RWMutex
	id       int64
	maxAge   int64
	ports    map[int64]int64 // port id -> path cost
	portIDs  []int64
	stored   map[int64]storedMessage
	lastTime Time
	hasTime  bool
}

// NewBridge constructs a bridge. Duplicate port ids, non-positive bridge
// ids, port ids, port costs and non-positive max age are rejected.
func NewBridge(id int64, maxAge int64, ports []PortSpec) (*Bridge, error) {
	if id <= 0 || maxAge <= 0 {
		return nil, ErrInvalidBridge
	}
	portCost := make(map[int64]int64, len(ports))
	portIDs := make([]int64, 0, len(ports))
	for _, p := range ports {
		if p.ID <= 0 || p.Cost <= 0 {
			return nil, ErrInvalidBridge
		}
		if _, exists := portCost[p.ID]; exists {
			return nil, ErrInvalidBridge
		}
		portCost[p.ID] = p.Cost
		portIDs = append(portIDs, p.ID)
	}
	sort.Slice(portIDs, func(i, j int) bool { return portIDs[i] < portIDs[j] })
	return &Bridge{
		id:      id,
		maxAge:  maxAge,
		ports:   portCost,
		portIDs: portIDs,
		stored:  make(map[int64]storedMessage, len(ports)),
	}, nil
}

// Receive stores the latest configuration message received on portID.
// Rejections, in order: clock rollback, unknown port, non-positive bridge
// id or negative cost, sending bridge equal to this bridge.
func (b *Bridge) Receive(now Time, portID int64, msg ConfigMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hasTime && now < b.lastTime {
		return ErrClockRolledBack
	}
	if _, ok := b.ports[portID]; !ok {
		return ErrUnknownPort
	}
	if msg.RootID <= 0 || msg.SenderID <= 0 || msg.Cost < 0 {
		return ErrInvalidMessage
	}
	if msg.SenderID == b.id {
		return ErrSelfOrigin
	}
	b.stored[portID] = storedMessage{msg: msg, at: now}
	b.lastTime, b.hasTime = now, true
	return nil
}

// LinkDown clears the message stored on portID.
func (b *Bridge) LinkDown(now Time, portID int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hasTime && now < b.lastTime {
		return ErrClockRolledBack
	}
	if _, ok := b.ports[portID]; !ok {
		return ErrUnknownPort
	}
	delete(b.stored, portID)
	b.lastTime, b.hasTime = now, true
	return nil
}

// Roles computes the current root bridge and every port's role.
func (b *Bridge) Roles(now Time) (RolesResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.hasTime && now < b.lastTime {
		return RolesResult{}, ErrClockRolledBack
	}

	fresh := make(map[int64]ConfigMessage, len(b.portIDs))
	var best *candidate
	for _, pid := range b.portIDs {
		s, ok := b.stored[pid]
		if !ok || int64(now-s.at) >= b.maxAge {
			continue
		}
		fresh[pid] = s.msg
		if s.msg.RootID >= b.id {
			continue
		}
		c := candidate{
			root:      s.msg.RootID,
			cost:      s.msg.Cost + b.ports[pid],
			sender:    s.msg.SenderID,
			senderPt:  s.msg.PortID,
			localPort: pid,
		}
		if best == nil || betterCandidate(c, *best) {
			cp := c
			best = &cp
		}
	}

	res := RolesResult{BridgeID: b.id, Ports: make([]PortResult, 0, len(b.portIDs))}
	if best == nil {
		res.RootID = b.id
		res.RootCost = 0
		for _, pid := range b.portIDs {
			res.Ports = append(res.Ports, PortResult{
				PortID:     pid,
				Role:       RoleDesignated,
				Forward:    true,
				HasMessage: hasFresh(fresh, pid),
				Message:    fresh[pid],
			})
		}
		return res, nil
	}

	res.RootID = best.root
	res.RootCost = best.cost
	for _, pid := range b.portIDs {
		pr := PortResult{PortID: pid}
		if pid == best.localPort {
			pr.Role, pr.Forward = RoleRoot, true
			if msg, ok := fresh[pid]; ok {
				pr.HasMessage, pr.Message = true, msg
			}
			res.Ports = append(res.Ports, pr)
			continue
		}
		advert := ConfigMessage{
			RootID:   res.RootID,
			Cost:     res.RootCost,
			SenderID: b.id,
			PortID:   pid,
		}
		msg, hasMsg := fresh[pid]
		pr.HasMessage, pr.Message = hasMsg, msg
		if !hasMsg || betterVector(advert, msg) {
			pr.Role, pr.Forward = RoleDesignated, true
		} else {
			pr.Role, pr.Forward = RoleBlocking, false
		}
		res.Ports = append(res.Ports, pr)
	}
	return res, nil
}

func hasFresh(fresh map[int64]ConfigMessage, pid int64) bool {
	_, ok := fresh[pid]
	return ok
}

// betterCandidate reports whether a is lexicographically smaller than b on
// (root, cost, sender, sender port, local port).
func betterCandidate(a, b candidate) bool {
	return lexLess(
		a.root, b.root,
		a.cost, b.cost,
		a.sender, b.sender,
		a.senderPt, b.senderPt,
		a.localPort, b.localPort,
	)
}

// betterVector reports whether a is strictly better (lexicographically
// smaller on root, cost, sender, port) than b.
func betterVector(a, b ConfigMessage) bool {
	return lexLess(
		a.RootID, b.RootID,
		a.Cost, b.Cost,
		a.SenderID, b.SenderID,
		a.PortID, b.PortID,
	)
}

func lexLess(pairs ...int64) bool {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] != pairs[i+1] {
			return pairs[i] < pairs[i+1]
		}
	}
	return false
}
