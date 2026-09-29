package closure

import "sync"

type Clock func() int64

type version struct {
	sequence  uint64
	timestamp int64
	value     string
}

type replicaState struct {
	pending      map[uint64]Message
	applied      uint64
	publications map[PublicationMessage]struct{}
	values       map[string][]version
}

type Group struct {
	mu        sync.Mutex
	primaryID uint64
	replicas  map[uint64]*replicaState
	targetLag int64
	now       Clock
	nextSeq   uint64
	closed    int64
	inflight  map[uint64]int64
}

// NewGroup creates a deterministic replica group using the injected clock.
func NewGroup(primaryID uint64, replicaIDs []uint64, targetLag int64, now Clock) (*Group, error) {
	if targetLag < 0 {
		return nil, ErrInvalidTargetLag
	}
	if now == nil {
		return nil, ErrInvalidClock
	}
	if len(replicaIDs) == 0 {
		return nil, ErrInvalidReplicas
	}

	replicas := make(map[uint64]*replicaState, len(replicaIDs))
	for _, id := range replicaIDs {
		if replicas[id] != nil {
			return nil, ErrInvalidReplicas
		}
		replicas[id] = &replicaState{
			pending:      make(map[uint64]Message),
			publications: make(map[PublicationMessage]struct{}),
			values:       make(map[string][]version),
		}
	}
	if replicas[primaryID] == nil {
		return nil, ErrInvalidReplicas
	}

	return &Group{
		primaryID: primaryID,
		replicas:  replicas,
		targetLag: targetLag,
		now:       now,
		nextSeq:   1,
		closed:    -1,
		inflight:  make(map[uint64]int64),
	}, nil
}

// Primary returns the primary replica ID.
func (g *Group) Primary() uint64 {
	return g.primaryID
}

// Write proposes one KV write and returns its immutable replicated message.
func (g *Group) Write(input WriteInput) (WriteResult, error) {
	if input.DesiredTimestamp < 0 {
		return WriteResult{}, ErrNegativeTimestamp
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	timestamp := input.DesiredTimestamp
	raised := false
	if timestamp <= g.closed {
		timestamp = g.closed + 1
		raised = true
	}

	sequence := g.nextSeq
	g.nextSeq++
	g.inflight[sequence] = timestamp

	message := Message{
		Kind: MessageWrite,
		Write: WriteMessage{
			Sequence:  sequence,
			Timestamp: timestamp,
			Key:       input.Key,
			Value:     input.Value,
		},
	}
	return WriteResult{
		Sequence:  sequence,
		Timestamp: timestamp,
		Raised:    raised,
		Message:   message,
	}, nil
}

// Publish attempts to publish a new closed timestamp promise from the primary.
func (g *Group) Publish() (PublicationResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	candidate := g.now() - g.targetLag
	closed := g.closed
	advanced := false
	if candidate > closed {
		allowed := true
		for _, timestamp := range g.inflight {
			if candidate >= timestamp {
				allowed = false
				break
			}
		}
		if allowed {
			closed = candidate
			g.closed = closed
			advanced = true
		}
	}

	message := Message{
		Kind: MessagePublication,
		Publication: PublicationMessage{
			ClosedTimestamp: closed,
			LogSequence:     g.nextSeq - 1,
		},
	}
	return PublicationResult{
		ClosedTimestamp: closed,
		LogSequence:     message.Publication.LogSequence,
		Advanced:        advanced,
		Message:         message,
	}, nil
}

// Deliver injects one message at a replica, allowing lossless reordering and repetition by callers.
func (g *Group) Deliver(replicaID uint64, message Message) (DeliveryResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	replica := g.replicas[replicaID]
	if replica == nil {
		return DeliveryResult{}, ErrReplicaNotFound
	}
	if err := validateMessage(g.nextSeq-1, message); err != nil {
		return DeliveryResult{}, err
	}

	switch message.Kind {
	case MessageWrite:
		write := message.Write
		if existing, ok := replica.pending[write.Sequence]; ok {
			if existing != message {
				return DeliveryResult{}, ErrInvalidMessage
			}
		} else {
			replica.pending[write.Sequence] = message
		}

		for {
			next, ok := replica.pending[replica.applied+1]
			if !ok || next.Kind != MessageWrite {
				break
			}
			applyWrite(replica, next.Write)
			appliedSequence := replica.applied + 1
			delete(replica.pending, appliedSequence)
			replica.applied = appliedSequence
			if replicaID == g.primaryID {
				delete(g.inflight, appliedSequence)
			}
		}
	case MessagePublication:
		replica.publications[message.Publication] = struct{}{}
	}

	return DeliveryResult{
		ReplicaID:       replicaID,
		AppliedSequence: replica.applied,
	}, nil
}

// Read serves a historical read locally when an applicable closure promise is present.
func (g *Group) Read(replicaID uint64, key string, timestamp int64) (ReadResult, error) {
	if timestamp < 0 {
		return ReadResult{}, ErrNegativeTimestamp
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	replica := g.replicas[replicaID]
	if replica == nil {
		return ReadResult{}, ErrReplicaNotFound
	}

	hasClosingPublication := false
	eligible := false
	matched := PublicationMessage{}
	for publication := range replica.publications {
		if publication.ClosedTimestamp < timestamp {
			continue
		}
		hasClosingPublication = true
		if publication.LogSequence <= replica.applied {
			if !eligible ||
				publication.ClosedTimestamp > matched.ClosedTimestamp ||
				(publication.ClosedTimestamp == matched.ClosedTimestamp && publication.LogSequence > matched.LogSequence) {
				matched = publication
				eligible = true
			}
		}
	}
	if !hasClosingPublication {
		return ReadResult{}, ErrNotClosed
	}
	if !eligible {
		return ReadResult{}, ErrNotCaughtUp
	}

	value, found := readValue(replica.values[key], timestamp)
	return ReadResult{
		ReplicaID:       replicaID,
		Key:             key,
		Timestamp:       timestamp,
		Value:           value,
		Found:           found,
		AppliedSequence: replica.applied,
		ClosedTimestamp: matched.ClosedTimestamp,
		MatchedSequence: matched.LogSequence,
	}, nil
}

func validateMessage(maxSequence uint64, message Message) error {
	switch message.Kind {
	case MessageWrite:
		write := message.Write
		if write.Sequence == 0 || write.Sequence > maxSequence || write.Timestamp < 0 {
			return ErrInvalidMessage
		}
	case MessagePublication:
		publication := message.Publication
		if publication.ClosedTimestamp < -1 || publication.LogSequence > maxSequence {
			return ErrInvalidMessage
		}
	default:
		return ErrInvalidMessage
	}
	return nil
}

func applyWrite(replica *replicaState, write WriteMessage) {
	versions := replica.values[write.Key]
	versions = append(versions, version{
		sequence:  write.Sequence,
		timestamp: write.Timestamp,
		value:     write.Value,
	})
	replica.values[write.Key] = versions
}

func readValue(versions []version, timestamp int64) (string, bool) {
	var best *version
	for i := range versions {
		current := &versions[i]
		if current.timestamp > timestamp {
			continue
		}
		if best == nil || current.timestamp > best.timestamp ||
			(current.timestamp == best.timestamp && current.sequence > best.sequence) {
			best = current
		}
	}
	if best == nil {
		return "", false
	}
	return best.value, true
}
