// Package objstore implements a tenant object store with conditional
// writes, a per-tenant versioning switch and net-delta quota
// accounting. All operations are safe for concurrent use and behave
// as if executed in some serial order.
//
// Check order for every write (only the first failure is reported):
//
//	invalid parameter > precondition failed > target not found
//	> quota exceeded > injected storage failure
package objstore

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/cond"
	"ontology/quota"
)

const (
	maxSize  = int64(1_000_000_000_000)
	maxQuota = int64(1_000_000_000_000_000)
)

// Sentinel errors; use errors.Is to match them.
var (
	ErrInvalidParam   = errors.New("objstore: invalid parameter")
	ErrPrecondition   = errors.New("objstore: precondition failed")
	ErrNotFound       = errors.New("objstore: target not found")
	ErrQuotaExceeded  = errors.New("objstore: quota exceeded")
	ErrStorage        = errors.New("objstore: storage failure")
	ErrVersioningLock = errors.New("objstore: versioning cannot be disabled")
)

// PreconditionError reports a failed conditional write and names the
// first failed sub-condition.
type PreconditionError struct {
	Sub string
}

func (e *PreconditionError) Error() string {
	return "objstore: precondition failed: " + e.Sub
}

func (e *PreconditionError) Is(target error) bool { return target == ErrPrecondition }

// version is one stored version of a key. Delete markers have
// tombstone=true and count 0 bytes.
type version struct {
	ver       int64
	size      int64
	etag      string
	mtime     int64
	tombstone bool
}

// keyState keeps versions by number plus their numbers in ascending
// order, so the current version is an O(1) lookup that touches a
// single version record.
type keyState struct {
	vers  map[int64]*version
	order []int64 // ascending version numbers
}

func (ks *keyState) current() *version {
	if ks == nil || len(ks.order) == 0 {
		return nil
	}
	return ks.vers[ks.order[len(ks.order)-1]]
}

func (ks *keyState) remove(ver int64) {
	delete(ks.vers, ver)
	i := sort.Search(len(ks.order), func(i int) bool { return ks.order[i] >= ver })
	if i < len(ks.order) && ks.order[i] == ver {
		ks.order = append(ks.order[:i], ks.order[i+1:]...)
	}
}

type tenant struct {
	versioning bool
	lastVer    int64 // last allocated version number; rejected ops never consume one
	keys       map[string]*keyState
}

// Store is the tenant object store.
type Store struct {
	mu           sync.Mutex
	ledger       *quota.Ledger
	afterReserve func() error
	tenants      map[string]*tenant
	touched      int // version records touched while determining the current version
}

// New returns an empty store.
func New() *Store {
	return &Store{
		ledger:  quota.New(),
		tenants: make(map[string]*tenant),
	}
}

// SetAfterReserveHook injects a hook that runs after the quota
// reservation and before the install. A non-nil error aborts the
// write as ErrStorage and rolls the reservation back. The hook runs
// under the store lock and must not call back into the store.
func (s *Store) SetAfterReserveHook(h func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterReserve = h
}

// SetQuota sets the tenant's quota Q (0..1e15). It is always accepted,
// even when Q drops below the current usage U (over-quota state).
func (s *Store) SetQuota(t string, q int64) error {
	if t == "" || q < 0 || q > maxQuota {
		return ErrInvalidParam
	}
	s.ledger.SetQuota(t, q)
	return nil
}

// Usage returns the tenant's usage U: the sum of the sizes of all
// stored versions (delete markers count 0 bytes).
func (s *Store) Usage(t string) int64 { return s.ledger.Usage(t) }

// SetVersioning switches the tenant's bucket versioning on. It can
// only go from off to on: enabling an enabled bucket is idempotent,
// passing on=false while disabled is a no-op, and passing on=false
// while enabled fails with ErrVersioningLock.
func (s *Store) SetVersioning(t string, on bool) error {
	if t == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenant(t)
	switch {
	case on:
		tn.versioning = true // off->on, or idempotent no-op
		return nil
	case tn.versioning:
		return ErrVersioningLock
	default:
		return nil // disabled and staying disabled
	}
}

func (s *Store) tenant(t string) *tenant {
	tn := s.tenants[t]
	if tn == nil {
		tn = &tenant{keys: make(map[string]*keyState)}
		s.tenants[t] = tn
	}
	return tn
}

// current returns the key's current version, counting the touch.
func (s *Store) current(ks *keyState) *version {
	if ks == nil || len(ks.order) == 0 {
		return nil
	}
	s.touched++
	return ks.current()
}

func curInfo(v *version) cond.Current {
	if v == nil {
		return cond.Current{}
	}
	return cond.Current{Exists: true, Tombstone: v.tombstone, Etag: v.etag, Mtime: v.mtime}
}

// checkPrecond evaluates c against the key's current version.
func checkPrecond(c cond.Cond, cur *version) error {
	if sub, ok := c.Check(curInfo(cur)); !ok {
		return &PreconditionError{Sub: sub}
	}
	return nil
}

// Put stores size bytes under key with the given etag, subject to the
// precondition c. With versioning off it overwrites the current
// object (releasing the old bytes); with versioning on it appends a
// new version and returns its number. It returns the written version
// number (0 when versioning is off).
func (s *Store) Put(t, key string, size int64, etag string, c cond.Cond, now int64) (int64, error) {
	if t == "" || key == "" || etag == "" ||
		size < 0 || size > maxSize || now < 0 || now > cond.MaxTime || !c.Valid() {
		return 0, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tn := s.tenant(t)
	ks := tn.keys[key]
	cur := s.current(ks)
	if err := checkPrecond(c, cur); err != nil {
		return 0, err
	}

	// Net delta: overwrite releases the old bytes, append does not.
	d := size
	if !tn.versioning && cur != nil {
		d = size - cur.size
	}
	tok, ok := s.ledger.Reserve(t, d)
	if !ok {
		return 0, ErrQuotaExceeded
	}
	if s.afterReserve != nil {
		if err := s.afterReserve(); err != nil {
			tok.Release()
			return 0, fmt.Errorf("%w: %v", ErrStorage, err)
		}
	}

	v := &version{size: size, etag: etag, mtime: now}
	if !tn.versioning {
		tn.keys[key] = &keyState{vers: map[int64]*version{0: v}, order: []int64{0}}
	} else {
		tn.lastVer++
		v.ver = tn.lastVer
		if ks == nil {
			ks = &keyState{vers: make(map[int64]*version)}
			tn.keys[key] = ks
		}
		ks.vers[v.ver] = v
		ks.order = append(ks.order, v.ver)
	}
	tok.Commit()
	return v.ver, nil
}

// Delete removes an object or a version. ver=-1 means "unspecified":
// with versioning off it deletes the key's only object (ErrNotFound
// when absent); with versioning on it appends a delete marker (0
// bytes, consumes a version number, even when the key has no
// versions). ver>=0 permanently deletes that version, including
// markers and version 0.
func (s *Store) Delete(t, key string, ver int64, c cond.Cond, now int64) error {
	if t == "" || key == "" || ver < -1 || now < 0 || now > cond.MaxTime || !c.Valid() {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tn := s.tenant(t)
	if !tn.versioning && ver != -1 {
		return ErrInvalidParam
	}
	ks := tn.keys[key]
	cur := s.current(ks)
	if err := checkPrecond(c, cur); err != nil {
		return err
	}

	var d int64
	var target *version // non-nil for permanent deletes
	switch {
	case !tn.versioning:
		if cur == nil {
			return ErrNotFound
		}
		target = cur
		d = -cur.size
	case ver == -1:
		d = 0 // delete marker
	default:
		if ks != nil {
			s.touched++
			target = ks.vers[ver]
		}
		if target == nil {
			return ErrNotFound
		}
		d = -target.size
	}
	tok, ok := s.ledger.Reserve(t, d)
	if !ok {
		return ErrQuotaExceeded
	}
	if s.afterReserve != nil {
		if err := s.afterReserve(); err != nil {
			tok.Release()
			return fmt.Errorf("%w: %v", ErrStorage, err)
		}
	}

	switch {
	case !tn.versioning:
		delete(tn.keys, key)
	case ver == -1:
		tn.lastVer++
		marker := &version{ver: tn.lastVer, mtime: now, tombstone: true}
		if ks == nil {
			ks = &keyState{vers: make(map[int64]*version)}
			tn.keys[key] = ks
		}
		ks.vers[marker.ver] = marker
		ks.order = append(ks.order, marker.ver)
	default:
		ks.remove(ver)
		if len(ks.order) == 0 {
			delete(tn.keys, key)
		}
	}
	tok.Commit()
	return nil
}

// Stat returns the key's current version, for inspection and tests.
func (s *Store) Stat(t, key string) (ver, size int64, etag string, mtime int64, tombstone, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return 0, 0, "", 0, false, false
	}
	cur := s.current(tn.keys[key])
	if cur == nil {
		return 0, 0, "", 0, false, false
	}
	return cur.ver, cur.size, cur.etag, cur.mtime, cur.tombstone, true
}
