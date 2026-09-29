package chainreplication

import (
	"io"
	"log"
	"os"
	"sort"
	"sync"
)

// Option configures a Coordinator.
type Option func(*config)

type config struct {
	logger   *log.Logger
	onResult func(seq int, committed bool, val string)
}

// WithLogger sets the text logger (inputs, outputs, decision rationale).
func WithLogger(l *log.Logger) Option {
	return func(c *config) { c.logger = l }
}

// WithLogWriter redirects the built-in logger to w (e.g. io.Discard).
func WithLogWriter(w io.Writer) Option {
	return func(c *config) {
		c.logger = log.New(w, "[chain-rep] ", log.Lmicroseconds|log.Lmsgprefix)
	}
}

// WithResultCallback fires exactly once per accepted write when its outcome
// (committed/uncommitted) becomes known.
func WithResultCallback(f func(seq int, committed bool, val string)) Option {
	return func(c *config) { c.onResult = f }
}

// Entry is one committed write visible at the tail.
type Entry struct {
	Seq int
	Val string
}

// node is one replica's protocol state.
type node struct {
	id      string
	applied int            // highest contiguous seq applied (prefix watermark)
	held    map[int]string // stored writes: buffered holes plus applied values
	acked   int            // highest commit ack seen from successor
	failed  bool           // tombstone: was a member but has failed
}

type writeRec struct {
	val       string
	decided   bool
	committed bool
}

// Coordinator runs chain replication over an ordered list of node ids.
// All exported calls are serialised by one mutex.
type Coordinator struct {
	mu       sync.Mutex
	net      Network
	log      *log.Logger
	onResult func(seq int, committed bool, val string)

	chain   []string // alive order, head first; never empty while usable
	nodes   map[string]*node
	nextSeq int
	writes  map[int]*writeRec
	lost    map[int]bool // seqs declared uncommitted after a head failure
}

// NewCoordinator builds a coordinator for chain[0]=head .. chain[last]=tail.
func NewCoordinator(chain []string, net Network, opts ...Option) (*Coordinator, error) {
	cfg := &config{
		logger: log.New(os.Stderr, "[chain-rep] ", log.Lmicroseconds|log.Lmsgprefix),
	}
	for _, o := range opts {
		o(cfg)
	}

	if len(chain) == 0 {
		return nil, ErrEmptyChain
	}
	seen := make(map[string]bool, len(chain))
	for _, id := range chain {
		if id == "" {
			return nil, ErrEmptyNodeID
		}
		if seen[id] {
			return nil, ErrDuplicateNodeID
		}
		seen[id] = true
	}
	if net == nil {
		return nil, rejectionError("chainreplication: network must not be nil")
	}

	c := &Coordinator{
		net:      net,
		log:      cfg.logger,
		onResult: cfg.onResult,
		chain:    append([]string(nil), chain...),
		nodes:    make(map[string]*node, len(chain)),
		nextSeq:  1,
		writes:   make(map[int]*writeRec),
		lost:     map[int]bool{},
	}
	for _, id := range c.chain {
		c.nodes[id] = &node{id: id, held: map[int]string{}}
	}
	c.log.Printf("INPUT NewCoordinator chain=%v -> OUTPUT ok head=%s tail=%s",
		c.chain, c.chain[0], c.chain[len(c.chain)-1])
	return c, nil
}

// Write accepts a client write only at the head and assigns consecutive seqs.
func (c *Coordinator) Write(headID, val string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	head := c.chain[0]
	n, ok := c.nodes[headID]
	if !ok {
		c.log.Printf("INPUT Write node=%q -> REJECT unknown-node (state unchanged)", headID)
		return 0, ErrUnknownNode
	}
	if _, alive := c.indexOf(headID); !alive {
		c.log.Printf("INPUT Write node=%q -> REJECT node-failed (state unchanged)", headID)
		return 0, ErrNodeFailed
	}
	if headID != head {
		c.log.Printf("INPUT Write node=%q -> REJECT not-head head=%q (state unchanged)", headID, head)
		return 0, ErrWriteNotAtHead
	}

	seq := c.nextSeq
	c.nextSeq++
	c.writes[seq] = &writeRec{val: val}
	c.log.Printf("INPUT Write head=%s val=%q -> OUTPUT seq=%d", headID, val, seq)

	if len(c.chain) == 1 {
		// Sole node is both head and tail: apply == commit, no network hop.
		c.applyWrite(n, seq, val)
		n.acked = n.applied
		c.resolveHeadAcks(n)
	} else {
		c.applyWrite(n, seq, val)
		c.net.Send(Message{Kind: KindWrite, From: head, To: c.chain[1], Seq: seq, Val: val})
	}
	return seq, nil
}

// Read serves a client read only at the tail and returns committed entries.
func (c *Coordinator) Read(tailID string) ([]Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tail := c.chain[len(c.chain)-1]
	if _, ok := c.nodes[tailID]; !ok {
		c.log.Printf("INPUT Read node=%q -> REJECT unknown-node (state unchanged)", tailID)
		return nil, ErrUnknownNode
	}
	if _, alive := c.indexOf(tailID); !alive {
		c.log.Printf("INPUT Read node=%q -> REJECT node-failed (state unchanged)", tailID)
		return nil, ErrNodeFailed
	}
	if tailID != tail {
		c.log.Printf("INPUT Read node=%q -> REJECT not-tail tail=%q (state unchanged)", tailID, tail)
		return nil, ErrReadNotAtTail
	}

	t := c.nodes[tail]
	entries := make([]Entry, 0, t.applied)
	for seq := 1; seq <= t.applied; seq++ {
		if c.lost[seq] {
			continue
		}
		entries = append(entries, Entry{Seq: seq, Val: t.held[seq]})
	}
	c.log.Printf("INPUT Read tail=%s -> OUTPUT committed seqs 1..%d (%d entries); uncommitted hidden",
		tailID, t.applied, len(entries))
	return entries, nil
}

// Deliver injects one network message. Duplicate, stale or misaddressed
// messages are discarded without state change.
func (c *Coordinator) Deliver(m Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sender, fromAlive := c.indexOf(m.From)
	rcpt, toAlive := c.indexOf(m.To)
	if !fromAlive || !toAlive {
		c.log.Printf("INPUT Deliver %s seq=%d %s->%s -> DISCARD dead/unknown endpoint",
			kindName(m.Kind), m.Seq, m.From, m.To)
		return
	}
	switch m.Kind {
	case KindWrite:
		if rcpt == 0 || sender != rcpt-1 {
			c.log.Printf("INPUT Deliver WRITE seq=%d %s->%s -> DISCARD not-immediate-predecessor",
				m.Seq, m.From, m.To)
			return
		}
		c.handleWrite(c.nodes[m.To], m, rcpt == len(c.chain)-1)
	case KindAck:
		if sender != rcpt+1 {
			c.log.Printf("INPUT Deliver ACK seq=%d %s->%s -> DISCARD not-immediate-successor",
				m.Seq, m.From, m.To)
			return
		}
		c.handleAck(c.nodes[m.To], m)
	default:
		c.log.Printf("INPUT Deliver unknown kind seq=%d -> DISCARD", m.Seq)
	}
}

// Chain returns the current alive chain order (head first).
func (c *Coordinator) Chain() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.chain...)
}

// IsCommitted reports whether seq has been acknowledged back to the head,
// i.e. its final outcome is committed.
func (c *Coordinator) IsCommitted(seq int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.writes[seq]
	return ok && r.decided && r.committed
}

func (c *Coordinator) indexOf(id string) (int, bool) {
	for i, x := range c.chain {
		if x == id {
			return i, true
		}
	}
	return -1, false
}

func kindName(k MessageKind) string {
	switch k {
	case KindWrite:
		return "WRITE"
	case KindAck:
		return "ACK"
	default:
		return "UNKNOWN"
	}
}

func sortedKeys(m map[int]string, above int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		if k > above {
			out = append(out, k)
		}
	}
	sort.Ints(out)
	return out
}
