package closure

type MessageKind int

const (
	// MessageWrite is a replicated KV write entry.
	MessageWrite MessageKind = iota + 1
	// MessagePublication is a primary closure promise.
	MessagePublication
)

// WriteInput is a write proposed with an expected timestamp.
type WriteInput struct {
	Key              string
	Value            string
	DesiredTimestamp int64
}

// WriteMessage is the ordered log entry produced for a write.
type WriteMessage struct {
	Sequence  uint64
	Timestamp int64
	Key       string
	Value     string
}

// PublicationMessage is a promise (closed timestamp, maximum allocated log sequence).
type PublicationMessage struct {
	ClosedTimestamp int64
	LogSequence     uint64
}

// Message is delivered through the injected, unreliable network.
type Message struct {
	Kind        MessageKind
	Write       WriteMessage
	Publication PublicationMessage
}

// WriteResult reports the allocated sequence and final timestamp of a write.
type WriteResult struct {
	Sequence  uint64
	Timestamp int64
	Raised    bool
	Message   Message
}

// PublicationResult reports a publication attempt, even when closure did not advance.
type PublicationResult struct {
	ClosedTimestamp int64
	LogSequence     uint64
	Advanced        bool
	Message         Message
}

// DeliveryResult reports a replica's contiguous applied sequence after delivery.
type DeliveryResult struct {
	ReplicaID       uint64
	AppliedSequence uint64
}

// ReadResult reports a locally served historical value and the publication that authorized it.
type ReadResult struct {
	ReplicaID       uint64
	Key             string
	Timestamp       int64
	Value           string
	Found           bool
	AppliedSequence uint64
	ClosedTimestamp int64
	MatchedSequence uint64
}
