// Package stp implements a simplified spanning-tree-protocol bridge port
// role calculator.
package stp

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Errors returned by Bridge construction and operations. They are returned in
// the fixed priority order mandated by the protocol specification, so callers
// can compare against them with errors.Is to see the first violated rule.
var (
	ErrNonPositiveBridgeID = errors.New("stp: bridge id must be positive")
	ErrNoPorts             = errors.New("stp: bridge must have at least one port")
	ErrCostLength          = errors.New("stp: port costs length must match port ids length")
	ErrNonPositivePortCost = errors.New("stp: port cost must be positive")
	ErrDuplicatePortID     = errors.New("stp: port ids must be unique")
	ErrNonPositiveMaxAge   = errors.New("stp: max age must be positive")

	ErrClockRollback     = errors.New("stp: clock rolled back")
	ErrPortNotFound      = errors.New("stp: port does not exist")
	ErrInvalidBridgeID   = errors.New("stp: bridge id in message must be positive")
	ErrNegativeCost      = errors.New("stp: message cost must not be negative")
	ErrSenderIsSelf      = errors.New("stp: sending bridge is this bridge")
	ErrInvalidSenderPort = errors.New("stp: sending port id must be positive")
)

// ConfigMessage is the four-component vector carried in a BPDU:
// (root bridge id, cost to root, sending bridge id, sending port id).
type ConfigMessage struct {
	RootID     int
	Cost       int
	SenderID   int
	SenderPort int
}

// PortRole is the role assigned to a bridge port at an instant.
type PortRole int

const (
	RoleUnknown PortRole = iota
	RoleRoot
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

type storedMessage struct {
	msg     ConfigMessage
	arrived time.Time
}

// rootCandidate is a local root-path option derived from one live message.
type rootCandidate struct {
	rootID     int
	cost       int
	senderID   int
	senderPort int
	localPort  int
}

// Bridge is a single switch with a positive id and uniquely numbered ports.
type Bridge struct {
	id     int
	maxAge time.Duration

	mu       sync.Mutex
	ports    []int
	costs    map[int]int
	messages map[int]storedMessage
	lastTime time.Time
}

// NewBridge constructs a bridge. maxAgeA is the maximum message age.
func NewBridge(id int, portIDs []int, portCosts []int, maxAgeA time.Duration) (*Bridge, error) {
	if id <= 0 {
		return nil, ErrNonPositiveBridgeID
	}
	if len(portIDs) == 0 {
		return nil, ErrNoPorts
	}
	if len(portCosts) != len(portIDs) {
		return nil, ErrCostLength
	}
	costs := make(map[int]int, len(portIDs))
	for i, pid := range portIDs {
		if portCosts[i] <= 0 {
			return nil, ErrNonPositivePortCost
		}
		if _, dup := costs[pid]; dup {
			return nil, ErrDuplicatePortID
		}
		costs[pid] = portCosts[i]
	}
	if maxAgeA <= 0 {
		return nil, ErrNonPositiveMaxAge
	}
	ports := append([]int(nil), portIDs...)
	sort.Ints(ports)
	return &Bridge{
		id:       id,
		maxAge:   maxAgeA,
		ports:    ports,
		costs:    costs,
		messages: make(map[int]storedMessage),
	}, nil
}

// ID returns the bridge id.
func (b *Bridge) ID() int { return b.id }

// Receive stores a config message arriving on localPort at instant now.
func (b *Bridge) Receive(now time.Time, localPort int, msg ConfigMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.lastTime.IsZero() && now.Before(b.lastTime) {
		return ErrClockRollback
	}
	if _, ok := b.costs[localPort]; !ok {
		return ErrPortNotFound
	}
	if msg.RootID <= 0 || msg.SenderID <= 0 {
		return ErrInvalidBridgeID
	}
	if msg.Cost < 0 {
		return ErrNegativeCost
	}
	if msg.SenderID == b.id {
		return ErrSenderIsSelf
	}
	if msg.SenderPort <= 0 {
		return ErrInvalidSenderPort
	}
	b.messages[localPort] = storedMessage{msg: msg, arrived: now}
	b.lastTime = now
	return nil
}

// LinkDown clears the message stored on localPort.
func (b *Bridge) LinkDown(localPort int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.costs[localPort]; !ok {
		return ErrPortNotFound
	}
	delete(b.messages, localPort)
	return nil
}

// PortStatus is the computed role of one port.
type PortStatus struct {
	Port int
	Role PortRole
}

// Roles computes the root/designated/blocking role of every port at instant now.
func (b *Bridge) Roles(now time.Time) (rootID, costToRoot int, roles []PortStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.lastTime.IsZero() && now.Before(b.lastTime) {
		return 0, 0, nil
	}

	live := make(map[int]ConfigMessage)
	var best *rootCandidate
	for _, pid := range b.ports {
		stored, ok := b.messages[pid]
		if !ok {
			continue
		}
		if now.Sub(stored.arrived) >= b.maxAge {
			continue
		}
		live[pid] = stored.msg
		c := rootCandidate{
			rootID:     stored.msg.RootID,
			cost:       stored.msg.Cost + b.costs[pid],
			senderID:   stored.msg.SenderID,
			senderPort: stored.msg.SenderPort,
			localPort:  pid,
		}
		if c.rootID >= b.id {
			continue
		}
		if best == nil || candidateLess(c, *best) {
			cp := c
			best = &cp
		}
	}

	rootPort := -1
	if best != nil {
		rootID = best.rootID
		costToRoot = best.cost
		rootPort = best.localPort
	} else {
		rootID = b.id
		costToRoot = 0
	}

	roles = make([]PortStatus, 0, len(b.ports))
	for _, pid := range b.ports {
		role := RoleBlocking
		switch {
		case pid == rootPort:
			role = RoleRoot
		default:
			advert := ConfigMessage{rootID, costToRoot, b.id, pid}
			incoming, hasIncoming := live[pid]
			if rootID == b.id || !hasIncoming || messageLess(advert, incoming) {
				role = RoleDesignated
			}
		}
		roles = append(roles, PortStatus{Port: pid, Role: role})
	}
	return rootID, costToRoot, roles
}

// messageLess reports whether a is strictly better (smaller component-wise)
// than b over (root id, cost, sender bridge id, sender port id).
func messageLess(a, b ConfigMessage) bool {
	switch {
	case a.RootID != b.RootID:
		return a.RootID < b.RootID
	case a.Cost != b.Cost:
		return a.Cost < b.Cost
	case a.SenderID != b.SenderID:
		return a.SenderID < b.SenderID
	default:
		return a.SenderPort < b.SenderPort
	}
}

// candidateLess compares candidates lexicographically over
// (root id, cost, sender bridge id, sender port id, local port id).
func candidateLess(a, b rootCandidate) bool {
	switch {
	case a.rootID != b.rootID:
		return a.rootID < b.rootID
	case a.cost != b.cost:
		return a.cost < b.cost
	case a.senderID != b.senderID:
		return a.senderID < b.senderID
	case a.senderPort != b.senderPort:
		return a.senderPort < b.senderPort
	default:
		return a.localPort < b.localPort
	}
}
