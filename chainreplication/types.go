package chainreplication

import (
	"fmt"
	"log"
	"os"
	"sync"
)

type RejectReason string

const (
	RejectEmptyChain        RejectReason = "empty_chain"
	RejectEmptyNodeID       RejectReason = "empty_node_id"
	RejectDuplicateNodeID   RejectReason = "duplicate_node_id"
	RejectNotHead           RejectReason = "not_head"
	RejectNotTail           RejectReason = "not_tail"
	RejectUnknownNode       RejectReason = "unknown_node"
	RejectNodeAlreadyFailed RejectReason = "node_already_failed"
	RejectLastSurvivor      RejectReason = "last_survivor"
)

// RejectError identifies a rejected operation via Reason so callers can
// distinguish every rejection case. A rejected operation never mutates state.
type RejectError struct {
	Reason  RejectReason
	Message string
}

func (e *RejectError) Error() string { return string(e.Reason) + ": " + e.Message }

type MessageType int

const (
	MsgWrite MessageType = iota
	MsgAck
)

func msgTypeName(t MessageType) string {
	if t == MsgAck {
		return "ACK"
	}
	return "WRITE"
}

// Message is the only thing that travels over the injected Network.
type Message struct {
	Type  MessageType
	Seq   int
	Value string
	Epoch int
	From  string
	To    string
}

// Network delivers messages asynchronously. Implementations may delay,
// reorder or duplicate messages. DropNode must discard every buffered or
// in-flight message involving a failed node.
type Network interface {
	Send(Message)
	DropNode(id string)
}

type Logger interface {
	Logf(format string, args ...any)
}

type stdLogger struct{ l *log.Logger }

func (s stdLogger) Logf(format string, args ...any) { s.l.Printf(format, args...) }

// DefaultLogger prints inputs, outputs and decisions to standard output.
func DefaultLogger() Logger {
	return stdLogger{l: log.New(os.Stdout, "[chain-rep] ", log.Lmicroseconds)}
}

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}

// WriteHandle is returned by Write. The Outcome channel is resolved exactly
// once per write: true for committed, false for uncommitted.
type WriteHandle struct {
	seq     int
	outcome chan bool
}

func (h *WriteHandle) Seq() int             { return h.seq }
func (h *WriteHandle) Outcome() <-chan bool { return h.outcome }

type node struct {
	id      string
	failed  bool
	applied int              // largest sequence applied locally
	buffer  map[int]*Message // writes with seq > applied, held until gaps fill
	pending map[int]bool     // applied locally but not yet acknowledged upstream
	values  map[int]string   // applied values; needed for reads and retransmission
}

// Coordinator is a chain-replication controller. All exported methods are
// safe for concurrent use; state is mutated under a single mutex so a replay
// of the same operation/delivery sequence yields identical results.
type Coordinator struct {
	mu      sync.Mutex
	chain   []string
	alive   map[string]*node
	net     Network
	logger  Logger
	epoch   int
	nextSeq int
	ch      map[int]chan bool
}

func indexOf(chain []string, id string) int {
	for i, x := range chain {
		if x == id {
			return i
		}
	}
	return -1
}

func downstream(chain []string, selfID, otherID string) bool {
	s, o := indexOf(chain, selfID), indexOf(chain, otherID)
	return s >= 0 && o > s
}

func NewCoordinator(ids []string, net Network, logger Logger) (*Coordinator, error) {
	if len(ids) == 0 {
		return nil, &RejectError{RejectEmptyChain, "cannot construct a chain with no nodes"}
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, &RejectError{RejectEmptyNodeID, "node identifiers must be non-empty"}
		}
		if seen[id] {
			return nil, &RejectError{RejectDuplicateNodeID, "duplicate node identifier: " + id}
		}
		seen[id] = true
	}
	if net == nil {
		return nil, fmt.Errorf("network must not be nil")
	}
	if logger == nil {
		logger = discardLogger{}
	}
	c := &Coordinator{
		chain:   append([]string(nil), ids...),
		alive:   make(map[string]*node, len(ids)),
		net:     net,
		logger:  logger,
		nextSeq: 1,
		ch:      make(map[int]chan bool),
	}
	for _, id := range ids {
		c.alive[id] = &node{
			id:      id,
			buffer:  make(map[int]*Message),
			pending: make(map[int]bool),
			values:  make(map[int]string),
		}
	}
	c.logger.Logf("INPUT NewCoordinator chain=%v -> OUTPUT accepted epoch=%d head=%s tail=%s",
		ids, c.epoch, c.chain[0], c.chain[len(c.chain)-1])
	return c, nil
}
