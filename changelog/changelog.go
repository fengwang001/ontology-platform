package changelog

// Entry is one visible write record in the log.
type Entry struct {
	Seq   int64
	Key   string
	Value string
}

// Record is the result of reading a key at a position.
type Record struct {
	Seq   int64
	Value string
}

// Changelog is an append-only keyed log with interval compaction.
type Changelog struct {
}

// New creates an empty changelog whose next sequence is 1.
func New() *Changelog {
	return &Changelog{}
}

// Append appends one write and returns its assigned sequence.
func (c *Changelog) Append(key, value string) (int64, error) {
	return 0, nil
}

// Read returns the largest-sequence visible record of key at position.
func (c *Changelog) Read(position int64, key string) (Record, error) {
	return Record{}, nil
}

// Compact merges writes inside [left,right], keeping only the last per key.
func (c *Changelog) Compact(left, right int64) error {
	return nil
}

// NextSeq returns the sequence that the next append will receive.
func (c *Changelog) NextSeq() int64 {
	return 1
}
