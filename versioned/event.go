package versioned

// Op identifies the kind of change an Event performs.
type Op int

const (
	// OpWrite upserts the key's row.
	OpWrite Op = iota + 1
	// OpDelete removes the key's row and writes a tombstone.
	OpDelete
)

// Event is a versioned change that may arrive out of order.
type Event struct {
	Key     string
	Version int64
	Op      Op
	Value   []byte
}

// Row is a live (visible) row for a key.
type Row struct {
	Version int64
	Value   []byte
}
