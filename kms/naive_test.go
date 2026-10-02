package kms

// naiveManager is a deliberately simple reference implementation used to
// cross-check Manager: it rotates one version at a time, scans the whole
// key map for expiries and keeps no heap and no counters. Small rotation
// periods keep the per-version expansion cheap.
type naiveManager struct {
	v      int64
	wmin   int64
	wmax   int64
	kmax   int64
	maxNow int64
	keys   map[string]*naiveKey
	genMax map[string]int64
}

type naiveKey struct {
	state    State
	gen      int64
	p        int64
	versions []Version
	d        int64
	deleteAt int64
}

func newNaiveManager(v, wmin, wmax, kmax int64) *naiveManager {
	return &naiveManager{
		v:      v,
		wmin:   wmin,
		wmax:   wmax,
		kmax:   kmax,
		keys:   make(map[string]*naiveKey),
		genMax: make(map[string]int64),
	}
}

// catchUp rotates one version at a time, exactly as the rules expand.
func (k *naiveKey) catchUp(now, v int64) {
	if k.state != Enabled || k.p == 0 {
		return
	}
	for k.d <= now {
		maxNum := k.versions[len(k.versions)-1].Number
		k.versions = append(k.versions, Version{Number: maxNum + 1, CreatedAt: k.d})
		k.d += k.p
		if int64(len(k.versions)) > v {
			k.versions = k.versions[1:]
		}
	}
}

func (m *naiveManager) live(id string, now int64) *naiveKey {
	k := m.keys[id]
	if k == nil {
		return nil
	}
	if k.state == Pending && k.deleteAt <= now {
		return nil
	}
	return k
}

func (m *naiveManager) liveCount(now int64) int64 {
	var n int64
	for _, k := range m.keys {
		if k.state == Pending && k.deleteAt <= now {
			continue
		}
		n++
	}
	return n
}

func (m *naiveManager) checkClock(now int64) *Error {
	if now < m.maxNow {
		return &Error{Code: ErrClockRegression, Reason: "now below max accepted now", MaxNow: m.maxNow, Limit: now}
	}
	return nil
}

func (m *naiveManager) Create(id string, p, now int64) (int64, error) {
	if id == "" {
		return 0, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if !validPeriod(p) {
		return 0, &Error{Code: ErrInvalidParam, ID: id, Reason: "rotation period not 0 and out of range [60,1e9]", Limit: p}
	}
	if err := checkNow(now); err != nil {
		return 0, err
	}
	if err := m.checkClock(now); err != nil {
		return 0, err
	}
	if k := m.live(id, now); k != nil {
		return 0, &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "live key with same id already exists"}
	}
	if m.liveCount(now)+1 > m.kmax {
		return 0, &Error{Code: ErrLimitExceeded, ID: id, Reason: "live key count would exceed Kmax", Limit: m.kmax}
	}
	m.maxNow = now
	gen := m.genMax[id] + 1
	m.genMax[id] = gen
	m.keys[id] = &naiveKey{
		state:    Enabled,
		gen:      gen,
		p:        p,
		versions: []Version{{Number: 1, CreatedAt: now}},
		d:        now + p,
	}
	return gen, nil
}

func (m *naiveManager) Encrypt(id string, now int64) (Credential, error) {
	if id == "" {
		return Credential{}, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if err := checkNow(now); err != nil {
		return Credential{}, err
	}
	if err := m.checkClock(now); err != nil {
		return Credential{}, err
	}
	k := m.live(id, now)
	if k == nil {
		return Credential{}, &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	if k.state != Enabled {
		return Credential{}, deniedErr(id, k.state)
	}
	m.maxNow = now
	k.catchUp(now, m.v)
	return Credential{ID: id, Gen: k.gen, Version: k.versions[len(k.versions)-1].Number}, nil
}

func (m *naiveManager) checkCredential(cred Credential, now int64) (*naiveKey, *Error) {
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
	k := m.live(cred.ID, now)
	if k == nil || k.gen != cred.Gen {
		return nil, &Error{Code: ErrKeyDeleted, ID: cred.ID, Gen: cred.Gen, Reason: "generation is not the live generation (deleted or superseded)"}
	}
	if k.state != Enabled {
		return nil, deniedErr(cred.ID, k.state)
	}
	return k, nil
}

func (m *naiveManager) Decrypt(cred Credential, now int64) (bool, error) {
	k, err := m.checkCredential(cred, now)
	if err != nil {
		return false, err
	}
	cp := *k
	cp.versions = append([]Version(nil), k.versions...)
	cp.catchUp(now, m.v)
	if cerr := checkVersion(cred, cp.versions[0].Number, cp.versions[len(cp.versions)-1].Number); cerr != nil {
		return false, cerr
	}
	m.maxNow = now
	k.catchUp(now, m.v)
	return cred.Version == k.versions[len(k.versions)-1].Number, nil
}

func (m *naiveManager) ReEncrypt(cred Credential, now int64) (Credential, bool, error) {
	k, err := m.checkCredential(cred, now)
	if err != nil {
		return Credential{}, false, err
	}
	cp := *k
	cp.versions = append([]Version(nil), k.versions...)
	cp.catchUp(now, m.v)
	if cerr := checkVersion(cred, cp.versions[0].Number, cp.versions[len(cp.versions)-1].Number); cerr != nil {
		return Credential{}, false, cerr
	}
	m.maxNow = now
	k.catchUp(now, m.v)
	fresh := Credential{ID: cred.ID, Gen: cred.Gen, Version: k.versions[len(k.versions)-1].Number}
	if fresh.Version == cred.Version {
		return cred, false, nil
	}
	return fresh, true, nil
}

func (m *naiveManager) lookup(id string, now int64) (*naiveKey, *Error) {
	if id == "" {
		return nil, &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if err := checkNow(now); err != nil {
		return nil, err
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	k := m.live(id, now)
	if k == nil {
		return nil, &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	return k, nil
}

func (m *naiveManager) Disable(id string, now int64) error {
	k, err := m.lookup(id, now)
	if err != nil {
		return err
	}
	if k.state != Enabled {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "disable requires an Enabled key"}
	}
	m.maxNow = now
	k.catchUp(now, m.v)
	k.state = Disabled
	return nil
}

func (m *naiveManager) Enable(id string, now int64) error {
	k, err := m.lookup(id, now)
	if err != nil {
		return err
	}
	if k.state != Disabled {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "enable requires a Disabled key"}
	}
	m.maxNow = now
	k.state = Enabled
	if k.p > 0 && k.d <= now {
		k.d = now + k.p
	}
	return nil
}

func (m *naiveManager) ScheduleDeletion(id string, w, now int64) error {
	if id == "" {
		return &Error{Code: ErrInvalidParam, Reason: "empty key id"}
	}
	if w < m.wmin || w > m.wmax {
		return &Error{Code: ErrInvalidParam, ID: id, Reason: "deletion window outside [Wmin,Wmax]", Limit: w}
	}
	if err := checkNow(now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	k := m.live(id, now)
	if k == nil {
		return &Error{Code: ErrNotFound, ID: id, Reason: "no live key with this id"}
	}
	if k.state != Enabled && k.state != Disabled {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "schedule-deletion requires Enabled or Disabled"}
	}
	m.maxNow = now
	if k.state == Enabled {
		k.catchUp(now, m.v)
	}
	k.state = Pending
	k.deleteAt = now + w
	return nil
}

func (m *naiveManager) CancelDeletion(id string, now int64) error {
	k, err := m.lookup(id, now)
	if err != nil {
		return err
	}
	if k.state != Pending {
		return &Error{Code: ErrStateConflict, ID: id, State: k.state, Reason: "cancel-deletion requires a Pending key"}
	}
	m.maxNow = now
	k.state = Disabled
	return nil
}

func (m *naiveManager) Describe(id string, now int64) (KeyView, error) {
	k, err := m.lookup(id, now)
	if err != nil {
		return KeyView{}, err
	}
	cp := *k
	cp.versions = append([]Version(nil), k.versions...)
	if cp.state == Enabled {
		cp.catchUp(now, m.v)
	}
	return KeyView{
		ID:           id,
		State:        cp.state,
		Gen:          cp.gen,
		Versions:     cp.versions,
		NextRotation: cp.d,
		DeleteAt:     cp.deleteAt,
	}, nil
}
