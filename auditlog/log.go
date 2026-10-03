package auditlog

import "strconv"

func validTs(ts int64) bool { return ts >= 0 && ts <= maxTs }

// Append writes a data record (Typ 0) with the current key, then discards
// the key by evolving it. Rejection order: invalid argument, already sealed,
// timestamp rollback, capacity full (the seal slot is reserved).
func (l *Log) Append(ts int64, data []byte) error {
	if !validTs(ts) || len(data) > l.maxData {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return ErrSealed
	}
	if ts < l.lastTs {
		return ErrTimeRollback
	}
	if l.nextI >= int64(l.cap)-1 {
		return ErrCapFull
	}
	dataCopy := cloneBytes(data)
	e := &Entry{
		Index: l.nextI,
		Typ:   TypData,
		Ts:    ts,
		Data:  dataCopy,
		Tag:   l.mac(l.ki, l.nextI, TypData, ts, dataCopy),
	}
	l.ki = l.evolve(l.ki)
	l.entries = append(l.entries, e)
	l.nextI++
	l.lastTs = ts
	return nil
}

// Seal writes the seal record (Typ 1). Its Data is the decimal ASCII text of
// the record index. After sealing, no further write or export is accepted.
// Rejection order: invalid argument, already sealed, timestamp rollback.
func (l *Log) Seal(ts int64) error {
	if !validTs(ts) {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return ErrSealed
	}
	if ts < l.lastTs {
		return ErrTimeRollback
	}
	data := []byte(strconv.FormatInt(l.nextI, 10))
	e := &Entry{
		Index: l.nextI,
		Typ:   TypSeal,
		Ts:    ts,
		Data:  data,
		Tag:   l.mac(l.ki, l.nextI, TypSeal, ts, data),
	}
	l.ki = l.evolve(l.ki)
	l.entries = append(l.entries, e)
	l.nextI++
	l.lastTs = ts
	l.sealed = true
	return nil
}

// Rekey writes a rekey record (Typ 2) and replaces the current key with
// rekey(k, i) instead of evolving it. The rekey record consumes one capacity
// slot like a data record and does not seal the log. Rejection order is the
// same as Append (only ts is validated as an argument).
func (l *Log) Rekey(ts int64) error {
	if !validTs(ts) {
		return ErrInvalidArg
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return ErrSealed
	}
	if ts < l.lastTs {
		return ErrTimeRollback
	}
	if l.nextI >= int64(l.cap)-1 {
		return ErrCapFull
	}
	data := []byte(strconv.FormatInt(l.nextI, 10))
	e := &Entry{
		Index: l.nextI,
		Typ:   TypRekey,
		Ts:    ts,
		Data:  data,
		Tag:   l.mac(l.ki, l.nextI, TypRekey, ts, data),
	}
	l.ki = l.rekey(l.ki, l.nextI)
	l.entries = append(l.entries, e)
	l.nextI++
	l.lastTs = ts
	return nil
}

// Export returns the current (nextIndex, currentKey) pair for checkpoint
// custody. It is rejected once the log has been sealed.
func (l *Log) Export() (index int64, key uint64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return 0, 0, ErrSealed
	}
	return l.nextI, l.ki, nil
}

// Entries returns a deep copy snapshot of the log entries.
func (l *Log) Entries() []*Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneEntries(l.entries)
}

// snapshot returns a deep copy under the read lock.
func (l *Log) snapshot() []*Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return cloneEntries(l.entries)
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneEntry(e *Entry) *Entry {
	return &Entry{
		Index: e.Index,
		Typ:   e.Typ,
		Ts:    e.Ts,
		Data:  cloneBytes(e.Data),
		Tag:   e.Tag,
	}
}

func cloneEntries(in []*Entry) []*Entry {
	out := make([]*Entry, len(in))
	for i, e := range in {
		out[i] = cloneEntry(e)
	}
	return out
}
