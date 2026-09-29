package ontology

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	Alive   Status = "alive"
	Suspect Status = "suspect"
	Dead    Status = "dead"
)

type Message struct {
	Member      string
	Status      Status
	Incarnation int
}

type ViewEntry struct {
	Status      Status
	Incarnation int
}

type OutgoingMessage struct {
	Updates []Message
}

type Decision struct {
	Accepted       bool
	AcceptedCount  int
	ReplacedCount  int
	DiscardedCount int
	SelfHealed     bool
	NodeExited     bool
	Reason         string
	PiggybackCap   int
	Results        []MessageDecision
}

type MessageDecision struct {
	Message   Message
	Accepted  bool
	Replaced  bool
	Discarded bool
	Reason    string
}

type RejectReason string

const (
	RejectNegativeIncarnation RejectReason = "negative_incarnation"
	RejectUnknownMember       RejectReason = "unknown_member"
	RejectInvalidParameter    RejectReason = "invalid_parameter"
	RejectNodeExited          RejectReason = "node_exited"
)

type RejectError struct {
	Reason RejectReason
	Op     string
	Detail string
}

type Node struct {
	mu        sync.Mutex
	self      string
	members   []string
	known     map[string]bool
	views     map[string]ViewEntry
	lambda    int
	batchSize int
	timeout   time.Duration
	now       time.Duration
	pending   map[string]*queuedUpdate
	suspectAt map[string]time.Duration
	exited    bool
	log       io.Writer
}

type queuedUpdate struct {
	update    Message
	sent      int
	firstSeen time.Duration
}

func (e *RejectError) Error() string {
	if e.Detail == "" {
		return string(e.Reason)
	}
	return string(e.Reason) + ": " + e.Detail
}

func reject(operation string, reason RejectReason, detail string) error {
	return &RejectError{Op: operation, Reason: reason, Detail: detail}
}

func (n *Node) logf(format string, args ...any) {
	fmt.Fprintf(n.log, "membership: "+format+"\n", args...)
}

func NewNode(self string, members []string, lambda, batchSize int, timeout time.Duration, logWriter io.Writer) (*Node, error) {
	if lambda <= 0 {
		return nil, reject("NewNode", RejectInvalidParameter, "lambda must be positive")
	}
	if batchSize <= 0 {
		return nil, reject("NewNode", RejectInvalidParameter, "B must be positive")
	}
	if timeout <= 0 {
		return nil, reject("NewNode", RejectInvalidParameter, "timeout must be positive")
	}
	if len(members) == 0 {
		return nil, reject("NewNode", RejectInvalidParameter, "membership list must not be empty")
	}

	known := make(map[string]bool, len(members))
	roster := make([]string, 0, len(members))
	for _, member := range members {
		if member == "" {
			return nil, reject("NewNode", RejectInvalidParameter, "member identifier must not be empty")
		}
		if known[member] {
			return nil, reject("NewNode", RejectInvalidParameter, "duplicate member "+member)
		}
		known[member] = true
		roster = append(roster, member)
	}
	if !known[self] {
		return nil, reject("NewNode", RejectUnknownMember, self)
	}
	if logWriter == nil {
		logWriter = io.Discard
	}

	views := make(map[string]ViewEntry, len(roster))
	for _, member := range roster {
		views[member] = ViewEntry{Status: Alive, Incarnation: 0}
	}

	n := &Node{
		self:      self,
		members:   roster,
		known:     known,
		views:     views,
		lambda:    lambda,
		batchSize: batchSize,
		timeout:   timeout,
		pending:   make(map[string]*queuedUpdate),
		suspectAt: make(map[string]time.Duration),
		log:       logWriter,
	}
	n.logf("NewNode self=%s members=%s lambda=%d B=%d timeout=%s", self, strings.Join(roster, ","), lambda, batchSize, timeout)
	return n, nil
}

func (n *Node) View() map[string]ViewEntry {
	n.mu.Lock()
	defer n.mu.Unlock()

	snapshot := make(map[string]ViewEntry, len(n.views))
	for member, entry := range n.views {
		snapshot[member] = entry
	}
	return snapshot
}

func (n *Node) Exited() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.exited
}
