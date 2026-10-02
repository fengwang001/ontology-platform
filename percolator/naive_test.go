package percolator

import "sort"

// naiveStore is an independent, deliberately literal reimplementation of
// the specification used as a differential oracle. It uses the same
// exported types but shares no code paths with Store.
type naiveStore struct {
	oracle int64
	water  int64
	txns   map[int64]*naiveTxn
	keys   map[string]*naiveKey
}

type naiveTxn struct {
	state    string // "fresh", "prewritten", "committed", "aborted"
	muts     []Mutation
	primary  string
	commitTS int64
}

type naiveKey struct {
	versions []Version
	lock     *Lock
}

func newNaive() *naiveStore {
	return &naiveStore{
		txns: map[int64]*naiveTxn{},
		keys: map[string]*naiveKey{},
	}
}

func (n *naiveStore) key(key string) *naiveKey {
	k := n.keys[key]
	if k == nil {
		k = &naiveKey{}
		n.keys[key] = k
	}
	return k
}

func (n *naiveStore) begin() int64 {
	n.oracle++
	st := n.oracle
	n.txns[st] = &naiveTxn{state: "fresh"}
	return st
}

func naiveRollback(k *naiveKey, st int64) bool {
	for _, v := range k.versions {
		if v.Kind == Rollback && v.StartTS == st {
			return true
		}
	}
	return false
}

func naiveAddVersion(k *naiveKey, v Version) {
	k.versions = append(k.versions, v)
	sort.SliceStable(k.versions, func(i, j int) bool {
		a, b := k.versions[i], k.versions[j]
		if a.CommitTS != b.CommitTS {
			return a.CommitTS > b.CommitTS
		}
		return a.StartTS > b.StartTS
	})
}

func (n *naiveStore) addRollback(k *naiveKey, st int64) {
	if naiveRollback(k, st) {
		return
	}
	naiveAddVersion(k, Version{CommitTS: st, StartTS: st, Kind: Rollback})
}

func (n *naiveStore) timeOK(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < n.water {
		return ErrClockSkew
	}
	return nil
}

func (n *naiveStore) prewrite(st int64, muts []Mutation, primary string, ttl, now int64) error {
	if len(muts) == 0 {
		return ErrInvalidArg
	}
	seen := map[string]bool{}
	hasPrimary := false
	for _, m := range muts {
		if m.Key == "" || (m.Kind != Put && m.Kind != Delete) {
			return ErrInvalidArg
		}
		if seen[m.Key] {
			return ErrInvalidArg
		}
		seen[m.Key] = true
		if m.Key == primary {
			hasPrimary = true
		}
	}
	if primary == "" || !hasPrimary || ttl < 1 || ttl > 1_000_000_000 {
		return ErrInvalidArg
	}
	t := n.txns[st]
	if t == nil {
		return ErrNoSuchTxn
	}
	if t.state != "fresh" {
		return ErrTxnState
	}
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < n.water {
		return ErrClockSkew
	}

	for _, m := range muts {
		k, ok := n.keys[m.Key]
		if !ok {
			continue
		}
		if naiveRollback(k, st) {
			return ErrAlreadyAbort
		}
		if k.lock != nil && k.lock.StartTS != st {
			return ErrKeyLocked
		}
		for _, v := range k.versions {
			if v.Kind != Rollback && v.CommitTS > st {
				return ErrWriteConflict
			}
		}
	}

	n.water = now
	deadline := now + ttl
	for _, m := range muts {
		k := n.key(m.Key)
		k.lock = &Lock{StartTS: st, Primary: primary, Deadline: deadline, Kind: m.Kind, Value: m.Value}
	}
	t.state = "prewritten"
	t.muts = cloneMuts(muts)
	t.primary = primary
	return nil
}

func (n *naiveStore) commitPrimary(st int64) (int64, error) {
	t := n.txns[st]
	if t == nil {
		return 0, ErrNoSuchTxn
	}
	if t.state != "prewritten" {
		return 0, ErrTxnState
	}
	k := n.key(t.primary)
	if k.lock == nil || k.lock.StartTS != st {
		return 0, ErrLockLost
	}
	n.oracle++
	cts := n.oracle
	lk := k.lock
	k.lock = nil
	naiveAddVersion(k, Version{CommitTS: cts, StartTS: st, Kind: lk.Kind, Value: lk.Value})
	t.state = "committed"
	t.commitTS = cts
	return cts, nil
}

func (n *naiveStore) commitKeys(st int64, keys []string) error {
	t := n.txns[st]
	if t == nil {
		return ErrNoSuchTxn
	}
	if t.state != "committed" {
		return ErrTxnState
	}
	allowed := map[string]bool{}
	for _, m := range t.muts {
		allowed[m.Key] = true
	}
	for _, key := range keys {
		if !allowed[key] {
			return ErrInvalidArg
		}
	}
	for _, key := range keys {
		k := n.key(key)
		if k.lock == nil || k.lock.StartTS != st {
			continue
		}
		lk := k.lock
		k.lock = nil
		naiveAddVersion(k, Version{CommitTS: t.commitTS, StartTS: st, Kind: lk.Kind, Value: lk.Value})
	}
	return nil
}

func (n *naiveStore) abort(st int64) error {
	t := n.txns[st]
	if t == nil {
		return ErrNoSuchTxn
	}
	if t.state != "prewritten" {
		return ErrTxnState
	}
	for _, m := range t.muts {
		k := n.key(m.Key)
		if k.lock != nil && k.lock.StartTS == st {
			k.lock = nil
		}
		n.addRollback(k, st)
	}
	t.state = "aborted"
	return nil
}

func (n *naiveStore) resolve(primary string, st, now int64) TxnStatusResult {
	k := n.key(primary)
	if k.lock != nil && k.lock.StartTS == st {
		if now >= k.lock.Deadline {
			k.lock = nil
			n.addRollback(k, st)
			return TxnStatusResult{Status: StatusRolledBack}
		}
		return TxnStatusResult{Status: StatusLive}
	}
	var commitTS int64
	foundCommit := false
	for _, v := range k.versions {
		if v.StartTS != st {
			continue
		}
		if v.Kind == Rollback {
			return TxnStatusResult{Status: StatusRolledBack}
		}
		if !foundCommit || v.CommitTS > commitTS {
			commitTS = v.CommitTS
			foundCommit = true
		}
	}
	if foundCommit {
		return TxnStatusResult{Status: StatusCommitted, CommitTS: commitTS}
	}
	n.addRollback(k, st)
	return TxnStatusResult{Status: StatusRolledBack}
}

func (n *naiveStore) checkTxnStatus(primary string, st, now int64) (TxnStatusResult, error) {
	if primary == "" || st < 1 {
		return TxnStatusResult{}, ErrInvalidArg
	}
	if err := n.timeOK(now); err != nil {
		return TxnStatusResult{}, err
	}
	n.water = now
	return n.resolve(primary, st, now), nil
}

func (n *naiveStore) get(key string, rts, now int64) (GetResult, error) {
	if key == "" || rts < 0 {
		return GetResult{}, ErrInvalidArg
	}
	if err := n.timeOK(now); err != nil {
		return GetResult{}, err
	}
	n.water = now
	k, ok := n.keys[key]
	if !ok {
		return GetResult{Exists: false}, nil
	}
	if k.lock != nil && k.lock.StartTS <= rts {
		lk := k.lock
		res := n.resolve(lk.Primary, lk.StartTS, now)
		switch res.Status {
		case StatusCommitted:
			if k.lock != nil && k.lock.StartTS == lk.StartTS {
				fwd := k.lock
				k.lock = nil
				naiveAddVersion(k, Version{CommitTS: res.CommitTS, StartTS: fwd.StartTS, Kind: fwd.Kind, Value: fwd.Value})
			}
		case StatusRolledBack:
			if k.lock != nil && k.lock.StartTS == lk.StartTS {
				k.lock = nil
				n.addRollback(k, lk.StartTS)
			}
		default:
			return GetResult{}, ErrKeyLocked
		}
	}
	for _, v := range k.versions {
		if v.CommitTS <= rts {
			if v.Kind == Rollback {
				continue
			}
			if v.Kind == Delete {
				return GetResult{Exists: false}, nil
			}
			return GetResult{Exists: true, Value: v.Value}, nil
		}
	}
	return GetResult{Exists: false}, nil
}

func (n *naiveStore) snapshot() Snapshot {
	keys := map[string]KeySnapshot{}
	for name, k := range n.keys {
		snap := KeySnapshot{Versions: append([]Version(nil), k.versions...)}
		if k.lock != nil {
			lk := *k.lock
			snap.Lock = &lk
		}
		keys[name] = snap
	}
	return Snapshot{Oracle: n.oracle, Water: n.water, Keys: keys}
}
