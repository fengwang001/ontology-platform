package trustanchor

import "sync"

const (
	StatusUntracked = "UNTRACKED"
	StatusAddPend   = "ADDPEND"
	StatusValid     = "VALID"
	StatusMissing   = "MISSING"
	StatusRevoked   = "REVOKED"
	StatusBanned    = "BANNED"
)

const (
	ReasonInvalidConfig = "INVALID_CONFIG"
	ReasonInvalidArgs   = "INVALID_ARGS"
	ReasonClockRollback = "CLOCK_ROLLBACK"
	ReasonUntrusted     = "UNTRUSTED_SIGNERS"
	ReasonDeadlock      = "DEADLOCK"
	ReasonLimit         = "LIMIT_EXCEEDED"
)

type Entry struct {
	Key     []byte
	Revoked bool
}

type KeyState struct {
	Status string
	Since  int64
	At     int64
	Count  int
}

type RejectError struct {
	Reason  string
	Message string
	Index   int
	Detail  string
	Count   int
	Since   int64
	At      int64
}

func (e *RejectError) Error() string { return e.Reason + ": " + e.Message }

type Tracker struct {
	mu        sync.RWMutex
	h         int64
	r         int64
	kmax      int
	m         int
	q         int
	last      int64
	keys      map[string]*record
	banned    map[string]struct{}
	bannedAt  map[string]int64
	processed int64
}

type record struct {
	status string
	since  int64
	at     int64
	count  int
}

func New(h, r int64, kmax, requiredAppearances, quorum int, anchors ...[]byte) (*Tracker, error) {
	if h < 0 || h > 1_000_000_000 {
		return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "hold duration must be between 0 and 10^9", Detail: "H"}
	}
	if r < 1 || r > 1_000_000_000 {
		return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "retention duration must be between 1 and 10^9", Detail: "R"}
	}
	if kmax < 1 || kmax > 1000 {
		return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "Kmax must be between 1 and 1000", Detail: "Kmax"}
	}
	if requiredAppearances < 1 || requiredAppearances > 100 {
		return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "M must be between 1 and 100", Detail: "M"}
	}
	if quorum < 1 || quorum > kmax {
		return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "Q must be between 1 and Kmax", Detail: "Q"}
	}
	if len(anchors) < quorum || len(anchors) > kmax {
		return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "initial anchor count must be between Q and Kmax", Detail: "anchors"}
	}

	keys := make(map[string]*record, len(anchors))
	for index, anchor := range anchors {
		if len(anchor) == 0 {
			return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "initial anchor must be a non-empty key", Index: index, Detail: "anchors"}
		}
		id := string(anchor)
		if _, exists := keys[id]; exists {
			return nil, &RejectError{Reason: ReasonInvalidConfig, Message: "initial anchors must be distinct", Index: index, Detail: "anchors"}
		}
		keys[id] = &record{status: StatusValid}
	}

	return &Tracker{
		h:        h,
		r:        r,
		kmax:     kmax,
		m:        requiredAppearances,
		q:        quorum,
		keys:     keys,
		banned:   make(map[string]struct{}),
		bannedAt: make(map[string]int64),
	}, nil
}

func (t *Tracker) Observe(entries []Entry, signers [][]byte, now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return &RejectError{Reason: ReasonInvalidArgs, Message: "now must be between 0 and 10^15", Detail: "now"}
	}
	if len(entries) < 1 || len(entries) > 64 {
		return &RejectError{Reason: ReasonInvalidArgs, Message: "key set must contain between 1 and 64 entries", Detail: "entries"}
	}
	seenEntries := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		if len(entry.Key) == 0 {
			return &RejectError{Reason: ReasonInvalidArgs, Message: "key identifier must be non-empty", Index: index, Detail: "entries.key"}
		}
		id := string(entry.Key)
		if _, exists := seenEntries[id]; exists {
			return &RejectError{Reason: ReasonInvalidArgs, Message: "key identifiers in an observation must be distinct", Index: index, Detail: "entries.key"}
		}
		seenEntries[id] = struct{}{}
	}
	for index, signer := range signers {
		if len(signer) == 0 {
			return &RejectError{Reason: ReasonInvalidArgs, Message: "signer identifier must be non-empty", Index: index, Detail: "signers"}
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if now < t.last {
		return &RejectError{
			Reason:  ReasonClockRollback,
			Message: "observation time is earlier than the latest accepted observation",
			Detail:  "now",
			Since:   t.last,
			At:      now,
		}
	}

	trustedSigners := make(map[string]struct{})
	for _, signer := range signers {
		id := string(signer)
		if rec, exists := t.keys[id]; exists && rec.status == StatusValid {
			trustedSigners[id] = struct{}{}
		}
	}
	if len(trustedSigners) < t.q {
		firstInvalid := -1
		for index, signer := range signers {
			id := string(signer)
			if rec, exists := t.keys[id]; !exists || rec.status != StatusValid {
				firstInvalid = index
				break
			}
		}
		err := &RejectError{
			Reason:  ReasonUntrusted,
			Message: "fewer than Q distinct signers were VALID before the observation",
			Detail:  "signers",
			Count:   len(trustedSigners),
			Since:   int64(t.q),
		}
		if firstInvalid >= 0 {
			err.Index = firstInvalid
		}
		return err
	}

	candidate := cloneRecords(t.keys)
	candidateBanned := cloneStringSet(t.banned)
	candidateBannedAt := cloneInt64Map(t.bannedAt)
	processedCount := len(t.keys)

	for id, rec := range candidate {
		if _, present := seenEntries[id]; !present {
			switch rec.status {
			case StatusValid:
				rec.status = StatusMissing
				rec.since = now
				rec.at = 0
				rec.count = 0
			case StatusAddPend:
				delete(candidate, id)
			}
		}
	}

	for _, entry := range entries {
		id := string(entry.Key)
		if _, banned := t.banned[id]; banned {
			continue
		}
		processedCount++
		rec, exists := candidate[id]
		if !exists {
			if entry.Revoked {
				continue
			}
			candidate[id] = &record{status: StatusAddPend, since: now, count: 1}
			continue
		}
		if entry.Revoked {
			switch rec.status {
			case StatusValid, StatusMissing:
				rec.status = StatusRevoked
				rec.at = now
				rec.since = 0
				rec.count = 0
			case StatusAddPend:
				delete(candidate, id)
			}
			continue
		}
		switch rec.status {
		case StatusAddPend:
			rec.count++
		case StatusMissing:
			rec.status = StatusValid
			rec.since = 0
			rec.at = 0
			rec.count = 0
		}
	}

	validCount := 0
	trackedCount := 0
	for id, rec := range candidate {
		trackedCount++
		switch rec.status {
		case StatusValid:
			validCount++
		case StatusAddPend:
			if now >= rec.since+t.h && rec.count >= t.m {
				rec.status = StatusValid
				rec.since = 0
				rec.count = 0
				validCount++
			}
		case StatusRevoked:
			if now >= rec.at+t.r {
				candidateBanned[id] = struct{}{}
				candidateBannedAt[id] = rec.at
				delete(candidate, id)
				trackedCount--
			}
		case StatusMissing:
			if now >= rec.since+t.r {
				delete(candidate, id)
				trackedCount--
			}
		}
	}

	if validCount < t.q {
		return &RejectError{
			Reason:  ReasonDeadlock,
			Message: "accepting the observation would leave fewer than Q VALID keys",
			Detail:  "VALID",
			Count:   validCount,
			Since:   int64(t.q),
		}
	}
	if trackedCount > t.kmax {
		return &RejectError{
			Reason:  ReasonLimit,
			Message: "accepting the observation would track more than Kmax keys",
			Detail:  "tracked",
			Count:   trackedCount,
			Since:   int64(t.kmax),
		}
	}

	t.keys = candidate
	t.banned = candidateBanned
	t.bannedAt = candidateBannedAt
	t.last = now
	t.processed += int64(processedCount)
	return nil
}

func cloneRecords(in map[string]*record) map[string]*record {
	out := make(map[string]*record, len(in))
	for id, rec := range in {
		copyRecord := *rec
		out[id] = &copyRecord
	}
	return out
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for value := range in {
		out[value] = struct{}{}
	}
	return out
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (t *Tracker) State(key []byte) KeyState {
	if len(key) == 0 {
		return KeyState{Status: StatusUntracked}
	}
	id := string(key)
	t.mu.RLock()
	defer t.mu.RUnlock()
	if at, exists := t.bannedAt[id]; exists {
		return KeyState{Status: StatusBanned, At: at}
	}
	if rec, exists := t.keys[id]; exists {
		return KeyState{Status: rec.status, Since: rec.since, At: rec.at, Count: rec.count}
	}
	return KeyState{Status: StatusUntracked}
}
