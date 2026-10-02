package kms

import "container/heap"

// key is the internal mutable state of one live key generation.
type key struct {
	state     State
	gen       int64
	p         int64 // rotation period, 0 disables automatic rotation
	versions  []Version
	d         int64 // next automatic rotation time
	deleteAt  int64
	heapToken uint64 // validates the live heap entry for this pending period
}

// maxVersion returns the highest retained (and highest ever, post catch-up)
// version number.
func (k *key) maxVersion() int64 { return k.versions[len(k.versions)-1].Number }

// minVersion returns the lowest retained version number.
func (k *key) minVersion() int64 { return k.versions[0].Number }

func (k *key) clone() key {
	cp := *k
	cp.versions = append([]Version(nil), k.versions...)
	return cp
}

// catchUp rotates an Enabled key up to now, returning how many versions were
// materialized. With c = floor((now-d)/P)+1 due rotations, versions are
// created at the scheduled times d, d+P, ..., d+(c-1)P, version numbers
// advance by the full c, but only the last min(c, V) versions are
// materialized; retained versions are then trimmed to the newest V.
func (k *key) catchUp(now, v int64) int64 {
	if k.state != Enabled || k.p == 0 || k.d > now {
		return 0
	}
	c := (now-k.d)/k.p + 1
	m := c
	if m > v {
		m = v
	}
	first := k.maxVersion() + c - m + 1
	firstTime := k.d + (c-m)*k.p
	for i := int64(0); i < m; i++ {
		k.versions = append(k.versions, Version{Number: first + i, CreatedAt: firstTime + i*k.p})
	}
	k.d += c * k.p
	minNum := k.maxVersion() - v + 1
	if minNum < 1 {
		minNum = 1
	}
	drop := 0
	for drop < len(k.versions) && k.versions[drop].Number < minNum {
		drop++
	}
	if drop > 0 {
		k.versions = append([]Version(nil), k.versions[drop:]...)
	}
	return m
}

// validPeriod reports whether p is an acceptable rotation period.
func validPeriod(p int64) bool {
	return p == 0 || (p >= 60 && p <= 1_000_000_000)
}

// checkNow validates the logical clock range.
func checkNow(now int64) *Error {
	if now < 0 || now > MaxNow {
		return &Error{Code: ErrInvalidParam, Reason: "now out of range [0,1e15]", Limit: now}
	}
	return nil
}

// checkClock rejects operations whose now is below the max now of
// previously accepted operations.
func (m *Manager) checkClock(now int64) *Error {
	if now < m.maxNow {
		return &Error{Code: ErrClockRegression, Reason: "now below max accepted now", MaxNow: m.maxNow, Limit: now}
	}
	return nil
}

// accept commits the global clock and lazily reclaims keys whose deleteAt
// has passed. Only accepted operations may call it.
func (m *Manager) accept(now int64) {
	m.maxNow = now
	m.reclaim(now)
}

// getKey returns the live key for id at now, treating Pending keys with
// deleteAt <= now as already deleted. It never mutates state.
func (m *Manager) getKey(id string, now int64) *key {
	k := m.keys[id]
	if k == nil {
		return nil
	}
	if k.state == Pending && k.deleteAt <= now {
		return nil
	}
	return k
}

// liveCount counts logically live keys at now without mutating state:
// physically present keys minus those already expired at now.
func (m *Manager) liveCount(now int64) int64 {
	n := int64(len(m.keys))
	for _, e := range m.pending {
		if e.deleteAt > now {
			continue
		}
		if k := m.keys[e.id]; k != nil && k.state == Pending && k.heapToken == e.token {
			n--
		}
	}
	return n
}

// reclaim physically removes keys expired at now. Pops are bounded by the
// number of keys expiring now plus stale (cancelled) entries surfaced,
// independent of the total live-key count.
func (m *Manager) reclaim(now int64) {
	for len(m.pending) > 0 && m.pending[0].deleteAt <= now {
		e := heap.Pop(&m.pending).(delEntry)
		m.heapPops++
		if k := m.keys[e.id]; k != nil && k.state == Pending && k.heapToken == e.token {
			delete(m.keys, e.id)
		}
	}
}

// checkID runs the shared validation for id+now operations and returns the
// live key.
func (m *Manager) checkID(id string, now int64) (*key, *Error) {
	if id == "" {
		return nil, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	k := m.getKey(id, now)
	if k == nil {
		return nil, &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	return k, nil
}

// checkCredential runs the shared validation for credential operations and
// returns the live key bound to the credential generation.
func (m *Manager) checkCredential(cred Credential, now int64) (*key, *Error) {
	if cred.ID == "" {
		return nil, &Error{Code: ErrInvalidParam, Reason: "empty credential id"}
	}
	if cred.Gen < 1 {
		return nil, &Error{Code: ErrInvalidParam, ID: cred.ID, Gen: cred.Gen, Reason: "credential generation below 1"}
	}
	if cred.Version < 1 {
		return nil, &Error{Code: ErrInvalidParam, ID: cred.ID, Gen: cred.Gen, Version: cred.Version, Reason: "credential version below 1"}
	}
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	if m.genMax[cred.ID] == 0 || cred.Gen > m.genMax[cred.ID] {
		return nil, &Error{Code: ErrNotFound, ID: cred.ID, Gen: cred.Gen, Reason: "key id never created or generation above max known"}
	}
	k := m.getKey(cred.ID, now)
	if k == nil || k.gen != cred.Gen {
		return nil, &Error{Code: ErrKeyDeleted, ID: cred.ID, Gen: cred.Gen, Reason: "generation is not the live generation (deleted or superseded)"}
	}
	if k.state != Enabled {
		return nil, deniedErr(cred.ID, k.state)
	}
	return k, nil
}

// deniedErr builds the state-denial error for encrypt/decrypt operations.
func deniedErr(id string, s State) *Error {
	reason := "disabled"
	if s == Pending {
		reason = "pending deletion"
	}
	return &Error{Code: ErrStateDenied, ID: id, State: s, Reason: reason}
}

// checkVersion validates the credential version against the (post catch-up)
// retained version window [minNum, maxNum].
func checkVersion(cred Credential, minNum, maxNum int64) *Error {
	if cred.Version > maxNum {
		return &Error{Code: ErrVersionProblem, ID: cred.ID, Gen: cred.Gen, Version: cred.Version, Reason: "version does not exist", Limit: maxNum}
	}
	if cred.Version < minNum {
		return &Error{Code: ErrVersionProblem, ID: cred.ID, Gen: cred.Gen, Version: cred.Version, Reason: "version retired", Limit: minNum}
	}
	return nil
}

// delEntry is one scheduled-deletion heap entry. Entries become stale when
// the key's heapToken advances (cancellation) or the key is removed.
type delEntry struct {
	deleteAt int64
	id       string
	token    uint64
}

// delHeap is a min-heap of deletion entries ordered by deleteAt.
type delHeap []delEntry

func (h delHeap) Len() int { return len(h) }

func (h delHeap) Less(i, j int) bool {
	if h[i].deleteAt != h[j].deleteAt {
		return h[i].deleteAt < h[j].deleteAt
	}
	return h[i].id < h[j].id
}

func (h delHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *delHeap) Push(x any) { *h = append(*h, x.(delEntry)) }

func (h *delHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
