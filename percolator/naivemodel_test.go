package percolator

// naiveStore is an independent, deliberately literal reimplementation of the
// specification, written straight from the rule list without sharing any
// production helper. Differential tests compare the real Store against it.
type naiveStore struct {
	oracle    uint64
	watermark uint64
	txns      map[uint64]*naiveTxn
	keys      map[string]*naiveKey
}

type naiveTxn struct {
	state    string // "started", "prewritten", "committed", "aborted"
	muts     []Mutation
	primary  string
	commitTs uint64
}

type naiveKey struct {
	versions []Version
	lock     *Lock
}

func newNaive() *naiveStore {
	return &naiveStore{
		txns: map[uint64]*naiveTxn{},
		keys: map[string]*naiveKey{},
	}
}

func (n *naiveStore) key(k string) *naiveKey {
	ks := n.keys[k]
	if ks == nil {
		ks = &naiveKey{}
		n.keys[k] = ks
	}
	return ks
}

func (n *naiveStore) Begin() uint64 {
	n.oracle++
	n.txns[n.oracle] = &naiveTxn{state: "started"}
	return n.oracle
}

func naiveRollbackExists(ks *naiveKey, st uint64) bool {
	for _, v := range ks.versions {
		if v.StartTs == st && v.Type == Rollback {
			return true
		}
	}
	return false
}

func naiveAddRollback(ks *naiveKey, st uint64) {
	if naiveRollbackExists(ks, st) {
		return
	}
	ks.versions = append(ks.versions, Version{CommitTs: st, StartTs: st, Type: Rollback})
}

func naiveHasNewerCommit(ks *naiveKey, st uint64) bool {
	for _, v := range ks.versions {
		if v.Type != Rollback && v.CommitTs > st {
			return true
		}
	}
	return false
}

func (n *naiveStore) Prewrite(st uint64, muts []Mutation, primary string, ttl, now uint64) ErrorCode {
	if len(muts) == 0 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	present := false
	for _, m := range muts {
		if m.Key == "" || (m.Type != Put && m.Type != Delete) {
			return ErrInvalidArgument
		}
		if seen[m.Key] {
			return ErrInvalidArgument
		}
		seen[m.Key] = true
		if m.Key == primary {
			present = true
		}
	}
	if primary == "" || !present || ttl < 1 || ttl > maxTTL {
		return ErrInvalidArgument
	}
	t, ok := n.txns[st]
	if !ok {
		return ErrTxnNotFound
	}
	if t.state != "started" {
		return ErrInvalidState
	}
	if now > maxNow {
		return ErrInvalidTime
	}
	if now < n.watermark {
		return ErrClockSkew
	}
	expire := now + ttl
	locked := make([]*naiveKey, 0, len(muts))
	for _, m := range muts {
		ks := n.key(m.Key)
		switch {
		case naiveRollbackExists(ks, st):
			return ErrAlreadyAborted
		case ks.lock != nil && ks.lock.StartTs != st:
			return ErrKeyLocked
		case naiveHasNewerCommit(ks, st):
			return ErrWriteConflict
		}
		locked = append(locked, ks)
	}
	for i, m := range muts {
		locked[i].lock = &Lock{StartTs: st, Primary: primary, Expire: expire, Type: m.Type, Value: m.Value}
	}
	cp := make([]Mutation, len(muts))
	copy(cp, muts)
	t.state = "prewritten"
	t.muts = cp
	t.primary = primary
	n.watermark = now
	return ""
}

func (n *naiveStore) CommitPrimary(st uint64) (uint64, ErrorCode) {
	t, ok := n.txns[st]
	if !ok {
		return 0, ErrTxnNotFound
	}
	if t.state != "prewritten" {
		return 0, ErrInvalidState
	}
	ks := n.key(t.primary)
	if ks.lock == nil || ks.lock.StartTs != st {
		return 0, ErrLockLost
	}
	n.oracle++
	ct := n.oracle
	lk := ks.lock
	ks.versions = append(ks.versions, Version{CommitTs: ct, StartTs: st, Type: lk.Type, Value: lk.Value})
	ks.lock = nil
	t.state = "committed"
	t.commitTs = ct
	return ct, ""
}

func (n *naiveStore) CommitKeys(st uint64, keys []string) ErrorCode {
	t, ok := n.txns[st]
	if !ok {
		return ErrTxnNotFound
	}
	if t.state != "committed" {
		return ErrInvalidState
	}
	owned := map[string]bool{}
	for _, m := range t.muts {
		owned[m.Key] = true
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if k == "" || seen[k] || !owned[k] {
			return ErrInvalidArgument
		}
		seen[k] = true
	}
	for _, k := range keys {
		ks := n.key(k)
		if ks.lock == nil || ks.lock.StartTs != st {
			continue
		}
		lk := ks.lock
		ks.versions = append(ks.versions, Version{CommitTs: t.commitTs, StartTs: st, Type: lk.Type, Value: lk.Value})
		ks.lock = nil
	}
	return ""
}

func (n *naiveStore) Abort(st uint64) ErrorCode {
	t, ok := n.txns[st]
	if !ok {
		return ErrTxnNotFound
	}
	if t.state != "prewritten" {
		return ErrInvalidState
	}
	for _, m := range t.muts {
		ks := n.key(m.Key)
		if ks.lock != nil && ks.lock.StartTs == st {
			ks.lock = nil
		}
		naiveAddRollback(ks, st)
	}
	t.state = "aborted"
	return ""
}

// resolve mirrors resolveTxn.
func (n *naiveStore) resolve(primary string, st, now uint64) (TxnStatus, uint64) {
	ks := n.key(primary)
	if ks.lock != nil && ks.lock.StartTs == st {
		if now >= ks.lock.Expire {
			ks.lock = nil
			naiveAddRollback(ks, st)
			return StatusRolledBack, 0
		}
		return StatusLive, 0
	}
	for _, v := range ks.versions {
		if v.StartTs == st {
			if v.Type == Rollback {
				return StatusRolledBack, 0
			}
			return StatusCommitted, v.CommitTs
		}
	}
	naiveAddRollback(ks, st)
	return StatusRolledBack, 0
}

func (n *naiveStore) CheckTxnStatus(primary string, st, now uint64) (TxnStatus, uint64, ErrorCode) {
	if primary == "" || st < 1 {
		return 0, 0, ErrInvalidArgument
	}
	if now > maxNow {
		return 0, 0, ErrInvalidTime
	}
	if now < n.watermark {
		return 0, 0, ErrClockSkew
	}
	status, ct := n.resolve(primary, st, now)
	n.watermark = now
	return status, ct, ""
}

func (n *naiveStore) Get(key string, rts int64, now uint64) (bool, string, ErrorCode) {
	if key == "" || rts < 0 {
		return false, "", ErrInvalidArgument
	}
	if now > maxNow {
		return false, "", ErrInvalidTime
	}
	if now < n.watermark {
		return false, "", ErrClockSkew
	}
	ks := n.key(key)
	if ks.lock != nil && ks.lock.StartTs <= uint64(rts) {
		lk := ks.lock
		status, ct := n.resolve(lk.Primary, lk.StartTs, now)
		if status == StatusCommitted {
			ks.versions = append(ks.versions, Version{CommitTs: ct, StartTs: lk.StartTs, Type: lk.Type, Value: lk.Value})
			ks.lock = nil
		} else if status == StatusRolledBack {
			ks.lock = nil
			naiveAddRollback(ks, lk.StartTs)
		} else {
			return false, "", ErrLocked
		}
	}
	n.watermark = now
	var best *Version
	for i := range ks.versions {
		v := &ks.versions[i]
		if v.Type == Rollback || v.CommitTs > uint64(rts) {
			continue
		}
		if best == nil || v.CommitTs > best.CommitTs {
			best = v
		}
	}
	if best == nil || best.Type == Delete {
		return false, "", ""
	}
	return true, best.Value, ""
}

func (n *naiveStore) snapshot() Snapshot {
	snap := Snapshot{
		Oracle:    n.oracle,
		Watermark: n.watermark,
		Versions:  map[string][]Version{},
		Locks:     map[string]Lock{},
	}
	for k, ks := range n.keys {
		if len(ks.versions) > 0 {
			vs := make([]Version, len(ks.versions))
			copy(vs, ks.versions)
			snap.Versions[k] = vs
		}
		if ks.lock != nil {
			snap.Locks[k] = *ks.lock
		}
	}
	return snap
}
