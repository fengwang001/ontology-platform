package wal

// Replay returns all records with Seq > from, in ascending Seq order.
// It pairs with Truncate: replaying from a truncation point yields
// exactly the records that survived it.
func (l *Log) Replay(from uint64) ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Record
	for _, r := range l.records {
		if r.Seq > from {
			out = append(out, r)
		}
	}
	return out, nil
}

// Truncate drops every record with Seq <= upto.
func (l *Log) Truncate(upto uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := l.records[:0]
	for _, r := range l.records {
		if r.Seq > upto {
			keep = append(keep, r)
		}
	}
	l.records = keep
	return nil
}
