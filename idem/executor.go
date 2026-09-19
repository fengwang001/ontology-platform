package idem

// Do executes fn at most once per (key, fingerprint) pair, subject to TTL
// expiry. Concurrent callers with the same key and fingerprint block until the
// running call finishes and share its result.
func (e *Executor) Do(key, fp string, fn func() (Result, error)) (Result, Outcome, error) {
	e.mu.Lock()
	en := e.slot[key]
	if en != nil {
		if en.fp != fp {
			e.mu.Unlock()
			return Result{}, Executed, ErrFingerprintMismatch
		}
		if !en.inFlight() && en.expired(e.now, e.ttl) {
			en = nil
		}
	}

	if en == nil {
		en = newEntry(fp)
		e.slot[key] = en
		e.mu.Unlock()

		if en.run(fn, e.now) {
			e.release(key, en)
		}
		return en.res, Executed, en.err
	}

	// The entry is either in flight or a live completed record.
	done := en.done
	inFlight := en.inFlight()
	e.mu.Unlock()

	if inFlight {
		<-done
		return en.res, Waited, en.err
	}
	return en.res, Replayed, en.err
}

// release removes the slot iff it still points at en. Retriable failures and
// panics take this path so the key can be retried immediately.
func (e *Executor) release(key string, en *entry) {
	e.mu.Lock()
	if e.slot[key] == en {
		delete(e.slot, key)
	}
	e.mu.Unlock()
}
