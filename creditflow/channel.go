// Package creditflow implements credit-based point-to-point flow control
// between one sender and one receiver.
package creditflow

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Distinct, mutually distinguishable error classes. Every rejected operation
// returns one (and only one) of these via errors.Is and leaves all state
// untouched.
var (
	// ErrInvalidParam is returned for non-positive parameters (capacity,
	// backlog limit) on construction.
	ErrInvalidParam = errors.New("creditflow: invalid parameter")
	// ErrConsumeOutOfBounds is returned when a consume request references a
	// sequence number that has not been delivered or is not the next one
	// expected (gaps, duplicates, future numbers).
	ErrConsumeOutOfBounds = errors.New("creditflow: consume sequence out of bounds")
	// ErrProbeNotAllowed is returned when a probe is issued while the sender
	// still holds credit or has no backlog.
	ErrProbeNotAllowed = errors.New("creditflow: probe conditions not satisfied")
	// ErrBacklogOverflow is returned when producing a message would exceed the
	// configured sender backlog limit.
	ErrBacklogOverflow = errors.New("creditflow: sender backlog overflow")
)

// Message is a numbered payload delivered from sender to receiver.
// Numbers are assigned by produce order, starting at 1, and are never reused.
type Message struct {
	Seq     int64
	Payload []byte
}

// Channel pairs one sender with one receiver over a receiver buffer of fixed
// capacity. All methods are safe for concurrent use by separate goroutines.
type Channel struct {
	mu           sync.Mutex
	log          *slog.Logger
	capacity     int64
	backlogLimit int64

	credit    int64 // sender-held, spendable credit; always >= 0
	inFlight  int64 // granted credit not yet matched by a delivery
	produced  int64 // total messages ever produced
	consumed  int64 // total messages ever consumed
	nextSeq   int64 // next producer sequence number (starts at 1)
	nextCons  int64 // next sequence number the receiver must consume (starts at 1)
	backlog   []Message
	backHead  int
	delivered []Message // receiver buffer, in sequence order
	delHead   int
}

// New creates a Channel. capacity is the receiver buffer size in messages;
// backlogLimit bounds the sender's undelivered message queue.
func New(capacity, backlogLimit int64, logger *slog.Logger) (*Channel, error) {
	if capacity <= 0 || backlogLimit <= 0 {
		return nil, ErrInvalidParam
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Channel{
		log:          logger,
		capacity:     capacity,
		backlogLimit: backlogLimit,
		nextSeq:      1,
		nextCons:     1,
	}, nil
}

// Produce appends one message to the sender backlog and immediately attempts
// to send while credit is available. Returns the assigned sequence number.
// Rejected (ErrBacklogOverflow) calls change no state.
func (c *Channel) Produce(payload []byte) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if int64(len(c.backlog)-c.backHead) >= c.backlogLimit {
		c.logStep("produce", slog.Int64("backlog_len", int64(len(c.backlog)-c.backHead)),
			"reject", "backlog limit reached", ErrBacklogOverflow)
		return 0, ErrBacklogOverflow
	}

	seq := c.nextSeq
	body := append([]byte(nil), payload...)
	c.nextSeq++
	c.produced++
	c.backlog = append(c.backlog, Message{Seq: seq, Payload: body})

	sent := c.pumpLocked()
	c.logStep("produce", slog.Int64("assigned_seq", seq),
		"accept", "queued then auto-sent while credit lasted", nil,
		slog.Int64("auto_sent", sent))
	return seq, nil
}

// Advertise lets the receiver grant credit based on currently free buffer
// slots: credit = (capacity - buffered - inFlight); when positive that amount
// is granted, otherwise nothing happens. It never consumes messages.
func (c *Channel) Advertise() (granted int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	buffered := int64(len(c.delivered) - c.delHead)
	free := c.capacity - buffered - c.inFlight
	if free <= 0 {
		c.logStep("advertise", slog.Int64("free_slots", free),
			"noop", "no free slots: capacity-buffered-inFlight <= 0", nil)
		return 0
	}
	c.inFlight += free
	c.credit += free
	sent := c.pumpLocked()
	c.logStep("advertise", slog.Int64("free_slots", free),
		"grant", "granted free-slots credit and drained backlog", nil,
		slog.Int64("granted", free), slog.Int64("auto_sent", sent))
	return free
}

// Probe may only run while the sender holds no credit AND has a non-empty
// backlog. Its effect is identical to one Advertise (no guaranteed, no
// minimum, not remembered); otherwise ErrProbeNotAllowed is returned.
func (c *Channel) Probe() (granted int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	backlogLen := int64(len(c.backlog) - c.backHead)
	if c.credit != 0 || backlogLen == 0 {
		c.logStep("probe", slog.Int64("backlog_len", backlogLen),
			"reject", "allowed only with zero credit and non-empty backlog", ErrProbeNotAllowed)
		return 0, ErrProbeNotAllowed
	}

	buffered := int64(len(c.delivered) - c.delHead)
	free := c.capacity - buffered - c.inFlight
	if free <= 0 {
		// Same effect as an advertise: no credit when nothing is free.
		// No guarantee, no minimum, and the probe is not remembered.
		c.logStep("probe", slog.Int64("free_slots", free),
			"noop", "conditions met but no free slots", nil)
		return 0, nil
	}
	c.inFlight += free
	c.credit += free
	sent := c.pumpLocked()
	c.logStep("probe", slog.Int64("free_slots", free),
		"grant", "probe granted free-slots credit and drained backlog", nil,
		slog.Int64("granted", free), slog.Int64("auto_sent", sent))
	return free, nil
}

// Consume removes the message with the given sequence number from the
// receiver buffer. Consumption must follow sequence order with no gaps or
// repeats; it never advertises credit.
func (c *Channel) Consume(seq int64) (*Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if seq != c.nextCons {
		c.logStep("consume", slog.Int64("requested_seq", seq),
			"reject", "must consume the next undelivered sequence exactly once",
			ErrConsumeOutOfBounds)
		return nil, ErrConsumeOutOfBounds
	}
	if c.delHead >= len(c.delivered) {
		c.logStep("consume", slog.Int64("requested_seq", seq),
			"reject", "next sequence has not been delivered yet",
			ErrConsumeOutOfBounds)
		return nil, ErrConsumeOutOfBounds
	}

	msg := c.delivered[c.delHead]
	c.delHead++
	c.nextCons++
	c.consumed++
	c.logStep("consume", slog.Int64("requested_seq", seq),
		"accept", "head of receiver buffer consumed; no advertisement", nil)
	return &msg, nil
}

// Snapshot is an immutable point-in-time view used for invariants/logging.
type Snapshot struct {
	Credit        int64 // sender-held credit (never negative)
	Backlog       int64 // queued-but-undelivered messages
	Buffered      int64 // messages delivered to, not yet consumed by receiver
	InFlight      int64 // credit granted but not yet matched by a delivery
	ProducedTotal int64 // total messages ever produced
	ConsumedTotal int64 // total messages ever consumed
}

// pumpLocked moves messages from backlog into the receiver buffer while
// credit is available. Credit is deducted before each send, so it can never
// go negative and zero credit sends nothing. The caller must hold c.mu.
// Returns the number of messages sent.
func (c *Channel) pumpLocked() int64 {
	var sent int64
	for c.credit > 0 && c.backHead < len(c.backlog) {
		c.credit-- // deduct first ...
		c.inFlight--
		msg := c.backlog[c.backHead] // ... then send
		c.backHead++
		c.delivered = append(c.delivered, msg)
		sent++
	}
	if c.backHead == len(c.backlog) {
		c.backlog = c.backlog[:0]
		c.backHead = 0
	}
	if c.delHead > 0 && c.delHead == len(c.delivered) {
		c.delivered = c.delivered[:0]
		c.delHead = 0
	}
	return sent
}

// snapshotLocked returns counters; caller must hold c.mu (Snapshot takes it).
func (c *Channel) snapshotLocked() Snapshot {
	return Snapshot{
		Credit:        c.credit,
		Backlog:       int64(len(c.backlog) - c.backHead),
		Buffered:      int64(len(c.delivered) - c.delHead),
		InFlight:      c.inFlight,
		ProducedTotal: c.produced,
		ConsumedTotal: c.consumed,
	}
}

// Snapshot returns the current counters.
func (c *Channel) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

// logStep prints every operation's inputs, credit/backlog/buffer state and the
// decision reason, as required for reproducible traces.
func (c *Channel) logStep(op string, input slog.Attr, decision, reason string, cause error, extra ...slog.Attr) {
	s := c.snapshotLocked()
	attrs := []slog.Attr{
		slog.String("op", op),
		input,
		slog.Int64("credit", s.Credit),
		slog.Int64("in_flight", s.InFlight),
		slog.Int64("backlog", s.Backlog),
		slog.Int64("buffered", s.Buffered),
		slog.Int64("produced_total", s.ProducedTotal),
		slog.Int64("consumed_total", s.ConsumedTotal),
		slog.String("decision", decision),
		slog.String("reason", reason),
	}
	if cause != nil {
		attrs = append(attrs, slog.String("error", cause.Error()))
	}
	attrs = append(attrs, extra...)
	c.log.LogAttrs(context.Background(), slog.LevelInfo, "creditflow step", attrs...)
}
