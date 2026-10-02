package ontology

import (
	"errors"
	"sync"
)

var ErrInvalidArgument = errors.New("ontology: invalid argument")
var ErrStreamTimeRollback = errors.New("ontology: stream time rollback")
var ErrBufferFull = errors.New("ontology: buffer full")

// FullPolicy defines what happens when an accepted update would exceed capacity.
type FullPolicy int

const (
	// EmitEarly removes the smallest open entries before buffering the update.
	EmitEarly FullPolicy = iota
	// Shutdown rejects the update without emitting or changing any state.
	Shutdown
)

// EmitKind distinguishes deadline-driven results from capacity-driven early results.
type EmitKind int

const (
	// Final means an entry was emitted because its closing time was reached.
	Final EmitKind = iota
	// Early means an open entry was removed to make room for a new update.
	Early
)

// Emitted is one buffered window result made visible by Update or Tick.
type Emitted struct {
	Key    []byte
	WS     int64
	End    int64
	Value  int64
	LastTS int64
	Kind   EmitKind
}

// UpdateResult reports whether an update was accepted, discarded as late, or emitted.
type UpdateResult struct {
	Status  string
	Emitted []Emitted
}

const (
	StatusAccepted  = "Accepted"
	StatusDiscarded = "Discarded"
)

// BufferedEntry is a snapshot of one entry still retained by the buffer.
type BufferedEntry struct {
	Key    []byte
	WS     int64
	End    int64
	Value  int64
	LastTS int64
}

// Buffer suppresses per-window results until their closing time reaches stream time.
type Buffer struct {
	mu         sync.Mutex
	width      int64
	grace      int64
	cap        int
	policy     FullPolicy
	streamTime int64
	late       int64
	peeks      int64
	entries    map[entryKey]*bufferEntry
	root       *treeNode
}

type entryKey struct {
	key string
	ws  int64
}

type bufferEntry struct {
	key    []byte
	ws     int64
	end    int64
	value  int64
	lastTS int64
}

// NewBuffer constructs a buffer with width S, grace G, capacity E, and overflow policy.
func NewBuffer(width, grace int64, capacity int, policy FullPolicy) (*Buffer, error) {
	if width < 1 || width > 1_000_000_000 || grace < 0 || grace > 1_000_000_000 ||
		capacity < 1 || capacity > 1_000_000 || (policy != EmitEarly && policy != Shutdown) {
		return nil, ErrInvalidArgument
	}
	return &Buffer{
		width:      width,
		grace:      grace,
		cap:        capacity,
		policy:     policy,
		streamTime: -1,
		entries:    make(map[entryKey]*bufferEntry),
	}, nil
}

// Update atomically applies one window update and returns all emissions it causes.
func (b *Buffer) Update(key []byte, ts, value int64) (UpdateResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(key) == 0 || ts < 0 || ts > 1_000_000_000_000_000 ||
		value < -1_000_000_000_000 || value > 1_000_000_000_000 {
		return UpdateResult{}, ErrInvalidArgument
	}

	keyCopy := append([]byte(nil), key...)
	keyString := string(keyCopy)
	ws := ts / b.width * b.width
	end := ws + b.width
	id := entryKey{key: keyString, ws: ws}

	if end+b.grace <= b.streamTime {
		b.late++
		return UpdateResult{Status: StatusDiscarded}, nil
	}

	newStreamTime := b.streamTime
	if ts > newStreamTime {
		newStreamTime = ts
	}

	maxOpenEnd := newStreamTime - b.grace
	closedTree, openTree := splitByEnd(b.root, maxOpenEnd)
	b.peeks++

	rest := len(b.entries) - nodeSize(closedTree)
	_, existing := b.entries[id]
	currentOpen := existing && end+b.grace > newStreamTime
	needNew := 1
	if currentOpen {
		needNew = 0
	}

	var earlyTree *treeNode
	if rest+needNew > b.cap {
		earlyCount := rest + needNew - b.cap
		if b.policy == Shutdown {
			b.root = mergeNodes(closedTree, openTree)
			return UpdateResult{}, ErrBufferFull
		}

		b.peeks += 2
		if !currentOpen {
			earlyTree, openTree = splitByRank(openTree, earlyCount)
		} else {
			rank := rankOfEntry(openTree, end, keyString)
			if rank <= earlyCount {
				var first *treeNode
				first, openTree = splitByRank(openTree, earlyCount+1)
				earlyTree, first = splitByRank(first, earlyCount)
				openTree = mergeNodes(first, openTree)
			} else {
				earlyTree, openTree = splitByRank(openTree, earlyCount)
			}
		}
	}

	b.root = openTree
	emitted := make([]Emitted, 0, nodeSize(closedTree)+nodeSize(earlyTree))
	emitted = appendEntriesInOrder(closedTree, emitted, Final)
	emitted = appendEntriesInOrder(earlyTree, emitted, Early)

	removedKeys := make(map[entryKey]struct{}, nodeSize(closedTree)+nodeSize(earlyTree))
	removedKeys = appendKeysInOrder(closedTree, removedKeys)
	removedKeys = appendKeysInOrder(earlyTree, removedKeys)
	for removedKey := range removedKeys {
		delete(b.entries, removedKey)
	}

	if entry, ok := b.entries[id]; ok {
		entry.value = value
		entry.lastTS = ts
	} else {
		entry := &bufferEntry{
			key:    keyCopy,
			ws:     ws,
			end:    end,
			value:  value,
			lastTS: ts,
		}
		b.entries[id] = entry
		b.root = insertNode(b.root, newTreeNode(entry))
	}

	b.streamTime = newStreamTime
	return UpdateResult{Status: StatusAccepted, Emitted: emitted}, nil
}

// Tick advances stream time and emits all entries closed by t.
func (b *Buffer) Tick(t int64) ([]Emitted, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if t < 0 || t > 1_000_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if t < b.streamTime {
		return nil, ErrStreamTimeRollback
	}
	if t == b.streamTime {
		return []Emitted{}, nil
	}

	closedTree, openTree := splitByEnd(b.root, t-b.grace)
	b.peeks++
	emitted := appendEntriesInOrder(closedTree, make([]Emitted, 0, nodeSize(closedTree)), Final)

	removedKeys := make(map[entryKey]struct{}, nodeSize(closedTree))
	removedKeys = appendKeysInOrder(closedTree, removedKeys)
	for removedKey := range removedKeys {
		delete(b.entries, removedKey)
	}

	b.root = openTree
	b.streamTime = t
	return emitted, nil
}

// Buffered lists retained entries by ascending end and then ascending key bytes.
func (b *Buffer) Buffered() []BufferedEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	return appendBufferedInOrder(b.root, make([]BufferedEntry, 0, nodeSize(b.root)))
}

// StreamTime returns the current monotonic stream time.
func (b *Buffer) StreamTime() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.streamTime
}

// Late returns the number of updates discarded after their closing time.
func (b *Buffer) Late() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.late
}
