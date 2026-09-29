package subscription

import (
	"cmp"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
)

var (
	ErrInvalidSubscriptionID = errors.New("invalid subscription id")
	ErrDuplicateSubscription = errors.New("duplicate subscription")
	ErrSubscriptionNotFound  = errors.New("subscription not found")
)

type OrderedKey interface {
	cmp.Ordered
}

// Change is one globally ordered key change delivered to matching subscriptions.
type Change[K OrderedKey] struct {
	Sequence uint64
	Key      K
}

type subscriber[K OrderedKey] struct {
	id         string
	lowerBound K
	startAfter uint64
	delivered  uint64
	updates    chan<- Change[K]
}

type SubscriptionInfo[K OrderedKey] struct {
	ID               string
	LowerBound       K
	StartAfter       uint64
	DeliveredThrough uint64
}

type Snapshot[K OrderedKey] struct {
	CurrentSequence uint64
	Subscriptions   []SubscriptionInfo[K]
}

type Pusher[K OrderedKey] struct {
	mu       sync.RWMutex
	sequence uint64
	active   map[string]*subscriber[K]
	logger   *slog.Logger
}

func NewPusher[K OrderedKey]() *Pusher[K] {
	return NewPusherWithLogger[K](slog.New(slog.NewTextHandler(os.Stdout, nil)))
}

// NewPusherWithLogger creates a pusher that writes decision logs to logger.
func NewPusherWithLogger[K OrderedKey](logger *slog.Logger) *Pusher[K] {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pusher[K]{
		active: make(map[string]*subscriber[K]),
		logger: logger,
	}
}

// Register activates id at the current sequence and sends future matching changes.
func (p *Pusher[K]) Register(id string, lowerBound K, updates chan<- Change[K]) error {
	if strings.TrimSpace(id) == "" {
		p.logger.Error("register rejected", "input_id", id, "lower_bound", lowerBound, "reason", "id must contain non-whitespace characters", "result", ErrInvalidSubscriptionID)
		return ErrInvalidSubscriptionID
	}
	if updates == nil {
		p.logger.Error("register rejected", "input_id", id, "lower_bound", lowerBound, "reason", "updates channel is nil", "result", ErrInvalidSubscriptionID)
		return ErrInvalidSubscriptionID
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.active[id]; exists {
		p.logger.Error("register rejected", "input_id", id, "lower_bound", lowerBound, "reason", "subscription id is already registered", "result", ErrDuplicateSubscription)
		return ErrDuplicateSubscription
	}

	sub := &subscriber[K]{
		id:         id,
		lowerBound: lowerBound,
		startAfter: p.sequence,
		updates:    updates,
	}
	p.active[id] = sub
	p.logger.Info("subscription registered", "input_id", id, "lower_bound", lowerBound, "start_after", sub.startAfter, "result", "registered")
	return nil
}

// Unsubscribe removes an active subscription before returning.
func (p *Pusher[K]) Unsubscribe(id string) error {
	if strings.TrimSpace(id) == "" {
		p.logger.Error("unsubscribe rejected", "input_id", id, "reason", "id must contain non-whitespace characters", "result", ErrSubscriptionNotFound)
		return ErrSubscriptionNotFound
	}

	p.mu.Lock()
	_, exists := p.active[id]
	if !exists {
		p.mu.Unlock()
		p.logger.Error("unsubscribe rejected", "input_id", id, "reason", "subscription is not active", "result", ErrSubscriptionNotFound)
		return ErrSubscriptionNotFound
	}

	delete(p.active, id)
	p.logger.Info("subscription removed", "input_id", id, "result", "unsubscribed")
	p.mu.Unlock()
	return nil
}

// Snapshot returns a concurrency-safe copy of the global and subscription positions.
func (p *Pusher[K]) Snapshot() Snapshot[K] {
	p.mu.RLock()
	defer p.mu.RUnlock()

	subscriptions := make([]SubscriptionInfo[K], 0, len(p.active))
	ids := make([]string, 0, len(p.active))
	for id := range p.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sub := p.active[id]
		subscriptions = append(subscriptions, SubscriptionInfo[K]{
			ID:               sub.id,
			LowerBound:       sub.lowerBound,
			StartAfter:       sub.startAfter,
			DeliveredThrough: sub.delivered,
		})
	}
	return Snapshot[K]{CurrentSequence: p.sequence, Subscriptions: subscriptions}
}

// CurrentSequence returns the sequence of the most recently pushed change.
func (p *Pusher[K]) CurrentSequence() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sequence
}

// DeliveredThrough returns the last matching sequence delivered to an active subscription.
func (p *Pusher[K]) DeliveredThrough(id string) (uint64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	sub, exists := p.active[id]
	if !exists {
		return 0, false
	}
	return sub.delivered, true
}

// Push assigns the next gap-free sequence and delivers the change to every active matching subscription.
func (p *Pusher[K]) Push(key K) Change[K] {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sequence++
	change := Change[K]{Sequence: p.sequence, Key: key}
	targets := make([]*subscriber[K], 0, len(p.active))
	decisions := make([]string, 0, len(p.active))

	ids := make([]string, 0, len(p.active))
	for id := range p.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sub := p.active[id]
		registeredBeforeChange := sub.startAfter < change.Sequence
		matchesBound := change.Key >= sub.lowerBound
		if registeredBeforeChange && matchesBound {
			targets = append(targets, sub)
			decisions = append(decisions, sub.id+":match")
		} else {
			reason := "registered-after-change"
			if registeredBeforeChange {
				reason = "key-below-lower-bound"
			}
			decisions = append(decisions, sub.id+":"+reason)
		}
	}
	for _, sub := range targets {
		sub.updates <- change
		sub.delivered = change.Sequence
	}

	p.logger.Info("change pushed",
		"input_key", key,
		"sequence", change.Sequence,
		"active_match_count", len(targets),
		"decisions", strings.Join(decisions, ","),
		"predicate", "key >= lower_bound",
		"result", "sequence-assigned-and-matches-delivered",
	)
	return change
}
