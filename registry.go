package ontology

import (
	"sort"
	"sync"
)

var (
	ErrClockRewound      = registryError("clock moved backwards")
	ErrEmptyClientID     = registryError("client id is empty")
	ErrNegativeKeepAlive = registryError("keep-alive must not be negative")
	ErrNegativeDelay     = registryError("will delay must not be negative")
	ErrClientNotOnline   = registryError("client is not online")
)

type registryError string

func (err registryError) Error() string {
	return string(err)
}

type Will struct {
	Topic       string
	Payload     []byte
	DelayMillis int64
}

type Publication struct {
	ClientID    string
	Topic       string
	Payload     []byte
	PublishedAt int64
}

type connection struct {
	keepAliveSeconds int64
	lastActivityAt   int64
	will             *Will
}

type pendingWill struct {
	clientID      string
	will          Will
	scheduledAt   int64
	disconnectSeq int64
}

type Registry struct {
	mu            sync.Mutex
	lastNow       int64
	hasObserved   bool
	connections   map[string]*connection
	pendingByID   map[string]*pendingWill
	pending       []*pendingWill
	publications  []Publication
	disconnectSeq int64
}

func NewRegistry() *Registry {
	return &Registry{
		connections: make(map[string]*connection),
		pendingByID: make(map[string]*pendingWill),
	}
}

func (r *Registry) Connect(clientID string, keepAliveSeconds int64, will *Will, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validateInput(now, clientID, keepAliveSeconds, will); err != nil {
		return err
	}

	r.beginOperation(now)

	_, online := r.connections[clientID]
	delete(r.connections, clientID)
	if online {
		r.cancelPending(clientID)
	}

	conn := &connection{
		keepAliveSeconds: keepAliveSeconds,
		lastActivityAt:   now,
	}
	if will != nil {
		conn.will = cloneWill(will)
	}
	r.connections[clientID] = conn
	return nil
}

func (r *Registry) Activity(clientID string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validateInput(now, clientID, 0, nil); err != nil {
		return err
	}

	r.beginOperation(now)

	conn, ok := r.connections[clientID]
	if !ok {
		return ErrClientNotOnline
	}
	conn.lastActivityAt = now
	return nil
}

func (r *Registry) Disconnect(clientID string, normal bool, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validateInput(now, clientID, 0, nil); err != nil {
		return err
	}

	r.beginOperation(now)

	conn, ok := r.connections[clientID]
	if !ok {
		return ErrClientNotOnline
	}

	will := conn.will
	delete(r.connections, clientID)
	if !normal && will != nil {
		r.scheduleWill(clientID, will, now)
	}
	return nil
}

func (r *Registry) Advance(now int64) ([]Publication, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.hasObserved && now < r.lastNow {
		return nil, ErrClockRewound
	}

	before := len(r.publications)
	r.beginOperation(now)
	return clonePublications(r.publications[before:]), nil
}

func (r *Registry) Publications() []Publication {
	r.mu.Lock()
	defer r.mu.Unlock()

	return clonePublications(r.publications)
}

func (r *Registry) validateInput(now int64, clientID string, keepAliveSeconds int64, will *Will) error {
	if r.hasObserved && now < r.lastNow {
		return ErrClockRewound
	}
	if clientID != "" {
		if keepAliveSeconds < 0 {
			return ErrNegativeKeepAlive
		}
		if will != nil && will.DelayMillis < 0 {
			return ErrNegativeDelay
		}
		return nil
	}
	return ErrEmptyClientID
}

func (r *Registry) beginOperation(now int64) {
	r.hasObserved = true
	r.lastNow = now
	r.detectKeepAliveTimeouts(now)
	r.publishDueWills(now)
}

func (r *Registry) detectKeepAliveTimeouts(now int64) {
	clientIDs := make([]string, 0, len(r.connections))
	for clientID := range r.connections {
		clientIDs = append(clientIDs, clientID)
	}
	sort.Strings(clientIDs)

	for _, clientID := range clientIDs {
		conn := r.connections[clientID]
		if conn == nil || conn.keepAliveSeconds == 0 {
			continue
		}
		if now-conn.lastActivityAt > 1500*conn.keepAliveSeconds {
			will := conn.will
			delete(r.connections, clientID)
			if will != nil {
				r.scheduleWill(clientID, will, now)
			}
		}
	}
}

func (r *Registry) scheduleWill(clientID string, will *Will, disconnectedAt int64) {
	r.cancelPending(clientID)
	r.disconnectSeq++
	pending := &pendingWill{
		clientID:      clientID,
		will:          *cloneWill(will),
		scheduledAt:   disconnectedAt + will.DelayMillis,
		disconnectSeq: r.disconnectSeq,
	}
	r.pendingByID[clientID] = pending
	r.pending = append(r.pending, pending)
}

func (r *Registry) publishDueWills(now int64) {
	sort.SliceStable(r.pending, func(i, j int) bool {
		left := r.pending[i]
		right := r.pending[j]
		if left.scheduledAt != right.scheduledAt {
			return left.scheduledAt < right.scheduledAt
		}
		return left.disconnectSeq < right.disconnectSeq
	})

	dueCount := 0
	for dueCount < len(r.pending) && r.pending[dueCount].scheduledAt <= now {
		dueCount++
	}

	due := r.pending[:dueCount]
	r.pending = r.pending[dueCount:]
	for _, pending := range due {
		delete(r.pendingByID, pending.clientID)
		r.publications = append(r.publications, Publication{
			ClientID:    pending.clientID,
			Topic:       pending.will.Topic,
			Payload:     cloneBytes(pending.will.Payload),
			PublishedAt: now,
		})
	}
}

func (r *Registry) cancelPending(clientID string) {
	if _, ok := r.pendingByID[clientID]; !ok {
		return
	}
	delete(r.pendingByID, clientID)
	next := r.pending[:0]
	for _, pending := range r.pending {
		if pending.clientID != clientID {
			next = append(next, pending)
		}
	}
	r.pending = next
}

func cloneWill(will *Will) *Will {
	if will == nil {
		return nil
	}
	clone := *will
	clone.Payload = cloneBytes(will.Payload)
	return &clone
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func clonePublications(publications []Publication) []Publication {
	clone := make([]Publication, len(publications))
	copy(clone, publications)
	for i := range clone {
		clone[i].Payload = cloneBytes(publications[i].Payload)
	}
	return clone
}
